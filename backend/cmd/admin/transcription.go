package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/renfrewcountyscanner/call-recorder/backend/internal/transcription"
)

const (
	maxTranscriptionConcurrency = 8
	transcriptionAdvisoryLock   = 81640002
)

func transcriptionCommand(pool *pgxpool.Pool, args []string) {
	if len(args) == 0 {
		fatal(errors.New("usage: transcription <run|queue|retry|history|diagnose>"))
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
	case "diagnose":
		diagnoseTranscription(pool)
	default:
		fatal(errors.New("unknown transcription command"))
	}
}

type transcriptionConfig struct {
	Enabled               bool
	ProcessingEnabled     bool
	Provider              string
	ProviderType          string
	Endpoint              string
	Model                 string
	SecretRef             string
	DefaultLanguage       *string
	MinDurationMS         int64
	MaxAudioDurationMS    int64
	MaxFileSize           int64
	Temperature           float64
	VADEnabled            bool
	PhrasePromptsEnabled  bool
	PhrasePrompt          string
	RequestTimeoutSeconds int
	Concurrency           int
	RetryLimit            int
	AllowedEndpointCIDRs  string
}

func loadTranscriptionConfig(ctx context.Context, pool *pgxpool.Pool) (transcriptionConfig, error) {
	var cfg transcriptionConfig
	var lang *string
	err := pool.QueryRow(ctx, `SELECT enabled,processing_enabled,provider,provider_type,coalesce(endpoint_url,''),coalesce(model,''),coalesce(secret_ref,''),default_language,min_duration_ms,max_audio_duration_ms,max_file_size,temperature,vad_enabled,phrase_prompts_enabled,coalesce(phrase_prompt,''),request_timeout_seconds,concurrency,retry_limit,allowed_endpoint_cidrs FROM transcription_config WHERE id=true`).Scan(
		&cfg.Enabled, &cfg.ProcessingEnabled, &cfg.Provider, &cfg.ProviderType, &cfg.Endpoint, &cfg.Model, &cfg.SecretRef, &lang,
		&cfg.MinDurationMS, &cfg.MaxAudioDurationMS, &cfg.MaxFileSize, &cfg.Temperature, &cfg.VADEnabled, &cfg.PhrasePromptsEnabled, &cfg.PhrasePrompt,
		&cfg.RequestTimeoutSeconds, &cfg.Concurrency, &cfg.RetryLimit, &cfg.AllowedEndpointCIDRs)
	if err != nil {
		return cfg, err
	}
	cfg.DefaultLanguage = lang
	if cfg.Concurrency < 1 {
		cfg.Concurrency = 1
	}
	if cfg.Concurrency > maxTranscriptionConcurrency {
		cfg.Concurrency = maxTranscriptionConcurrency
	}
	if cfg.RetryLimit < 0 {
		cfg.RetryLimit = 0
	}
	if cfg.RequestTimeoutSeconds < 1 {
		cfg.RequestTimeoutSeconds = 60
	}
	return cfg, nil
}

func loadTranscriptionAPIKey(ctx context.Context, pool *pgxpool.Pool, secretRef string) (string, error) {
	var encryptedKey, keyNonce []byte
	_ = pool.QueryRow(ctx, `SELECT ciphertext,nonce FROM application_secrets WHERE purpose='transcription_api_key'`).Scan(&encryptedKey, &keyNonce)
	if len(encryptedKey) > 0 {
		key, err := adminMasterKey()
		if err != nil {
			return "", errors.New("encrypted transcription key is unavailable: master key missing")
		}
		plain, err := adminDecryptSecret(key, encryptedKey, keyNonce)
		if err != nil {
			return "", errors.New("encrypted transcription key cannot be decrypted")
		}
		return string(plain), nil
	}
	if secretRef != "" {
		v := os.Getenv(secretRef)
		if v == "" {
			return "", fmt.Errorf("configured transcription secret reference %q is unavailable", secretRef)
		}
		return v, nil
	}
	return "", nil
}

