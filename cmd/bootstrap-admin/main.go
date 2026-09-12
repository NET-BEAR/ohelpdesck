package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/NET-BEAR/ohelpdesck/internal/auth"
	"github.com/NET-BEAR/ohelpdesck/internal/platform/database"
)

func main() {
	password := os.Getenv("INITIAL_ADMIN_PASSWORD")
	if password == "" {
		fmt.Fprintln(os.Stderr, "initial administrator password is required")
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, os.Getenv("DATABASE_URL"), 2)
	if err != nil {
		fmt.Fprintln(os.Stderr, "database unavailable")
		os.Exit(1)
	}
	defer pool.Close()
	if _, err := pool.Migrate(ctx, "up"); err != nil {
		fmt.Fprintln(os.Stderr, "migration failed")
		os.Exit(1)
	}
	repository := auth.NewRepository(pool)
	if _, err := repository.ByLogin(ctx, "sysadmin"); err == nil {
		fmt.Println("initial administrator already exists")
		return
	} else if err != auth.ErrNotFound {
		fmt.Fprintln(os.Stderr, "administrator bootstrap failed")
		os.Exit(1)
	}
	email := os.Getenv("INITIAL_ADMIN_EMAIL")
	if email == "" {
		email = "sysadmin@local.invalid"
	}
	if _, err := repository.Create(ctx, auth.CreateUser{Login: "sysadmin", Email: email, Name: "System Administrator", Password: password, Role: auth.Administrator}); err != nil {
		fmt.Fprintln(os.Stderr, "administrator bootstrap failed")
		os.Exit(1)
	}
	fmt.Println("initial administrator created")
}
