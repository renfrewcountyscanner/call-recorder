package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/renfrewcountyscanner/call-recorder/backend/migrations"
)

const migrationLockID int64 = 81640002
const migrationReleaseVersion = "v1.0.0"

func runMigrations(pool *pgxpool.Pool) error {
	ctx := context.Background()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	if _, err = conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationLockID); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	defer conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, migrationLockID)

	if _, err = conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		filename text PRIMARY KEY,
		checksum_sha256 text NOT NULL,
		release_version text NOT NULL,
		applied_at timestamptz NOT NULL DEFAULT now()
	)`); err != nil {
		return fmt.Errorf("create migration ledger: %w", err)
	}

	entries, err := fs.ReadDir(migrations.Files, ".")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return errors.New("no embedded migrations found")
	}

	for _, name := range names {
		body, readErr := migrations.Files.ReadFile(name)
		if readErr != nil {
			return readErr
		}
		digest := sha256.Sum256(body)
		checksum := hex.EncodeToString(digest[:])
		var recorded string
		err = conn.QueryRow(ctx, `SELECT checksum_sha256 FROM schema_migrations WHERE filename=$1`, name).Scan(&recorded)
		if err == nil {
			if recorded != checksum {
				return fmt.Errorf("migration %s checksum changed: database=%s binary=%s", name, recorded, checksum)
			}
			fmt.Printf("migration=%s status=already_applied\n", name)
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("read migration ledger for %s: %w", name, err)
		}

		tx, beginErr := conn.Begin(ctx)
		if beginErr != nil {
			return beginErr
		}
		if _, err = tx.Exec(ctx, string(body)); err == nil {
			_, err = tx.Exec(ctx, `INSERT INTO schema_migrations(filename,checksum_sha256,release_version) VALUES($1,$2,$3)`, name, checksum, migrationReleaseVersion)
		}
		if err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		if err = tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit migration %s: %w", name, err)
		}
		fmt.Printf("migration=%s status=applied checksum=%s\n", name, checksum)
	}
	return nil
}
