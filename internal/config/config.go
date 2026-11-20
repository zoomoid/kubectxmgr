// Package config loads the supported SwitchConfig format and resolves store settings.
package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// DefaultInterval is the refresh age used when neither config nor store overrides it.
const DefaultInterval = 15 * time.Minute

// Config preserves the supported SwitchConfig fields and resolved discovery settings.
type Config struct {
	Kind              string    `yaml:"kind"`
	Version           string    `yaml:"version"`
	KubeconfigName    string    `yaml:"kubeconfigName"`
	ShowPreview       *bool     `yaml:"showPreview,omitempty"`
	ExecShell         string    `yaml:"execShell,omitempty"`
	RefreshIndexAfter string    `yaml:"refreshIndexAfter,omitempty"`
	Stores            []Store   `yaml:"kubeconfigStores"`
	Discovery         Discovery `yaml:"discovery,omitempty"`
	Warnings          []string  `yaml:"-"`
	Path              string    `yaml:"-"`
}

// Discovery bounds parallel store scans and the duration of each scan.
type Discovery struct {
	Concurrency int    `yaml:"concurrency,omitempty"`
	Timeout     string `yaml:"timeout,omitempty"`
}

// Store describes one independently discovered and atomically published inventory.
type Store struct {
	Kind              string        `yaml:"kind"`
	ID                string        `yaml:"id,omitempty"`
	Paths             []string      `yaml:"paths,omitempty"`
	KubeconfigName    string        `yaml:"kubeconfigName,omitempty"`
	ShowPrefix        *bool         `yaml:"showPrefix,omitempty"`
	Required          *bool         `yaml:"required,omitempty"`
	RefreshIndexAfter string        `yaml:"refreshIndexAfter,omitempty"`
	Config            Settings      `yaml:"config,omitempty"`
	Cache             *Cache        `yaml:"cache,omitempty"`
	Interval          time.Duration `yaml:"-"`
}

// Cache configures an optional filesystem cache for materialized credentials.
type Cache struct {
	Kind   string `yaml:"kind"`
	Config struct {
		Path string `yaml:"path"`
	} `yaml:"config"`
}

// Settings contains provider-specific options; validation rejects cross-provider fields.
type Settings struct {
	GCPAccount                string   `yaml:"gcpAccount,omitempty"`
	Authentication            *Auth    `yaml:"authentication,omitempty"`
	OldAuthentication         *Auth    `yaml:"Authentication,omitempty"`
	ProjectIDs                []string `yaml:"projectIDs,omitempty"`
	PreferredEndpoint         string   `yaml:"preferredEndpoint,omitempty"`
	Profile                   string   `yaml:"profile,omitempty"`
	Region                    string   `yaml:"region,omitempty"`
	SubscriptionID            string   `yaml:"subscriptionID,omitempty"`
	ResourceGroups            []string `yaml:"resourceGroups,omitempty"`
	Endpoint                  string   `yaml:"endpoint,omitempty"`
	RancherAPIAddress         string   `yaml:"rancherAPIAddress,omitempty"`
	RancherToken              string   `yaml:"rancherToken,omitempty"`
	RancherTokenFile          string   `yaml:"rancherTokenFile,omitempty"`
	LegacyAPI                 bool     `yaml:"legacyAPI,omitempty"`
	AuthorizedClusterEndpoint bool     `yaml:"authorizedClusterEndpoint,omitempty"`
}

// Auth selects GKE authentication through ADC, gcloud, or a service-account file.
type Auth struct {
	Type                   string `yaml:"authenticationType,omitempty"`
	ServiceAccountFilePath string `yaml:"serviceAccountFilePath,omitempty"`
}

// Expand expands a leading home-directory shorthand and environment references.
// Unset environment variables are errors rather than silently empty path segments.
func Expand(path string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	if path == "~" {
		path = home
	} else if strings.HasPrefix(path, "~/") {
		path = filepath.Join(home, path[2:])
	}

	var missing string
	path = os.Expand(path, func(key string) string {
		value, ok := os.LookupEnv(key)
		if !ok {
			missing = key
		}

		return value
	})

	if missing != "" {
		return "", fmt.Errorf("environment variable %s is unset", missing)
	}

	return path, nil
}

// Decode parses exactly one YAML document and rejects unsupported configuration fields.
// Validation and path resolution are separate so Load can first set the config path.
func Decode(data []byte) (*Config, error) {
	c := &Config{}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(c); err != nil {
		return nil, fmt.Errorf("decode SwitchConfig (unsupported fields must be removed): %w", err)
	}

	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("configuration must contain exactly one YAML document")
	}

	return c, nil
}

