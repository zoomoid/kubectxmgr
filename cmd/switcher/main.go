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
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := cmd.New().ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "switcher:", err)

		var exit *execution.ExitError
		if errors.As(err, &exit) {
			os.Exit(exit.Code)
		}

		os.Exit(1)
	}
}
