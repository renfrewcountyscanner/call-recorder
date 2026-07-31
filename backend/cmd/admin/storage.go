package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

func storage(pool *pgxpool.Pool, args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: call-recorder-admin storage missing-audio|orphan-audio|audit")
		os.Exit(2)
	}
	switch args[0] {
	case "missing-audio":
		storageMissingAudio(pool)
	case "orphan-audio":
		storageOrphanAudio(pool)
	case "audit":
		storageAudit(pool)
	default:
		fmt.Fprintln(os.Stderr, "usage: call-recorder-admin storage missing-audio|orphan-audio|audit")
		os.Exit(2)
	}
}

func storageMissingAudio(pool *pgxpool.Pool) {
	audioRoot := os.Getenv("CALL_RECORDER_AUDIO_ROOT")
	if audioRoot == "" {
		fatal(fmt.Errorf("CALL_RECORDER_AUDIO_ROOT is required"))
	}
	ctx := context.Background()
	rows, err := pool.Query(ctx, `SELECT id, audio_path, audio_size FROM calls WHERE audio_path IS NOT NULL AND audio_path != '' ORDER BY start_time DESC`)
	if err != nil {
		fatal(err)
	}
	defer rows.Close()
	var missing int
	var total int64
	for rows.Next() {
		var id, path string
		var size int64
		if err := rows.Scan(&id, &path, &size); err != nil {
			continue
		}
		total++
		full := filepath.Join(audioRoot, path)
		if _, err := os.Stat(full); os.IsNotExist(err) {
			missing++
			fmt.Printf("missing call=%s path=%s size=%d\n", id, path, size)
		}
	}
	fmt.Printf("total=%d missing=%d\n", total, missing)
	if missing > 0 {
		os.Exit(1)
	}
}

func storageOrphanAudio(pool *pgxpool.Pool) {
	audioRoot := os.Getenv("CALL_RECORDER_AUDIO_ROOT")
	if audioRoot == "" {
		fatal(fmt.Errorf("CALL_RECORDER_AUDIO_ROOT is required"))
	}
	ctx := context.Background()
	// Build set of DB audio paths.
	dbPaths := make(map[string]bool)
	rows, err := pool.Query(ctx, `SELECT audio_path FROM calls WHERE audio_path IS NOT NULL AND audio_path != ''`)
	if err != nil {
		fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err == nil {
			dbPaths[p] = true
		}
	}
	// Walk audio directory.
	var orphans int
	var totalSize int64
	err = filepath.Walk(audioRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".mp3" && ext != ".wav" {
			return nil
		}
		rel, err := filepath.Rel(audioRoot, path)
		if err != nil {
			return nil
		}
		totalSize += info.Size()
		if !dbPaths[rel] {
			orphans++
			fmt.Printf("orphan path=%s size=%d\n", rel, info.Size())
		}
		return nil
	})
	if err != nil {
		fatal(err)
	}
	fmt.Printf("disk_files_total disk_bytes=%d orphans=%d\n", totalSize, orphans)
	if orphans > 0 {
		os.Exit(1)
	}
}

func storageAudit(pool *pgxpool.Pool) {
	audioRoot := os.Getenv("CALL_RECORDER_AUDIO_ROOT")
	if audioRoot == "" {
		fatal(fmt.Errorf("CALL_RECORDER_AUDIO_ROOT is required"))
	}
	ctx := context.Background()

	// Count DB calls and total audio size.
	var dbCalls int
	var dbAudioBytes int64
	err := pool.QueryRow(ctx, `SELECT count(*), coalesce(sum(audio_size),0) FROM calls WHERE audio_path IS NOT NULL AND audio_path != ''`).Scan(&dbCalls, &dbAudioBytes)
	if err != nil {
		fatal(err)
	}

	// Build set of DB audio paths.
	dbPaths := make(map[string]bool)
	rows, err := pool.Query(ctx, `SELECT audio_path FROM calls WHERE audio_path IS NOT NULL AND audio_path != ''`)
	if err != nil {
		fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err == nil {
			dbPaths[p] = true
		}
	}

	// Walk audio directory.
	var diskFiles int
	var diskBytes int64
	var missing int
	var orphans int
	err = filepath.Walk(audioRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".mp3" && ext != ".wav" {
			return nil
		}
		diskFiles++
		diskBytes += info.Size()
		rel, err := filepath.Rel(audioRoot, path)
		if err != nil {
			return nil
		}
		if !dbPaths[rel] {
			orphans++
			fmt.Printf("orphan path=%s size=%d\n", rel, info.Size())
		}
		return nil
	})
	if err != nil {
		fatal(err)
	}

	// Check missing files.
	for p := range dbPaths {
		full := filepath.Join(audioRoot, p)
		if _, err := os.Stat(full); os.IsNotExist(err) {
			missing++
			fmt.Printf("missing path=%s\n", p)
		}
	}

	fmt.Printf("db_calls=%d db_audio_bytes=%d disk_files=%d disk_bytes=%d missing=%d orphans=%d\n", dbCalls, dbAudioBytes, diskFiles, diskBytes, missing, orphans)
	if missing > 0 || orphans > 0 {
		os.Exit(1)
	}
}