// Load combines a config file with explicit, environment, and default kubeconfig
// sources, then validates and resolves them. A missing config file is allowed;
// tool-owned KUBECONFIG paths are excluded to avoid rediscovering session copies.
func Load(path, extraPath, stateDir string) (*Config, error) {
	path, err := Expand(path)
	if err != nil {
		return nil, err
	}

	path, err = filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	c := &Config{Kind: "SwitchConfig", Version: "v1alpha1"}
	data, err := os.ReadFile(path)
	if err == nil {
		c, err = Decode(data)
	}

	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("load %s: %w", path, err)
	}

	c.Path = path
	if c.ShowPreview != nil {
		c.Warnings = append(c.Warnings, "showPreview is deprecated and has no effect")
	}

	if extraPath != "" {
		extraPath, err = Expand(extraPath)
		if err != nil {
			return nil, err
		}

		extraPath, err = filepath.Abs(extraPath)
		if err != nil {
			return nil, err
		}

		c.Stores = append(c.Stores, Store{Kind: "filesystem", ID: "flag", Paths: []string{extraPath}})
	}

	var paths []string
	for _, path := range filepath.SplitList(os.Getenv("KUBECONFIG")) {
		if path == "" || OwnedPath(stateDir, path) {
			continue
		}

		path, err = filepath.Abs(path)
		if err != nil {
			return nil, err
		}

		duplicate := false
		for _, s := range c.Stores {
			for _, existing := range s.Paths {
				expanded, err := Expand(existing)
				if err != nil {
					return nil, err
				}

				if filepath.Clean(expanded) == filepath.Clean(path) {
					duplicate = true
				}
			}
		}

		if !duplicate {
			paths = append(paths, path)
		}
	}

	if len(paths) > 0 {
		c.Stores = append(c.Stores, Store{Kind: "filesystem", ID: "environment", Paths: paths})
	}

	defaultPath, err := Expand("~/.kube/config")
	if err != nil {
		return nil, err
	}

	defaultConfigured := false
	for _, s := range c.Stores {
		for _, p := range s.Paths {
			expanded, err := Expand(p)
			if err != nil {
				return nil, err
			}

			if expanded == defaultPath {
				defaultConfigured = true
			}
		}
	}

	if !defaultConfigured {
		if _, err := os.Stat(defaultPath); err == nil {
			c.Stores = append(c.Stores, Store{Kind: "filesystem", ID: "default-kubeconfig", Paths: []string{defaultPath}})
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}

	if err := c.Validate(); err != nil {
		return nil, err
	}

	return c, nil
}

// OwnedPath reports lexical containment in root, including root itself.
// Callers traversing symlinks must resolve them first when physical containment matters.
func OwnedPath(root, path string) bool {
	root, err := filepath.Abs(root)
	if err != nil {
		return false
	}

	path, err = filepath.Abs(path)
	if err != nil {
		return false
	}

	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// duration applies a default only to an absent value, never to an invalid duration.
func duration(value string, fallback time.Duration) (time.Duration, error) {
	if value == "" {
		return fallback, nil
	}

	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("invalid positive duration %q", value)
	}

	return d, nil
}

