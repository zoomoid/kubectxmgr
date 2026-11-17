package exec

import (
	"errors"
	"io"
	"os"
	"sync"
	"time"

	"charm.land/bubbles/v2/spinner"
	"github.com/charmbracelet/x/ansi"
	"github.com/zoomoid/kubectxmgr/internal/logging"
	"golang.org/x/term"
)

// refreshProgress serializes concurrent diagnostics and clears the transient line
// before any warning so cleanup never erases a real diagnostic.
type refreshProgress struct {
	mu      sync.Mutex
	out     io.Writer
	active  bool
	width   int
	spinner spinner.Model
	err     error
	stop    chan struct{}
	done    chan struct{}
}

func (p *refreshProgress) start() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !logging.IsTerminal(p.out) || os.Getenv("TERM") == "dumb" {
		return nil
	}

	file := p.out.(interface{ Fd() uintptr })
	width, _, err := term.GetSize(int(file.Fd()))
	if err != nil {
		return err
	}

	p.active = true
	p.width = width
	p.spinner = spinner.New(spinner.WithSpinner(spinner.Dot))
	if err := p.draw(); err != nil {
		return err
	}

	p.stop, p.done = make(chan struct{}), make(chan struct{})
	go p.animate(p.stop, p.done)

	return nil
}

func (p *refreshProgress) draw() error {
	text := ansi.Truncate(p.spinner.View()+" Discovering clusters...", max(0, p.width-1), "")
	_, err := io.WriteString(p.out, "\r"+logging.Muted(p.out, text))
	return err
}

func (p *refreshProgress) advance() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.active {
		return false
	}

	// Our cancellable ticker owns scheduling instead of a Bubble Tea event loop.
	p.spinner, _ = p.spinner.Update(p.spinner.Tick())
	p.err = p.draw()
	return p.err == nil
}

func (p *refreshProgress) animate(stop, done chan struct{}) {
	defer close(done)
	ticker := time.NewTicker(p.spinner.Spinner.FPS)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			if !p.advance() {
				return
			}
		}
	}
}

func (p *refreshProgress) clear() error {
	if !p.active {
		return nil
	}

	p.active = false
	_, err := io.WriteString(p.out, "\r\x1b[2K")
	return err
}

func (p *refreshProgress) finish() error {
	p.mu.Lock()
	if p.stop != nil {
		close(p.stop)
		p.stop = nil
	}
	done := p.done
	p.mu.Unlock()

	// Join the animator before clearing so it cannot redraw after cleanup.
	if done != nil {
		<-done
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	return errors.Join(p.err, p.clear())
}

func (p *refreshProgress) Write(data []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if err := p.clear(); err != nil {
		return 0, err
	}

	return p.out.Write(data)
}
