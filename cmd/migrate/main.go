// Command migrate applies (or rolls back) internal/store's SQL migrations
// against DATABASE_URL. It's a thin wrapper: the migration logic lives in
// internal/store — this file just parses flags and wires it together.
package main

import (
	"flag"
	"fmt"
	"log"

	"github.com/MadonnaMat/go-rag-lab/internal/config"
	"github.com/MadonnaMat/go-rag-lab/internal/store"
)

func main() {
	down := flag.Bool("down", false, "roll back the most recently applied migration instead of applying pending ones")
	flag.Parse()

	if err := run(*down); err != nil {
		log.Fatal(err)
	}
}

func run(down bool) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if down {
		if err := store.MigrateDown(cfg.DatabaseURL); err != nil {
			return fmt.Errorf("migrate down: %w", err)
		}
		fmt.Println("Rolled back one migration.")
		return nil
	}

	if err := store.MigrateUp(cfg.DatabaseURL); err != nil {
		return fmt.Errorf("migrate up: %w", err)
	}
	fmt.Println("Migrations applied.")
	return nil
}
