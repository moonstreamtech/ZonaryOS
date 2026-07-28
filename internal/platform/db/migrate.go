package db

import (
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/moonstreamtech/ZonaryOS/migrations"
)

// Migrate applies all pending migrations against dsn. dsn must reference a
// role with sufficient privileges to create tables, roles, and RLS
// policies (the docker-compose "postgres" superuser locally) - deliberately
// not the same role the application server connects as, since that role
// (zonaryos_app) must never bypass RLS. See WithFirmContext.
func Migrate(dsn string) error {
	source, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("load migration source: %w", err)
	}

	m, err := migrate.NewWithSourceInstance("iofs", source, dsn)
	if err != nil {
		return fmt.Errorf("init migrator: %w", err)
	}

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}
