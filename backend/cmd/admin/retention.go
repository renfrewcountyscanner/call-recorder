package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func retention(pool *pgxpool.Pool, args []string) {
	if len(args) == 0 {
		retentionUsage()
	}
	switch args[0] {
	case "list":
		rows, err := pool.Query(context.Background(), `SELECT id,name,enabled,dry_run,retention_days,priority FROM retention_policies ORDER BY priority DESC,id`)
		if err != nil {
			fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var id, days, priority int
			var name string
			var enabled, dry bool
			if err := rows.Scan(&id, &name, &enabled, &dry, &days, &priority); err != nil {
				fatal(err)
			}
			fmt.Printf("%d\t%s\tenabled=%t\tdry_run=%t\tdays=%d\tpriority=%d\n", id, name, enabled, dry, days, priority)
		}
	case "history":
		rows, err := pool.Query(context.Background(), `SELECT id,coalesce(policy_id,0),dry_run,calls_matched,calls_deleted,audio_files_deleted,failures FROM retention_runs ORDER BY id DESC LIMIT 50`)
		if err != nil {
			fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var id, pid, matched, deleted, audio, failures int
			var dry bool
			if err := rows.Scan(&id, &pid, &dry, &matched, &deleted, &audio, &failures); err != nil {
				fatal(err)
			}
			fmt.Printf("%d\tpolicy=%d\tdry_run=%t\tmatched=%d\tdeleted=%d\taudio=%d\tfailures=%d\n", id, pid, dry, matched, deleted, audio, failures)
		}
	case "run":
		flags := flag.NewFlagSet("retention run", flag.ExitOnError)
		policy := flags.Int("policy", 0, "policy ID")
		dry := flags.Bool("dry-run", false, "force dry-run")
		_ = flags.Parse(args[1:])
		runRetention(pool, *policy, *dry)
	default:
		retentionUsage()
	}
}

