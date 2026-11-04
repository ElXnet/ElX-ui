package journal

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

//go:embed 001_operations.sql
var schema string

func TestPostgresOperationLifecycle(t *testing.T) {
	dsn := os.Getenv("ELX_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("ELX_TEST_POSTGRES_DSN is required for PostgreSQL integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	db, err := sql.Open("pgx", dsn)
	checkError(t, err, nil)
	defer db.Close()
	db.SetMaxOpenConns(1)
	// Temporary tables keep the test isolated from any permanent schema.
	migration := strings.ReplaceAll(schema, "CREATE TABLE", "CREATE TEMP TABLE")
	for _, stmt := range strings.Split(migration, ";") {
		if strings.TrimSpace(stmt) != "" {
			_, err := db.ExecContext(ctx, stmt)
			checkError(t, err, nil)
		}
	}

	target := Target{"00000000-0000-0000-0000-000000000001", "00000000-0000-0000-0000-000000000002"}
	cmd := Command{ID: "00000000-0000-0000-0000-000000000003", Payload: []byte("policy")}
	other := Command{ID: "00000000-0000-0000-0000-000000000004", Payload: []byte("next policy")}

	for range 2 {
		rev, err := Enqueue(ctx, db, target, cmd)
		checkError(t, err, nil)
		if rev != 1 {
			t.Fatalf("revision = %d, want 1", rev)
		}
	}
	checkPending(t, db, target, cmd.ID)
	_, err = Enqueue(ctx, db, target, other)
	checkError(t, err, ErrPending)
	conflict := cmd
	conflict.Payload = []byte("different")
	_, err = Enqueue(ctx, db, target, conflict)
	checkError(t, err, ErrConflict)

	hash := sha256.Sum256(cmd.Payload)
	wrongNode := target
	wrongNode.NodeID = other.ID
	checkError(t, ConfirmApplied(ctx, db, wrongNode, cmd.ID, 1, hash), ErrReceipt)
	checkError(t, ConfirmApplied(ctx, db, target, cmd.ID, 2, hash), ErrReceipt)
	checkError(t, ConfirmApplied(ctx, db, target, cmd.ID, 1, [32]byte{}), ErrReceipt)
	checkPending(t, db, target, cmd.ID)

	for range 2 {
		checkError(t, ConfirmApplied(ctx, db, target, cmd.ID, 1, hash), nil)
	}
	_, err = Pending(ctx, db, target)
	checkError(t, err, sql.ErrNoRows)
	_, err = Enqueue(ctx, db, target, other)
	checkError(t, err, ErrRevision)
	other.ExpectedRevision = 1
	_, err = Enqueue(ctx, db, target, other)
	checkError(t, err, nil)
	checkError(t, ConfirmApplied(ctx, db, target, cmd.ID, 1, hash), nil)
	checkPending(t, db, target, other.ID)
}
