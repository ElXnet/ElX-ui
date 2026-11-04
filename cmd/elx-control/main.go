// Command elx-control manages the ElX Control database.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/mhsanaei/3x-ui/v3/internal/elxcontrol/journal"
)

func run(ctx context.Context, args []string, dsn string) error {
	if len(args) != 1 || args[0] != "migrate" {
		return errors.New("usage: elx-control migrate")
	}
	if dsn == "" {
		return errors.New("ELX_CONTROL_POSTGRES_DSN is required")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return errors.New("invalid Control database configuration")
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return errors.New("cannot connect to Control database")
	}
	return journal.Migrate(ctx, db)
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Getenv("ELX_CONTROL_POSTGRES_DSN")); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
