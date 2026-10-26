package db

import (
	"database/sql"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver for goose
	"github.com/pressly/goose/v3"

	"github.com/cleber/gobackup/migrations"
)

// Migrate applies all pending embedded migrations against connString.
func Migrate(connString string) error {
	sqlDB, err := sql.Open("pgx", connString)
	if err != nil {
		return fmt.Errorf("migrate open: %w", err)
	}
	defer sqlDB.Close()

	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("migrate dialect: %w", err)
	}
	if err := goose.Up(sqlDB, "."); err != nil {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}
