package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/NET-BEAR/ohelpdesck/internal/platform/config"
	"github.com/NET-BEAR/ohelpdesck/internal/platform/runtime"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	os.Exit(runWorker(func(ctx context.Context) error { return runtime.Run(ctx, true) }))
}

// runWorker owns the process signal boundary so the production entrypoint and
// the subprocess shutdown regression execute the same SIGTERM handling path.
func runWorker(run func(context.Context) error) int {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx); err != nil {
		var configurationError config.Error
		if errors.As(err, &configurationError) {
			fmt.Fprintln(os.Stderr, configurationError.Error())
		} else {
			fmt.Fprintln(os.Stderr, "process startup or shutdown failed; verify configuration and dependency health")
		}
		return 1
	}
	return 0
}