func runRetention(pool *pgxpool.Pool, policyID int, forceDry bool) {
	ctx := context.Background()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		fatal(err)
	}
	defer conn.Release()
	var locked bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(84723901)`).Scan(&locked); err != nil || !locked {
		fatal(fmt.Errorf("another retention run is active"))
	}
	defer func() { _, _ = conn.Exec(ctx, `SELECT pg_advisory_unlock(84723901)`) }()
	where := "enabled"
	args := []any{}
	if policyID > 0 {
		where += " AND id=$1"
		args = append(args, policyID)
	}
	rows, err := conn.Query(ctx, `SELECT id,dry_run,retention_days,sender_filter,system_filter,talkgroup_filter,call_type_filter,min_duration_ms,max_duration_ms FROM retention_policies WHERE `+where+` ORDER BY priority DESC,id`, args...)
	if err != nil {
		fatal(err)
	}
	type policy struct {
		id, days                  int
		dry                       bool
		sender, system, tg, ctype *string
		min, max                  *int64
	}
	policies := []policy{}
	for rows.Next() {
		var p policy
		if err := rows.Scan(&p.id, &p.dry, &p.days, &p.sender, &p.system, &p.tg, &p.ctype, &p.min, &p.max); err != nil {
			rows.Close()
			fatal(err)
		}
		policies = append(policies, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		fatal(err)
	}
	for _, p := range policies {
		id, days, dry := p.id, p.days, p.dry
		sender, system, tg, ctype, min, max := p.sender, p.system, p.tg, p.ctype, p.min, p.max
		started := time.Now().UTC()
		effectiveDry := dry || forceDry
		query := `SELECT count(*),coalesce(sum(audio_size),0),coalesce(sum(duration_ms),0) FROM calls WHERE start_time < now() - ($1::int * interval '1 day') AND (NOT protected OR protection_expires_at IS NOT NULL AND protection_expires_at <= now()) AND NOT EXISTS (SELECT 1 FROM dataset_export_items dei JOIN dataset_exports de ON de.id=dei.export_id WHERE dei.call_id=calls.id AND de.status IN ('pending','running'))`
		qargs := []any{days}
		for _, f := range []struct {
			v   *string
			col string
		}{{sender, "sender_id"}, {system, "system_id"}, {tg, "talkgroup_id"}, {ctype, "call_type"}} {
			if f.v != nil {
				qargs = append(qargs, *f.v)
				query += fmt.Sprintf(" AND %s=$%d", f.col, len(qargs))
			}
		}
		if min != nil {
			qargs = append(qargs, *min)
			query += fmt.Sprintf(" AND duration_ms >= $%d", len(qargs))
		}
		if max != nil {
			qargs = append(qargs, *max)
			query += fmt.Sprintf(" AND duration_ms <= $%d", len(qargs))
		}
		var matched int
		var matchedBytes, matchedDuration int64
		if err := conn.QueryRow(ctx, query, qargs...).Scan(&matched, &matchedBytes, &matchedDuration); err != nil {
			fatal(err)
		}
		if effectiveDry {
			_, err = conn.Exec(ctx, `INSERT INTO retention_runs(policy_id,started_at,ended_at,dry_run,calls_matched,audio_bytes_matched,audio_duration_ms_matched,summary) VALUES($1,$2,now(),true,$3,$4,$5,$6)`, id, started, matched, matchedBytes, matchedDuration, `{"mode":"dry-run"}`)
			if err != nil {
				fatal(err)
			}
			fmt.Printf("policy=%d dry_run=true matched=%d deleted=0\n", id, matched)
			continue
		}
		audioRoot := os.Getenv("CALL_RECORDER_AUDIO_ROOT")
		if audioRoot == "" {
			fatal(fmt.Errorf("CALL_RECORDER_AUDIO_ROOT is required for destructive retention"))
		}
		candidatesQuery := strings.Replace(query, "SELECT count(*),coalesce(sum(audio_size),0),coalesce(sum(duration_ms),0)", "SELECT id,audio_path", 1)
		candidateRows, err := conn.Query(ctx, candidatesQuery, qargs...)
		if err != nil {
			fatal(err)
		}
		type candidate struct{ id, path string }
		candidates := []candidate{}
		for candidateRows.Next() {
			var c candidate
			if err := candidateRows.Scan(&c.id, &c.path); err != nil {
				candidateRows.Close()
				fatal(err)
			}
			candidates = append(candidates, c)
		}
		candidateRows.Close()
		trash := filepath.Join(audioRoot, ".retention-trash", time.Now().UTC().Format("20060102T150405.000000000"))
		if err := os.MkdirAll(trash, 0700); err != nil {
			fatal(err)
		}
		moved := []candidate{}
		missingFiles := 0
		for _, c := range candidates {
			src := filepath.Join(audioRoot, c.path)
			if !strings.HasPrefix(filepath.Clean(src), filepath.Clean(audioRoot)+string(os.PathSeparator)) {
				fatal(fmt.Errorf("unsafe audio path"))
			}
			dst := filepath.Join(trash, c.id+filepath.Ext(c.path))
			if err := os.Rename(src, dst); err != nil {
				if os.IsNotExist(err) {
					missingFiles++
					moved = append(moved, candidate{id: c.id})
					continue
				}
				for _, m := range moved {
					if m.path == "" {
						continue
					}
					_ = os.MkdirAll(filepath.Dir(filepath.Join(audioRoot, m.path)), 0750)
					_ = os.Rename(filepath.Join(trash, m.id+filepath.Ext(m.path)), filepath.Join(audioRoot, m.path))
				}
				fatal(err)
			}
			moved = append(moved, c)
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			fatal(err)
		}
		ids := make([]string, 0, len(candidates))
		for _, candidate := range candidates {
			ids = append(ids, candidate.id)
		}
		deletedRows, deleteErr := tx.Query(ctx, `DELETE FROM calls WHERE id=ANY($1) AND (NOT protected OR protection_expires_at IS NOT NULL AND protection_expires_at<=now()) AND NOT EXISTS (SELECT 1 FROM dataset_export_items dei JOIN dataset_exports de ON de.id=dei.export_id WHERE dei.call_id=calls.id AND de.status IN ('pending','running')) RETURNING id`, ids)
		deleted := map[string]bool{}
		if deleteErr == nil {
			for deletedRows.Next() {
				var deletedID string
				if deletedRows.Scan(&deletedID) == nil {
					deleted[deletedID] = true
				}
			}
			deleteErr = deletedRows.Err()
			deletedRows.Close()
		}
		err = deleteErr
		if err == nil {
			err = tx.Commit(ctx)
		} else {
			_ = tx.Rollback(ctx)
		}
		if err != nil {
			for _, m := range moved {
				if m.path == "" {
					continue
				}
				_ = os.MkdirAll(filepath.Dir(filepath.Join(audioRoot, m.path)), 0750)
				_ = os.Rename(filepath.Join(trash, m.id+filepath.Ext(m.path)), filepath.Join(audioRoot, m.path))
			}
			fatal(err)
		}
		failures := 0
		for _, m := range moved {
			if !deleted[m.id] && m.path != "" {
				_ = os.MkdirAll(filepath.Dir(filepath.Join(audioRoot, m.path)), 0750)
				_ = os.Rename(filepath.Join(trash, m.id+filepath.Ext(m.path)), filepath.Join(audioRoot, m.path))
				continue
			}
			if m.path != "" {
				if err := os.Remove(filepath.Join(trash, m.id+filepath.Ext(m.path))); err != nil {
					failures++
				}
			}
		}
		failures += missingFiles
		_ = os.Remove(trash)
		_, err = conn.Exec(ctx, `INSERT INTO retention_runs(policy_id,started_at,ended_at,dry_run,calls_matched,calls_deleted,audio_files_deleted,failures,audio_bytes_matched,audio_duration_ms_matched,summary) VALUES($1,$2,now(),false,$3,$4,$5,$6,$7,$8,$9)`, id, started, matched, len(deleted), len(deleted)-missingFiles, failures, matchedBytes, matchedDuration, `{"mode":"delete"}`)
		if err != nil {
			fatal(err)
		}
		fmt.Printf("policy=%d dry_run=false matched=%d deleted=%d audio=%d failures=%d\n", id, matched, len(deleted), len(deleted)-missingFiles, failures)
	}
}
func retentionUsage() {
	fmt.Fprintln(os.Stderr, "usage: call-recorder-admin retention <list|run|history>")
	os.Exit(2)
}
