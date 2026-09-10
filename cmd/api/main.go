package main

import (
	"context"
	"fmt"
	"github.com/krassus/ohelpdesck/internal/platform/runtime"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if runtime.Run(ctx, false) != nil {
		fmt.Fprintln(os.Stderr, "process startup or shutdown failed; verify configuration and dependency health")
		os.Exit(1)
	}
}
