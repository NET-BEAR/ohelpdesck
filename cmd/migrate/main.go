package main

import (
	"context"
	"fmt"
	"github.com/NET-BEAR/ohelpdesck/internal/platform/database"
	"os"
	"time"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: migrate up|down|status")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	p, e := database.Open(ctx, os.Getenv("DATABASE_URL"), 2)
	if e != nil {
		return fmt.Errorf("database unavailable")
	}
	defer p.Close()
	v, e := p.Migrate(ctx, args[0])
	if e != nil {
		return fmt.Errorf("migration failed")
	}
	fmt.Printf("migration version: %d\n", v)
	return nil
}
