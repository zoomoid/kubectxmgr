// Package state persists private application data with atomic replacement and
// cross-process writer locks. Readers never acquire discovery locks.
package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"go.yaml.in/yaml/v3"
	"golang.org/x/sys/unix"
)

// Version is the schema version of snapshots, status, selections, and credentials.
const Version = 1

// Reference identifies a selectable context without storing its credentials.
// ID is stable across display-name changes; Metadata supports provider retrieval.
type Reference struct {
	ID        string            `yaml:"id" json:"id"`
	Store     string            `yaml:"store" json:"store"`
	Name      string            `yaml:"name" json:"name"`
	Context   string            `yaml:"context" json:"context"`
	Namespace string            `yaml:"namespace,omitempty" json:"namespace,omitempty"`
	Metadata  map[string]string `yaml:"metadata" json:"metadata"`
}

// Snapshot contains one store's exhaustive inventory for a configuration fingerprint.
// Failed or interrupted scans must never publish an incomplete replacement.
type Snapshot struct {
	Version     int         `yaml:"version"`
	Fingerprint string      `yaml:"fingerprint"`
	Complete    bool        `yaml:"complete"`
	Discovered  time.Time   `yaml:"discoveredAt"`
	References  []Reference `yaml:"references"`
}

// Status records the latest discovery attempt independently of the usable inventory.
type Status struct {
	Version int       `yaml:"version"`
	Attempt time.Time `yaml:"attempt"`
	State   string    `yaml:"state"`
	Error   string    `yaml:"error,omitempty"`
}

// Selection stores context, namespace, and kubeconfig as one atomic restoration unit.
type Selection struct {
	Version    int    `yaml:"version"`
	Context    string `yaml:"context"`
	Namespace  string `yaml:"namespace"`
	Kubeconfig []byte `yaml:"kubeconfig"`
}

// Credentials is an expiring kubeconfig cache entry with ownership and lock identity.
type Credentials struct {
	Owner   string    `yaml:"owner"`
	Key     string    `yaml:"key"`
	Version int       `yaml:"version"`
	Expires time.Time `yaml:"expires"`
	Data    []byte    `yaml:"data"`
}

// Disk locates the private state tree used by one installation.
type Disk struct{ Root string }

// Hash derives a filesystem-safe identifier without exposing the original key.
func Hash(value string) string {
	h := sha256.Sum256([]byte(value))
	return hex.EncodeToString(h[:])
}

// Path returns the YAML path for a logical key within a state category.
func (d Disk) Path(kind, key string) string {
	return filepath.Join(d.Root, kind, Hash(key)+".yaml")
}

// Read decodes a YAML state file without acquiring writer locks.
// The caller remains responsible for validating schema version and completeness.
func Read[T any](path string) (T, error) {
	var value T
	b, err := os.ReadFile(path)
	if err != nil {
		return value, err
	}

	if err := yaml.Unmarshal(b, &value); err != nil {
		return value, fmt.Errorf("decode state %s: %w", path, err)
	}

	return value, nil
}

// PrivateDir creates a directory with mode 0700 or checks an existing directory.
// It refuses permissive existing directories rather than changing user-owned modes.
func PrivateDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}

	info, err := os.Stat(path)
	if err != nil {
		return err
	}

	if info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("directory %s is not private; set its permissions to 0700", path)
	}

	return nil
}

// Atomic marshals a value as YAML and publishes it through Write.
// Callers needing serialized read-modify-write operations must also hold a lock.
func Atomic(path string, value any) error {
	b, err := yaml.Marshal(value)
	if err != nil {
		return err
	}

	return Write(path, b)
}

// Write replaces a file with mode 0600 after syncing a temporary file in the same
// directory. Readers see either the old or the complete new file, never a partial write.
func Write(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	f, err := os.CreateTemp(dir, ".write-")
	if err != nil {
		return err
	}

	name := f.Name()
	defer func() { _ = os.Remove(name) }()

	if err := f.Chmod(0600); err != nil {
		_ = f.Close()
		return err
	}

	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}

	// Flush file data and close before making the replacement visible.
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}

	if err := f.Close(); err != nil {
		return err
	}

	if err := os.Rename(name, path); err != nil {
		return err
	}

	// Persist the directory entry where supported; some filesystems cannot sync it.
	df, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = df.Close() }()

	if err := df.Sync(); err != nil && !errors.Is(err, unix.EINVAL) && !errors.Is(err, unix.ENOTSUP) {
		return err
	}

	return nil
}

// ErrBusy indicates that a non-waiting lock request found an active writer.
var ErrBusy = errors.New("discovery already running")

// Lock acquires an advisory process lock and returns its release function.
// With wait set, contention is retried until acquisition or context cancellation;
// otherwise contention returns ErrBusy. Lock files remain in place after release
// so concurrent processes always lock the same inode.
func (d Disk) Lock(ctx context.Context, key string, wait bool) (func(), error) {
	path := d.Path("locks", key)
	if err := PrivateDir(filepath.Dir(path)); err != nil {
		return nil, err
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}

	for {
		err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return func() { _ = f.Close() }, nil
		}

		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			_ = f.Close()
			return nil, err
		}

		if !wait {
			_ = f.Close()
			return nil, ErrBusy
		}

		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}

// Snapshot reads a complete inventory only if its schema and fingerprint match.
func (d Disk) Snapshot(key, fingerprint string) (Snapshot, error) {
	s, err := Read[Snapshot](d.Path("inventory", key))
	if err != nil {
		return s, err
	}

	if s.Version != Version || !s.Complete || s.Fingerprint != fingerprint {
		return s, errors.New("incompatible or incomplete inventory; run switcher discover")
	}

	return s, nil
}

// SaveSelection serializes selection writers and atomically publishes the latest
// successful context/namespace pair together with its durable kubeconfig.
func (d Disk) SaveSelection(ctx context.Context, s Selection) error {
	unlock, err := d.Lock(ctx, "last-selection", true)
	if err != nil {
		return err
	}
	defer unlock()

	s.Version = Version

	return Atomic(filepath.Join(d.Root, "last.yaml"), s)
}

// Last reads the saved restoration unit without taking writer or discovery locks.
// Missing or incompatible state is an explicit error, never a request to discover.
func (d Disk) Last() (Selection, error) {
	s, err := Read[Selection](filepath.Join(d.Root, "last.yaml"))
	if errors.Is(err, fs.ErrNotExist) {
		return s, errors.New("no saved selection; select a context first with switcher")
	}

	if err != nil {
		return s, err
	}

	if s.Version != Version || s.Context == "" || len(s.Kubeconfig) == 0 {
		return s, errors.New("invalid saved selection; select a context again")
	}

	return s, nil
}
