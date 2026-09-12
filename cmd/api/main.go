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
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := runtime.Run(ctx, false); err != nil {
		var configurationError config.Error
		if errors.As(err, &configurationError) {
			fmt.Fprintln(os.Stderr, configurationError.Error())
		} else {
			fmt.Fprintln(os.Stderr, "process startup or shutdown failed; verify configuration and dependency health")
		}
		os.Exit(1)
	}
}
