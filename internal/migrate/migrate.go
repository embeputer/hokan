package migrate

import (
	"database/sql"
	"fmt"

	"github.com/hokan/hokan/migrations"
	"github.com/pressly/goose/v3"
)

func Up(db *sql.DB) error {
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("sqlite3"); err != nil {
		return fmt.Errorf("set dialect: %w", err)
	}
	if err := goose.Up(db, "."); err != nil {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}
