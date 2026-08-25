package store

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	pgx5migrate "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver used by newMigrate
)

// migrationsFS embeds every SQL file in migrations/ into the compiled
// binary at build time, the same reasoning as schema.sql's old go:embed:
// migrations ship as part of the binary itself, so there's no separate SQL
// file to mount or keep in sync across environments.
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// newMigrate builds a *migrate.Migrate reading its migration files from the
// embedded migrationsFS and applying them over databaseURL.
//
// golang-migrate's driver interfaces predate database/sql-native pgx — its
// pgx/v5 database driver takes a *sql.DB rather than working with a
// pgxpool.Pool directly, so this opens its own, separate database/sql
// connection via jackc/pgx/v5/stdlib (which registers pgx as a database/sql
// driver name) purely for running migrations. It's independent of Open's
// pgxpool, and closed once the migration run is done.
func newMigrate(databaseURL string) (*migrate.Migrate, error) {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open database/sql connection: %w", err)
	}

	driver, err := pgx5migrate.WithInstance(db, &pgx5migrate.Config{})
	if err != nil {
		return nil, fmt.Errorf("build migration database driver: %w", err)
	}

	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("load embedded migrations: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", src, "pgx5", driver)
	if err != nil {
		return nil, fmt.Errorf("build migrator: %w", err)
	}
	return m, nil
}

// MigrateUp applies every pending migration in internal/store/migrations,
// in order.
func MigrateUp(databaseURL string) error {
	m, err := newMigrate(databaseURL)
	if err != nil {
		return err
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}

// MigrateDown rolls back exactly one migration: the most recently applied
// one.
func MigrateDown(databaseURL string) error {
	m, err := newMigrate(databaseURL)
	if err != nil {
		return err
	}
	if err := m.Steps(-1); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("roll back migration: %w", err)
	}
	return nil
}