func recordHeartbeat(ctx context.Context, pool *pgxpool.Pool) {
	_, _ = pool.Exec(ctx, `INSERT INTO transcription_worker_heartbeat(id,worker_id,heartbeat_at,updated_at) VALUES(true,'transcription-worker',now(),now()) ON CONFLICT(id) DO UPDATE SET heartbeat_at=now(),updated_at=now()`)
}

func runTranscription(pool *pgxpool.Pool) {
	ctx := context.Background()
	cfg, err := loadTranscriptionConfig(ctx, pool)
	if err != nil {
		fatal(err)
	}
	recordHeartbeat(ctx, pool)
	if !cfg.Enabled || !cfg.ProcessingEnabled {
		fmt.Println("transcription disabled or processing disabled; worker idle")
		return
	}
	if cfg.Endpoint == "" || cfg.Model == "" {
		fatal(errors.New("transcription endpoint and model are required"))
	}
	if u, e := url.Parse(cfg.Endpoint); e != nil || (u.Scheme != "https" && u.Scheme != "http") {
		fatal(errors.New("transcription endpoint must use HTTP(S)"))
	}
	apiKey, err := loadTranscriptionAPIKey(ctx, pool, cfg.SecretRef)
	if err != nil {
		fatal(err)
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		fatal(err)
	}
	defer conn.Release()
	var locked bool
	if err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, transcriptionAdvisoryLock).Scan(&locked); err != nil || !locked {
		fatal(errors.New("another transcription worker is active"))
	}
	defer conn.Exec(ctx, `SELECT pg_advisory_unlock($1)`, transcriptionAdvisoryLock)

	var wg sync.WaitGroup
	jobs := make(chan transcriptionJob, cfg.Concurrency*2)
	var workersErr error
	var workersMu sync.Mutex

	for i := 0; i < cfg.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := transcriptionWorker(ctx, pool, cfg, apiKey, jobs); err != nil {
				workersMu.Lock()
				if workersErr == nil {
					workersErr = err
				}
				workersMu.Unlock()
			}
		}()
	}

	// Feed jobs until no more eligible jobs exist.
	for {
		job, ok, err := claimNextJob(ctx, pool, cfg)
		if err != nil {
			close(jobs)
			wg.Wait()
			fatal(err)
		}
		if !ok {
			break
		}
		jobs <- job
	}
	close(jobs)
	wg.Wait()
	if workersErr != nil {
		fatal(workersErr)
	}
	fmt.Printf("provider=%s worker-run-complete\n", cfg.Provider)
}

type transcriptionJob struct {
	id          int64
	callID      string
	audioPath   string
	audioFormat string
	durationMS  int64
	attemptCount int64
}

func checkDurationLimits(durationMS, minMS, maxMS int64) error {
	if minMS > 0 && durationMS < minMS {
		return fmt.Errorf("call duration %.2fs is below the minimum %.2fs", float64(durationMS)/1000, float64(minMS)/1000)
	}
	if maxMS > 0 && durationMS > maxMS {
		return fmt.Errorf("call duration %.2fs exceeds the maximum %.2fs", float64(durationMS)/1000, float64(maxMS)/1000)
	}
	return nil
}

func claimNextJob(ctx context.Context, pool *pgxpool.Pool, cfg transcriptionConfig) (transcriptionJob, bool, error) {
	var job transcriptionJob
	err := pool.QueryRow(ctx, `
		UPDATE transcription_jobs j SET status='running',attempt_count=attempt_count+1,started_at=now(),updated_at=now()
		FROM calls c
		WHERE j.id = (
			SELECT j2.id FROM transcription_jobs j2 JOIN calls c2 ON c2.id=j2.call_id
			WHERE j2.status IN ('pending','failed') AND j2.next_attempt_at<=now()
			ORDER BY j2.id FOR UPDATE SKIP LOCKED LIMIT 1
		)
		AND j.call_id = c.id
		RETURNING j.id, j.call_id, c.audio_path, c.audio_format, c.duration_ms, j.attempt_count`,
	).Scan(&job.id, &job.callID, &job.audioPath, &job.audioFormat, &job.durationMS, &job.attemptCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return job, false, nil
	}
	if err != nil {
		return job, false, err
	}
	return job, true, nil
}

