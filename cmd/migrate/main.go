// Command migrate applies pending database migrations. It connects with
// the privileged (owner/superuser) role - see internal/platform/db.Migrate.
package main

import (
	"log"
	"os"

	"github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

func main() {
	dsn := os.Getenv("ZONARYOS_MIGRATE_DATABASE_URL")
	if dsn == "" {
		log.Fatal("ZONARYOS_MIGRATE_DATABASE_URL must be set")
	}

	if err := db.Migrate(dsn); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	log.Println("migrations applied")
}
