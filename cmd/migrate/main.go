package main

import (
	"context"
	"fmt"
	"github.com/krassus/ohelpdesck/internal/platform/database"
	"os"
	"time"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: migrate up|down|status")
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	p, e := database.Open(ctx, os.Getenv("DATABASE_URL"), 2)
	if e != nil {
		fmt.Fprintln(os.Stderr, "database unavailable")
		os.Exit(1)
	}
	defer p.Close()
	v, e := p.Migrate(ctx, os.Args[1])
	if e != nil {
		fmt.Fprintln(os.Stderr, "migration failed")
		os.Exit(1)
	}
	fmt.Printf("migration version: %d\n", v)
}