func transcriptionWorker(ctx context.Context, pool *pgxpool.Pool, cfg transcriptionConfig, apiKey string, jobs <-chan transcriptionJob) error {
	client, err := transcription.HTTPClient(cfg.Endpoint, cfg.AllowedEndpointCIDRs)
	if err != nil {
		return err
	}
	client.Timeout = time.Duration(cfg.RequestTimeoutSeconds) * time.Second

	for job := range jobs {
		text, err := transcribeFile(client, cfg, apiKey, filepath.Join(os.Getenv("CALL_RECORDER_AUDIO_ROOT"), job.audioPath), job.durationMS)
		if err != nil {
			safe := sanitizeTranscriptionError(err)
			backoffSec := int64(math.Min(math.Pow(2, float64(job.attemptCount)), 3600))
			_, _ = pool.Exec(ctx, `
				UPDATE transcription_jobs
				SET status=CASE WHEN attempt_count>$1 THEN 'failed' ELSE 'pending' END,
				    error=$2,
				    next_attempt_at=now()+($3 * interval '1 second'),
				    updated_at=now()
				WHERE id=$4`, cfg.RetryLimit, safe, backoffSec, job.id)
			continue
		}
		language := ""
		if cfg.DefaultLanguage != nil {
			language = *cfg.DefaultLanguage
		}
		_, err = pool.Exec(ctx, `INSERT INTO transcripts(call_id,provider,language,text,original_text) VALUES($1,$2,NULLIF($3,''),$4,$4) ON CONFLICT(call_id,provider) DO UPDATE SET text=EXCLUDED.text,updated_at=now()`, job.callID, cfg.Provider, language, text)
		if err == nil {
			_, err = pool.Exec(ctx, `UPDATE calls SET search_document=to_tsvector('simple',coalesce(search_document::text,'')||' '||$2) WHERE id=$1`, job.callID, text)
		}
		if err == nil {
			_, _ = pool.Exec(ctx, `UPDATE transcription_jobs SET status='completed',completed_at=now(),error=NULL,updated_at=now() WHERE id=$1`, job.id)
		} else {
			_, _ = pool.Exec(ctx, `UPDATE transcription_jobs SET status='failed',error=$2,updated_at=now() WHERE id=$1`, job.id, sanitizeTranscriptionError(err))
		}
	}
	return nil
}