// Validate normalizes provider names and legacy fields, applies defaults, resolves
// paths, and rejects unsupported or conflicting settings. It mutates the config.
func (c *Config) Validate() error {
	if c.Kind != "SwitchConfig" || c.Version != "v1alpha1" {
		return errors.New("expected kind SwitchConfig and version v1alpha1")
	}

	interval, err := duration(c.RefreshIndexAfter, DefaultInterval)
	if err != nil {
		return err
	}

	if c.Discovery.Concurrency == 0 {
		c.Discovery.Concurrency = 4
	}

	if c.Discovery.Concurrency < 1 || c.Discovery.Concurrency > 64 {
		return errors.New("discovery.concurrency must be between 1 and 64")
	}

	if _, err := duration(c.Discovery.Timeout, 2*time.Minute); err != nil {
		return err
	}

	ids := map[string]bool{}
	for i := range c.Stores {
		s := &c.Stores[i]
		if s.Kind == "aks" {
			s.Kind = "azure"
		}

		switch s.Kind {
		case "filesystem", "gke", "eks", "azure", "rancher":
		default:
			return fmt.Errorf("unsupported store kind %q; supported: filesystem, gke, eks, azure/aks, rancher", s.Kind)
		}

		if s.ID == "" {
			s.ID = "default"
		}

		if ids[s.Key()] {
			return fmt.Errorf("duplicate store %s: give each store a unique id", s.Key())
		}
		ids[s.Key()] = true

		s.Interval, err = duration(s.RefreshIndexAfter, interval)
		if err != nil {
			return fmt.Errorf("%s: %w", s.Key(), err)
		}

		if s.KubeconfigName == "" {
			s.KubeconfigName = c.KubeconfigName
		}

		if s.KubeconfigName == "" {
			s.KubeconfigName = "config"
		}

		if _, err := filepath.Match(s.KubeconfigName, ""); err != nil {
			return fmt.Errorf("%s kubeconfigName: %w", s.Key(), err)
		}

		for j := range s.Paths {
			path, err := c.resolvePath(s.Paths[j])
			if err != nil {
				return err
			}

			s.Paths[j], err = filepath.Abs(path)
			if err != nil {
				return err
			}
		}

		a := &s.Config
		if a.Authentication != nil && a.OldAuthentication != nil {
			return fmt.Errorf("%s: conflicting authentication and Authentication fields", s.Key())
		}

		if a.Authentication == nil {
			a.Authentication = a.OldAuthentication
			a.OldAuthentication = nil
		}

		if err := s.validateSettings(); err != nil {
			return err
		}

		if s.Kind == "eks" {
			if a.Profile == "" {
				a.Profile = os.Getenv("AWS_PROFILE")
				if a.Profile == "" {
					a.Profile = os.Getenv("AWS_DEFAULT_PROFILE")
				}
			}

			if a.Region == "" {
				a.Region = os.Getenv("AWS_REGION")
				if a.Region == "" {
					a.Region = os.Getenv("AWS_DEFAULT_REGION")
				}
			}
		}

		if a.Authentication != nil && a.Authentication.Type != "" && a.Authentication.Type != "gcloud" && a.Authentication.Type != "service-account" {
			return fmt.Errorf("%s: use gcloud/ADC or service-account authentication, not %q", s.Key(), a.Authentication.Type)
		}

		if a.Authentication != nil && a.Authentication.Type == "service-account" {
			if a.GCPAccount != "" {
				return fmt.Errorf("%s: gcpAccount cannot be combined with service-account authentication", s.Key())
			}

			if a.Authentication.ServiceAccountFilePath == "" {
				return fmt.Errorf("%s: service-account requires serviceAccountFilePath", s.Key())
			}

			a.Authentication.ServiceAccountFilePath, err = c.resolvePath(a.Authentication.ServiceAccountFilePath)
			if err != nil {
				return err
			}
		}

		if a.RancherTokenFile != "" {
			a.RancherTokenFile, err = c.resolvePath(a.RancherTokenFile)
			if err != nil {
				return err
			}
		}

		if strings.Contains(a.RancherToken, "$") {
			a.RancherToken, err = Expand(a.RancherToken)
			if err != nil {
				return err
			}
		}

		if a.PreferredEndpoint != "" && a.PreferredEndpoint != "dns" && a.PreferredEndpoint != "private" && a.PreferredEndpoint != "public" {
			return fmt.Errorf("%s: preferredEndpoint must be dns, private, or public", s.Key())
		}

		if s.Kind == "filesystem" && len(s.Paths) == 0 {
			return fmt.Errorf("%s: paths must not be empty", s.Key())
		}

		if s.Kind == "azure" && a.SubscriptionID == "" {
			return fmt.Errorf("%s: subscriptionID is required", s.Key())
		}

		if s.Kind == "rancher" && (a.RancherAPIAddress == "" || (a.RancherToken == "" && a.RancherTokenFile == "")) {
			return fmt.Errorf("%s: rancherAPIAddress and rancherToken or rancherTokenFile are required", s.Key())
		}

		if a.RancherToken != "" && a.RancherTokenFile != "" {
			return fmt.Errorf("%s: choose rancherToken or rancherTokenFile", s.Key())
		}

		if s.Cache != nil {
			if s.Cache.Kind != "filesystem" || s.Cache.Config.Path == "" {
				return fmt.Errorf("%s: cache requires kind filesystem and config.path", s.Key())
			}

			s.Cache.Config.Path, err = c.resolvePath(s.Cache.Config.Path)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// resolvePath anchors relative settings to the configuration file, not the shell.
func (c *Config) resolvePath(path string) (string, error) {
	path, err := Expand(path)
	if err != nil {
		return "", err
	}

	if !filepath.IsAbs(path) && c.Path != "" {
		path = filepath.Join(filepath.Dir(c.Path), path)
	}

	return filepath.Abs(path)
}

// validateSettings rejects recognized fields when used with the wrong provider.
func (s Store) validateSettings() error {
	allowed := map[string]string{
		"filesystem": "",
		"gke":        "gcpAccount authentication projectIDs preferredEndpoint",
		"eks":        "profile region",
		"azure":      "subscriptionID resourceGroups endpoint",
		"rancher":    "rancherAPIAddress rancherToken rancherTokenFile legacyAPI authorizedClusterEndpoint",
	}

	b, err := yaml.Marshal(s.Config)
	if err != nil {
		return err
	}

	var fields map[string]any
	if err := yaml.Unmarshal(b, &fields); err != nil {
		return err
	}

	for key := range fields {
		if !strings.Contains(" "+allowed[s.Kind]+" ", " "+key+" ") {
			return fmt.Errorf("%s: config.%s is not supported for this provider", s.Key(), key)
		}
	}

	return nil
}

// Timeout returns the per-store scan limit after configuration validation.
func (c *Config) Timeout() time.Duration {
	d, _ := duration(c.Discovery.Timeout, 2*time.Minute)
	return d
}

// Key is the unique inventory identity within the normalized configuration.
func (s Store) Key() string { return s.Kind + "." + s.ID }

// IsRequired defaults stores to required unless explicitly marked optional.
func (s Store) IsRequired() bool { return s.Required == nil || *s.Required }

// Fingerprint binds persisted inventory and credentials to the store's YAML settings.
func (s Store) Fingerprint() string {
	data, _ := yaml.Marshal(s)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
