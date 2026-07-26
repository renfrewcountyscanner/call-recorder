package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func transcription(pool *pgxpool.Pool, args []string) {
	if len(args) == 0 {
		fatal(errors.New("usage: transcription <run|queue|retry|history>"))
	}
	switch args[0] {
	case "queue":
		f := flag.NewFlagSet("transcription queue", flag.ExitOnError)
		id := f.String("call", "", "call ID")
		_ = f.Parse(args[1:])
		if *id == "" {
			fatal(errors.New("--call is required"))
		}
		var provider string
		_ = pool.QueryRow(context.Background(), `SELECT provider FROM transcription_config WHERE id=true`).Scan(&provider)
		_, err := pool.Exec(context.Background(), `INSERT INTO transcription_jobs(call_id,provider) VALUES($1,$2) ON CONFLICT(call_id,provider) DO UPDATE SET status='pending',next_attempt_at=now(),error=NULL`, *id, provider)
		if err != nil {
			fatal(err)
		}
		fmt.Printf("call=%s queued\n", *id)
	case "retry":
		f := flag.NewFlagSet("transcription retry", flag.ExitOnError)
		id := f.Int64("job", 0, "job ID")
		_ = f.Parse(args[1:])
		if *id == 0 {
			fatal(errors.New("--job is required"))
		}
		_, err := pool.Exec(context.Background(), `UPDATE transcription_jobs SET status='pending',next_attempt_at=now(),error=NULL WHERE id=$1`, *id)
		if err != nil {
			fatal(err)
		}
	case "history":
		rows, err := pool.Query(context.Background(), `SELECT id,call_id,status,provider,attempt_count,coalesce(error,'') FROM transcription_jobs ORDER BY id DESC LIMIT 100`)
		if err != nil {
			fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var id, attempt int64
			var call, status, provider, errText string
			if e := rows.Scan(&id, &call, &status, &provider, &attempt, &errText); e != nil {
				fatal(e)
			}
			fmt.Printf("%d\tcall=%s\tstatus=%s\tprovider=%s\tattempts=%d\t%s\n", id, call, status, provider, attempt, errText)
		}
	case "run":
		runTranscription(pool)
	default:
		fatal(errors.New("unknown transcription command"))
	}
}

func runTranscription(pool *pgxpool.Pool) {
	ctx := context.Background()
	var enabled bool
	var provider, endpoint, model, secretRef string
	var maxSize, maxDur int64
	var language *string
	if err := pool.QueryRow(ctx, `SELECT enabled,provider,coalesce(endpoint_url,''),coalesce(model,''),coalesce(secret_ref,''),default_language,max_audio_duration_ms,max_file_size FROM transcription_config WHERE id=true`).Scan(&enabled, &provider, &endpoint, &model, &secretRef, &language, &maxDur, &maxSize); err != nil {
		fatal(err)
	}
	if !enabled {
		fmt.Println("transcription disabled")
		return
	}
	if endpoint == "" || model == "" {
		fatal(errors.New("transcription endpoint and model are required"))
	}
	if u, e := url.Parse(endpoint); e != nil || u.Scheme != "https" && u.Scheme != "http" {
		fatal(errors.New("transcription endpoint must use HTTP(S)"))
	}
	apiKey := os.Getenv(secretRef)
	if secretRef != "" && apiKey == "" {
		fatal(errors.New("configured transcription secret is unavailable"))
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		fatal(err)
	}
	defer conn.Release()
	var locked bool
	if err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(81640002)`).Scan(&locked); err != nil || !locked {
		fatal(errors.New("another transcription worker is active"))
	}
	defer conn.Exec(ctx, `SELECT pg_advisory_unlock(81640002)`)
	rows, err := conn.Query(ctx, `SELECT j.id,j.call_id,c.audio_path,c.audio_format FROM transcription_jobs j JOIN calls c ON c.id=j.call_id WHERE j.status IN ('pending','failed') AND j.next_attempt_at<=now() ORDER BY j.id LIMIT 20`)
	if err != nil {
		fatal(err)
	}
	defer rows.Close()
	type job struct {
		id                 int64
		call, path, format string
	}
	jobs := []job{}
	for rows.Next() {
		var j job
		if rows.Scan(&j.id, &j.call, &j.path, &j.format) == nil {
			jobs = append(jobs, j)
		}
	}
	for _, j := range jobs {
		_, _ = conn.Exec(ctx, `UPDATE transcription_jobs SET status='running',attempt_count=attempt_count+1,started_at=now(),updated_at=now() WHERE id=$1`, j.id)
		text, err := transcribeFile(endpoint, model, apiKey, language, filepath.Join(os.Getenv("CALL_RECORDER_AUDIO_ROOT"), j.path), maxSize, maxDur)
		if err != nil {
			safe := sanitizeError(err)
			_, _ = conn.Exec(ctx, `UPDATE transcription_jobs SET status=CASE WHEN attempt_count>=3 THEN 'failed' ELSE 'pending' END,error=$2,next_attempt_at=now()+least((2^least(attempt_count,8))::int,3600)*interval '1 second',updated_at=now() WHERE id=$1`, j.id, safe)
			continue
		}
		_, err = conn.Exec(ctx, `INSERT INTO transcripts(call_id,provider,language,text,original_text) VALUES($1,$2,$3,$4,$4) ON CONFLICT(call_id,provider) DO UPDATE SET text=EXCLUDED.text,updated_at=now()`, j.call, provider, language, text)
		if err == nil {
			_, err = conn.Exec(ctx, `UPDATE calls SET search_document=to_tsvector('simple',coalesce(search_document::text,'')||' '||$2) WHERE id=$1`, j.call, text)
		}
		if err == nil {
			_, _ = conn.Exec(ctx, `UPDATE transcription_jobs SET status='completed',completed_at=now(),error=NULL,updated_at=now() WHERE id=$1`, j.id)
		}
	}
	fmt.Printf("provider=%s processed=%d\n", provider, len(jobs))
}

func transcribeFile(endpoint, model, key string, language *string, path string, maxSize, maxDur int64) (string, error) {
	st, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if st.Size() > maxSize {
		return "", errors.New("audio exceeds transcription size limit")
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", filepath.Base(path))
	if err != nil {
		return "", err
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err = io.Copy(part, f); err != nil {
		return "", err
	}
	_ = maxDur
	_ = mw.WriteField("model", model)
	if language != nil {
		_ = mw.WriteField("language", *language)
	}
	if err = mw.Close(); err != nil {
		return "", err
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("provider returned HTTP %d", resp.StatusCode)
	}
	var out struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &out) != nil || strings.TrimSpace(out.Text) == "" {
		return "", errors.New("provider response missing text")
	}
	return strings.TrimSpace(out.Text), nil
}

var _ = filepath.Clean
var _ = os.Getenv
var _ = strings.TrimSpace
var _ = time.Second