func transcribeFile(client *http.Client, cfg transcriptionConfig, key, path string, durationMS int64) (string, error) {
	st, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if st.Size() > cfg.MaxFileSize {
		return "", fmt.Errorf("audio file size %d exceeds transcription limit %d", st.Size(), cfg.MaxFileSize)
	}
	if err := checkDurationLimits(durationMS, cfg.MinDurationMS, cfg.MaxAudioDurationMS); err != nil {
		return "", err
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
	if _, err = io.Copy(part, f); err != nil {
		f.Close()
		return "", err
	}
	f.Close()

	_ = mw.WriteField("model", cfg.Model)
	if cfg.DefaultLanguage != nil && *cfg.DefaultLanguage != "" {
		_ = mw.WriteField("language", *cfg.DefaultLanguage)
	}
	if cfg.Temperature > 0 {
		_ = mw.WriteField("temperature", strconv.FormatFloat(cfg.Temperature, 'f', -1, 64))
	}
	if cfg.PhrasePromptsEnabled && cfg.PhrasePrompt != "" {
		_ = mw.WriteField("prompt", cfg.PhrasePrompt)
	}
	if cfg.ProviderType == "faster-whisper" && cfg.VADEnabled {
		_ = mw.WriteField("vad_filter", "true")
	}
	if err = mw.Close(); err != nil {
		return "", err
	}

	req, err := http.NewRequest(http.MethodPost, cfg.Endpoint, &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := client.Do(req)
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
	if json.Unmarshal(raw, &out) != nil {
		return "", errors.New("provider response is not valid JSON with a text field")
	}
	return strings.TrimSpace(out.Text), nil
}

func sanitizeTranscriptionError(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	// Never propagate raw provider responses that could contain secrets.
	for _, secret := range []string{os.Getenv("CALL_RECORDER_LEGACY_API_KEY")} {
		if secret != "" {
			s = strings.ReplaceAll(s, secret, "[redacted]")
		}
	}
	if len(s) > 500 {
		s = s[:500]
	}
	return s
}


func diagnoseTranscription(pool *pgxpool.Pool) {
	ctx := context.Background()
	cfg, err := loadTranscriptionConfig(ctx, pool)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	status := func(label string, ok bool, detail string) {
		icon := "✓"
		if !ok {
			icon = "✗"
		}
		fmt.Printf("%s %-30s %s\n", icon, label+":", detail)
	}

	if err := pool.Ping(ctx); err != nil {
		status("Database connectivity", false, err.Error())
		os.Exit(1)
	}
	status("Database connectivity", true, "ok")

	status("Processing enabled", cfg.ProcessingEnabled, strconv.FormatBool(cfg.ProcessingEnabled))
	status("Provider enabled", cfg.Enabled, strconv.FormatBool(cfg.Enabled))

	key, keyErr := loadTranscriptionAPIKey(ctx, pool, cfg.SecretRef)
	status("API key available", keyErr == nil && (key != "" || cfg.SecretRef == ""), func() string {
		if keyErr != nil {
			return keyErr.Error()
		}
		if key != "" {
			return "configured"
		}
		return "not configured"
	}())

	endpointAllowed := false
	if cfg.Endpoint != "" {
		if _, e := transcription.HTTPClient(cfg.Endpoint, cfg.AllowedEndpointCIDRs); e == nil {
			endpointAllowed = true
		}
	}
	status("Endpoint allowed", endpointAllowed, cfg.Endpoint)

	audioRoot := os.Getenv("CALL_RECORDER_AUDIO_ROOT")
	if audioRoot == "" {
		audioRoot = "/var/lib/call-recorder/audio"
	}
	if _, e := os.Stat(audioRoot); e == nil {
		status("Audio root readable", true, audioRoot)
	} else {
		status("Audio root readable", false, e.Error())
	}

	var heartbeat *time.Time
	_ = pool.QueryRow(ctx, `SELECT heartbeat_at FROM transcription_worker_heartbeat WHERE id=true`).Scan(&heartbeat)
	if heartbeat != nil && time.Since(*heartbeat) < 2*time.Minute {
		status("Worker heartbeat", true, heartbeat.UTC().Format(time.RFC3339))
	} else if heartbeat != nil {
		status("Worker heartbeat", false, "stale: "+heartbeat.UTC().Format(time.RFC3339))
	} else {
		status("Worker heartbeat", false, "never")
	}

	var pending, running, failed int64
	_ = pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE status='pending'),count(*) FILTER(WHERE status='running'),count(*) FILTER(WHERE status='failed') FROM transcription_jobs`).Scan(&pending, &running, &failed)
	status("Queue counts", true, fmt.Sprintf("pending=%d running=%d failed=%d", pending, running, failed))

	var oldest *time.Time
	var lastErr string
	_ = pool.QueryRow(ctx, `SELECT min(next_attempt_at),coalesce((SELECT error FROM transcription_jobs WHERE status='failed' ORDER BY updated_at DESC LIMIT 1),'') FROM transcription_jobs WHERE status='pending'`).Scan(&oldest, &lastErr)
	if oldest != nil {
		status("Oldest pending job", true, oldest.UTC().Format(time.RFC3339))
	} else {
		status("Oldest pending job", true, "none")
	}
	if lastErr != "" {
		status("Last error", false, lastErr)
	} else {
		status("Last error", true, "none")
	}
}

var _ = filepath.Clean
var _ = time.Second
var _ = math.MaxFloat64
var _ = binary.MaxVarintLen64
var _ = pgxpool.Config{}
