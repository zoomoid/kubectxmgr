package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/zoomoid/kubectxmgr/internal/cmd"
	"github.com/zoomoid/kubectxmgr/internal/execution"
	"github.com/zoomoid/kubectxmgr/internal/tui"
)

// version defaults to local development and is replaced by GoReleaser at link time.
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	root := cmd.New()
	root.Version = version

	if err := root.ExecuteContext(ctx); err != nil {
		if errors.Is(err, tui.ErrCancelled) {
			os.Exit(130)
		}

		fmt.Fprintln(os.Stderr, "switcher:", err)

		var exit *execution.ExitError
		if errors.As(err, &exit) {
			os.Exit(exit.Code)
		}

		os.Exit(1)
	}
}
