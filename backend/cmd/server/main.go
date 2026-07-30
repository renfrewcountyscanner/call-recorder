package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/argon2"
)

//go:embed web/templates/*.html
var templatesFS embed.FS

//go:embed web/static
var staticFS embed.FS

// version is reported by /healthz and shown in the interface header.
const version = "v0.4.1"

type config struct {
	ListenAddr                string
	DatabaseURL               string
	AudioRoot                 string
	MaxAudioBytes             int64
	PendingTTL                time.Duration
	StartToleranceMS          int64
	DurationTolMS             int64
	BootstrapSender           string
	BootstrapKey              string
	LegacyEnabled             bool
	LegacyDebug               bool
	LegacyAuthID              string
	LegacyAPIKey              string
	TestFailFinalize          bool
	AdminEnabled              bool
	AdminOpen                 bool
	AdminToken                string
	CloudflareAccessEnabled   bool
	CloudflareAdminEmail      string
	CloudflareTrustedProxyIPs []string
}

type server struct {
	cfg       config
	db        *pgxpool.Pool
	logger    *slog.Logger
	templates *template.Template
}

type callMetadata struct {
	SourceCallID  string          `json:"source_call_id"`
	StartTime     time.Time       `json:"start_time"`
	DurationMS    int64           `json:"duration_ms"`
	ReceiverID    string          `json:"receiver_id"`
	SystemID      string          `json:"system_id"`
	SystemName    string          `json:"system_name"`
	SiteID        string          `json:"site_id"`
	SiteName      string          `json:"site_name"`
	TalkgroupID   string          `json:"talkgroup_id"`
	TalkgroupName string          `json:"talkgroup_name"`
	TalkgroupTag  string          `json:"talkgroup_tag"`
	RadioID       string          `json:"radio_id"`
	RadioName     string          `json:"radio_name"`
	RadioTag      string          `json:"radio_tag"`
	Frequency     string          `json:"frequency"`
	LCN           string          `json:"lcn"`
	VoiceService  string          `json:"voice_service"`
	CallType      string          `json:"call_type"`
	GroupCall     *bool           `json:"group_call"`
	AudioOffsetMS *int64          `json:"audio_offset_ms"`
	Transcript    string          `json:"transcript"`
	Notes         string          `json:"notes"`
	Patches       []patchMetadata `json:"patches"`
}

type patchMetadata struct {
	TalkgroupID   string `json:"talkgroup_id"`
	TalkgroupName string `json:"talkgroup_name"`
}
type createUploadRequest struct {
	SenderID       string       `json:"sender_id"`
	IdempotencyKey string       `json:"idempotency_key"`
	AudioFormat    string       `json:"audio_format"`
	Call           callMetadata `json:"call"`
}
type createUploadResponse struct {
	UploadToken string    `json:"upload_token,omitempty"`
	ExpiresAt   time.Time `json:"expires_at,omitempty"`
	Duplicate   bool      `json:"duplicate"`
	CallID      string    `json:"call_id,omitempty"`
	Error       string    `json:"error,omitempty"`
}
type errorResponse struct {
	Error string `json:"error"`
}
type completedCall struct {
	ID, SenderID, ReceiverID, SystemID, SystemName, SiteID, SiteName, TalkgroupID, TalkgroupName, TalkgroupTag, RadioID, RadioName, RadioTag, Frequency, LCN, VoiceService, AudioPath, AudioFormat, Transcript, Notes, CallType string
	Protected                                                                                                                                                                                                                   bool
	ProtectionReason, ProtectedBy                                                                                                                                                                                               string
	ProtectedAt, ProtectionExpiresAt                                                                                                                                                                                            *time.Time
	GroupCall                                                                                                                                                                                                                   *bool
	StartTime                                                                                                                                                                                                                   time.Time
	DurationMS                                                                                                                                                                                                                  int64
	AudioSize                                                                                                                                                                                                                   int64
	Patches                                                                                                                                                                                                                     int
	GeneratedTranscript                                                                                                                                                                                                         bool
	TranscriptionStatus                                                                                                                                                                                                         string
}

func main() {
	cfg := loadConfig()
	if cfg.DatabaseURL == "" {
		slog.Error("CALL_RECORDER_DATABASE_URL is required")
		os.Exit(2)
	}
	if err := os.MkdirAll(cfg.AudioRoot, 0o750); err != nil {
		slog.Error("create audio root", "error", err)
		os.Exit(2)
	}
	pool, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
	if err != nil {
		slog.Error("connect postgres", "error", err)
		os.Exit(2)
	}
	defer pool.Close()
	if err := pool.Ping(context.Background()); err != nil {
		slog.Error("ping postgres", "error", err)
		os.Exit(2)
	}
	s := &server{cfg: cfg, db: pool, logger: slog.Default(), templates: template.Must(template.New("cr").Funcs(template.FuncMap{"dur": formatDuration, "tdate": formatTimePtr, "srcBadge": sourceBadge, "inc": func(n int) int { return n + 1 }, "dec": func(n int) int { return n - 1 }, "slice": func(v ...string) []string { return v }}).ParseFS(templatesFS, "web/templates/*.html"))}
	if err := s.bootstrapSender(context.Background()); err != nil {
		slog.Error("bootstrap sender", "error", err)
		os.Exit(2)
	}
	if cfg.LegacyEnabled {
		if err := s.bootstrapLegacySender(context.Background()); err != nil {
			slog.Error("bootstrap legacy sender", "error", err)
			os.Exit(2)
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /readyz", s.health)
	mux.HandleFunc("GET /", s.callsPage)
	mux.HandleFunc("GET /calls", s.callsFragment)
	mux.HandleFunc("GET /call/", s.callDetail)
	mux.HandleFunc("GET /export/calls.csv", s.exportCallsCSV)
	mux.HandleFunc("GET /export/call/", s.exportCallJSON)
	mux.HandleFunc("GET /download/", s.downloadCall)
	mux.HandleFunc("GET /events/calls", s.eventsCalls)
	mux.HandleFunc("GET /status", s.statusPage)
	staticSub, err := fs.Sub(staticFS, "web/static")
	if err != nil {
		slog.Error("static assets", "error", err)
		os.Exit(2)
	}
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		http.FileServerFS(staticSub).ServeHTTP(w, r)
	})))
	if cfg.AdminEnabled && (cfg.AdminOpen || cfg.AdminToken != "" || (cfg.CloudflareAccessEnabled && cfg.CloudflareAdminEmail != "")) {
		mux.HandleFunc("GET /admin/login", s.adminLogin)
		mux.HandleFunc("POST /admin/login", s.adminLogin)
		mux.HandleFunc("GET /admin/talkgroups", s.adminTalkgroups)
		mux.HandleFunc("GET /admin/senders", s.adminSenders)
		mux.HandleFunc("POST /admin/senders/create", s.adminCreateSender)
		mux.HandleFunc("POST /admin/senders/replace", s.adminReplaceSender)
		mux.HandleFunc("POST /admin/senders/disable", s.adminDisableSender)
		mux.HandleFunc("POST /admin/call/", s.adminUpdateCallNotes)
		mux.HandleFunc("POST /admin/protect/", s.adminProtectCall)
		mux.HandleFunc("GET /admin/favourites", s.adminFavourites)
		mux.HandleFunc("POST /admin/favourites", s.adminSaveFavourite)
		mux.HandleFunc("POST /admin/favourites/member", s.adminSaveFavouriteMember)
		mux.HandleFunc("POST /admin/favourites/delete", s.adminDeleteFavourite)
		mux.HandleFunc("POST /admin/favourites/member/delete", s.adminDeleteFavouriteMember)
		mux.HandleFunc("GET /admin/notifications", s.adminNotifications)
		mux.HandleFunc("POST /admin/notifications/destination", s.adminSaveDestination)
		mux.HandleFunc("POST /admin/notifications/destination/action", s.adminDestinationAction)
		mux.HandleFunc("POST /admin/notifications/rule", s.adminSaveNotificationRule)
		mux.HandleFunc("POST /admin/notifications/rule/action", s.adminRuleAction)
		mux.HandleFunc("POST /admin/notifications/delivery/retry", s.adminRetryDelivery)
		mux.HandleFunc("GET /admin/notifications/history", s.adminNotificationHistory)
		mux.HandleFunc("GET /admin/transcription", s.adminTranscription)
		mux.HandleFunc("POST /admin/transcription/queue/", s.adminQueueTranscription)
		mux.HandleFunc("POST /admin/transcription/retry", s.adminRetryTranscription)
		mux.HandleFunc("POST /admin/transcription/config", s.adminSaveTranscriptionConfig)
		mux.HandleFunc("POST /admin/transcription/edit", s.adminEditTranscript)
		mux.HandleFunc("POST /admin/talkgroups", s.adminSaveTalkgroup)
		mux.HandleFunc("GET /admin/radios", s.adminRadios)
		mux.HandleFunc("POST /admin/radios", s.adminSaveRadio)
		mux.HandleFunc("GET /admin/retention", s.adminRetention)
		mux.HandleFunc("POST /admin/retention", s.adminSaveRetention)
		mux.HandleFunc("GET /admin/retention/history", s.adminRetentionHistory)
		mux.HandleFunc("POST /admin/retention/run", s.adminRunRetention)
		mux.HandleFunc("POST /admin/retention/delete", s.adminDeleteRetention)
	}
	mux.HandleFunc("GET /media/", s.media)
	mux.HandleFunc("POST /api/v1/uploads", s.createUpload)
	mux.HandleFunc("POST /api/v1/uploads/", s.receiveAudio)
	if cfg.LegacyEnabled {
		mux.HandleFunc("POST /api/callupload", s.legacyCreateUpload)
		mux.HandleFunc("POST /api/callaudioupload/", s.legacyReceiveAudio)
	}
	srv := &http.Server{Addr: cfg.ListenAddr, Handler: s.securityHeaders(mux), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 60 * time.Second}
	s.logger.Info("starting call recorder", "listen", cfg.ListenAddr)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		s.logger.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func loadConfig() config {
	return config{ListenAddr: env("CALL_RECORDER_LISTEN_ADDRESS", "0.0.0.0") + ":" + env("CALL_RECORDER_LISTEN_PORT", "8080"), DatabaseURL: os.Getenv("CALL_RECORDER_DATABASE_URL"), AudioRoot: env("CALL_RECORDER_AUDIO_ROOT", "/var/lib/call-recorder/audio"), MaxAudioBytes: envInt64("CALL_RECORDER_MAX_AUDIO_BYTES", 104857600), PendingTTL: time.Duration(envInt64("CALL_RECORDER_PENDING_TTL_SECONDS", 900)) * time.Second, StartToleranceMS: envInt64("CALL_RECORDER_DUPLICATE_START_TOLERANCE_MS", 2000), DurationTolMS: envInt64("CALL_RECORDER_DUPLICATE_DURATION_TOLERANCE_MS", 300), BootstrapSender: os.Getenv("CALL_RECORDER_BOOTSTRAP_SENDER_ID"), BootstrapKey: os.Getenv("CALL_RECORDER_BOOTSTRAP_SENDER_KEY"), LegacyEnabled: env("CALL_RECORDER_LEGACY_INGESTION_ENABLED", "false") == "true", LegacyDebug: env("CALL_RECORDER_LEGACY_DEBUG", "false") == "true", LegacyAuthID: os.Getenv("CALL_RECORDER_LEGACY_AUTH_ID"), LegacyAPIKey: os.Getenv("CALL_RECORDER_LEGACY_API_KEY"), TestFailFinalize: env("CALL_RECORDER_TEST_FAIL_FINALIZE", "false") == "true", AdminEnabled: env("CALL_RECORDER_ADMIN_ENABLED", "false") == "true", AdminOpen: env("CALL_RECORDER_ADMIN_OPEN", "false") == "true", AdminToken: os.Getenv("CALL_RECORDER_ADMIN_TOKEN"), CloudflareAccessEnabled: env("CALL_RECORDER_CLOUDFLARE_ACCESS_ENABLED", "false") == "true", CloudflareAdminEmail: strings.ToLower(strings.TrimSpace(os.Getenv("CALL_RECORDER_CLOUDFLARE_ADMIN_EMAIL"))), CloudflareTrustedProxyIPs: splitCSV(os.Getenv("CALL_RECORDER_CLOUDFLARE_TRUSTED_PROXY_IPS"))}
}
func splitCSV(value string) []string {
	var out []string
	for _, item := range strings.Split(value, ",") {
		if v := strings.TrimSpace(item); v != "" {
			out = append(out, v)
		}
	}
	return out
}
func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
func envInt64(key string, fallback int64) int64 {
	value, err := strconv.ParseInt(env(key, strconv.FormatInt(fallback, 10)), 10, 64)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}
func (s *server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; style-src-attr 'unsafe-inline'; img-src 'self'; media-src 'self'; connect-src 'self'; font-src 'self'; object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'self'")
		next.ServeHTTP(w, r)
	})
}
func (s *server) health(w http.ResponseWriter, r *http.Request) {
	if err := s.db.Ping(r.Context()); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{"database unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": version})
}
func (s *server) bootstrapSender(ctx context.Context) error {
	if s.cfg.BootstrapSender == "" || s.cfg.BootstrapKey == "" {
		return nil
	}
	hash, err := hashAPIKey(s.cfg.BootstrapKey)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `INSERT INTO remote_senders (sender_id,key_hash,enabled) VALUES ($1,$2,true) ON CONFLICT (sender_id) DO NOTHING`, s.cfg.BootstrapSender, []byte(hash))
	return err
}

func (s *server) bootstrapLegacySender(ctx context.Context) error {
	if s.cfg.LegacyAuthID == "" || s.cfg.LegacyAPIKey == "" {
		return errors.New("legacy sender ID and key are required when legacy ingestion is enabled")
	}
	hash, err := hashAPIKey(s.cfg.LegacyAPIKey)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `INSERT INTO remote_senders (sender_id,key_hash,enabled) VALUES ($1,$2,true) ON CONFLICT (sender_id) DO NOTHING`, s.cfg.LegacyAuthID, []byte(hash))
	return err
}

// legacyCreateUpload is intentionally separate from /api/v1. It only accepts
// body credentials; it never accepts modern API headers on this route.
func (s *server) legacyCreateUpload(w http.ResponseWriter, r *http.Request) {
	var request struct {
		AuthID       string `json:"apiAuthID"`
		APIKey       string `json:"apiKey"`
		AudioFormat  string `json:"callAudioFormat"`
		RecordedCall struct {
			StartTime     string  `json:"startTime"`
			Duration      float64 `json:"callDuration"`
			TalkGroupInfo struct {
				CallTargets []struct {
					ID    json.Number `json:"targetid"`
					Label string      `json:"targetlabel"`
					Tag   string      `json:"targettag"`
				} `json:"callTargets"`
				Receiver     string `json:"receiver"`
				Frequency    any    `json:"frequency"`
				SourceID     any    `json:"sourceid"`
				SourceLabel  string `json:"sourcelabel"`
				SourceTag    string `json:"sourcetag"`
				LCN          any    `json:"lcn"`
				VoiceService string `json:"voiceservice"`
				SystemID     any    `json:"systemid"`
				SystemLabel  string `json:"systemlabel"`
				SiteID       any    `json:"siteid"`
				SiteLabel    string `json:"sitelabel"`
				CallType     any    `json:"calltype"`
			} `json:"talkGroupInfo"`
		} `json:"recordedCall"`
	}
	if s.cfg.LegacyDebug {
		s.logger.Info("legacy metadata request", "content_type", r.Header.Get("Content-Type"), "content_length", r.ContentLength)
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.UseNumber()
	if err := decoder.Decode(&request); err != nil {
		if s.cfg.LegacyDebug {
			s.logger.Info("legacy metadata result", "status", 400, "message", "invalid JSON")
		}
		writeJSON(w, http.StatusBadRequest, map[string]any{"Status": 400, "StatusMessage": "invalid JSON"})
		return
	}
	canonicalSender, authenticated := s.authenticateLegacy(r.Context(), request.AuthID, request.APIKey)
	if !authenticated {
		if s.cfg.LegacyDebug {
			s.logger.Info("legacy metadata result", "sender_id", request.AuthID, "status", 403, "message", "authentication failed")
		}
		writeJSON(w, http.StatusOK, map[string]any{"Status": 403, "StatusMessage": "authentication failed"})
		return
	}
	if len(request.RecordedCall.TalkGroupInfo.CallTargets) == 0 {
		if s.cfg.LegacyDebug {
			s.logger.Info("legacy metadata result", "sender_id", request.AuthID, "status", 400, "message", "missing call target")
		}
		writeJSON(w, http.StatusOK, map[string]any{"Status": 400, "StatusMessage": "missing call target"})
		return
	}
	start, err := time.Parse(time.RFC3339Nano, request.RecordedCall.StartTime)
	if err != nil {
		if s.cfg.LegacyDebug {
			s.logger.Info("legacy metadata result", "sender_id", request.AuthID, "status", 400, "message", "invalid start time")
		}
		writeJSON(w, http.StatusOK, map[string]any{"Status": 400, "StatusMessage": "invalid start time"})
		return
	}
	target := request.RecordedCall.TalkGroupInfo.CallTargets[0]
	info := request.RecordedCall.TalkGroupInfo
	call := callMetadata{StartTime: start, DurationMS: int64(request.RecordedCall.Duration * 1000), ReceiverID: info.Receiver, SystemID: fmt.Sprint(info.SystemID), SystemName: info.SystemLabel, SiteID: fmt.Sprint(info.SiteID), SiteName: info.SiteLabel, TalkgroupID: target.ID.String(), TalkgroupName: target.Label, TalkgroupTag: target.Tag, RadioID: fmt.Sprint(info.SourceID), RadioName: info.SourceLabel, RadioTag: info.SourceTag, Frequency: fmt.Sprint(info.Frequency), LCN: fmt.Sprint(info.LCN), VoiceService: info.VoiceService, CallType: fmt.Sprint(info.CallType)}
	body, _ := json.Marshal(createUploadRequest{SenderID: canonicalSender, IdempotencyKey: "legacy-" + request.RecordedCall.StartTime + "-" + target.ID.String(), AudioFormat: strings.ToLower(request.AudioFormat), Call: call})
	forward := r.Clone(r.Context())
	forward.Body = io.NopCloser(bytes.NewReader(body))
	forward.ContentLength = int64(len(body))
	forward.Header = make(http.Header)
	forward.Header.Set("X-Call-Recorder-Key", request.APIKey)
	recorded := httptest.NewRecorder()
	s.createUpload(recorded, forward)
	var response createUploadResponse
	_ = json.Unmarshal(recorded.Body.Bytes(), &response)
	status := 200
	message := "accepted"
	if response.Error != "" {
		status = recorded.Code
		message = response.Error
	}
	if s.cfg.LegacyDebug {
		s.logger.Info("legacy metadata result", "sender_id", request.AuthID, "canonical_sender_id", canonicalSender, "status", status, "duplicate", response.Duplicate, "call_audio_id_present", response.UploadToken != "", "message", message)
	}
	writeJSON(w, http.StatusOK, map[string]any{"Status": status, "StatusMessage": message, "Duplicate": response.Duplicate, "CallAudioID": response.UploadToken})
}

func (s *server) legacyReceiveAudio(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.URL.Path, "/api/callaudioupload/")
	if s.cfg.LegacyDebug {
		s.logger.Info("legacy audio request", "content_type", r.Header.Get("Content-Type"), "content_length", r.ContentLength, "token_present", token != "")
	}
	forward := r.Clone(r.Context())
	forward.URL.Path = "/api/v1/uploads/" + token
	forward.Header = r.Header.Clone()
	// The legacy protocol authenticates the metadata request. The returned,
	// short-lived CallAudioID is the bearer credential for the ordered audio
	// request and the legacy sender does not repeat apiKey on that request.
	forward.Header.Set("X-Call-Recorder-Legacy", "1")
	recorded := httptest.NewRecorder()
	s.receiveAudio(recorded, forward)
	var response createUploadResponse
	_ = json.Unmarshal(recorded.Body.Bytes(), &response)
	status := 200
	message := "completed"
	if response.Error != "" {
		status = recorded.Code
		message = response.Error
	}
	if s.cfg.LegacyDebug {
		s.logger.Info("legacy audio result", "status", status, "message", message)
	}
	writeJSON(w, http.StatusOK, map[string]any{"Status": status, "StatusMessage": message})
}

func (s *server) createUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	defer r.Body.Close()
	var req createUploadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, errorResponse{"invalid JSON metadata"})
		return
	}
	if err := validateMetadata(req); err != nil {
		writeJSON(w, 400, errorResponse{err.Error()})
		return
	}
	if !s.authenticate(r.Context(), req.SenderID, r.Header.Get("X-Call-Recorder-Key")) {
		writeJSON(w, 401, errorResponse{"sender authentication failed"})
		return
	}
	if id, found, err := s.findDuplicate(r.Context(), req.SenderID, req.Call); err != nil {
		s.internal(w, err)
		return
	} else if found {
		writeJSON(w, 200, createUploadResponse{Duplicate: true, CallID: id})
		return
	}
	metadata, err := json.Marshal(req.Call)
	if err != nil {
		s.internal(w, err)
		return
	}
	token, err := randomToken()
	if err != nil {
		s.internal(w, err)
		return
	}
	uploadID, err := randomToken()
	if err != nil {
		s.internal(w, err)
		return
	}
	expires := time.Now().UTC().Add(s.cfg.PendingTTL)
	_, err = s.db.Exec(r.Context(), `INSERT INTO pending_uploads (id,token_hash,sender_id,idempotency_key,metadata,audio_format,expires_at,status) VALUES ($1,$2,$3,NULLIF($4,''),$5,$6,$7,'pending')`, uploadID, tokenHash(token), req.SenderID, req.IdempotencyKey, metadata, strings.ToLower(req.AudioFormat), expires)
	if err != nil {
		if strings.Contains(err.Error(), "pending_uploads_sender_idempotency_key_key") {
			writeJSON(w, 409, errorResponse{"idempotency key already pending"})
			return
		}
		s.internal(w, err)
		return
	}
	writeJSON(w, 201, createUploadResponse{UploadToken: token, ExpiresAt: expires})
}

func (s *server) receiveAudio(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.URL.Path, "/api/v1/uploads/")
	if token == "" || strings.Contains(token, "/") {
		writeJSON(w, 404, errorResponse{"upload not found"})
		return
	}
	if r.ContentLength > s.cfg.MaxAudioBytes {
		writeJSON(w, 413, errorResponse{"audio exceeds maximum size"})
		return
	}
	var pending struct {
		ID, SenderID, AudioFormat string
		Metadata                  []byte
		ExpiresAt                 time.Time
	}
	err := s.db.QueryRow(r.Context(), `SELECT id,sender_id,audio_format,metadata,expires_at FROM pending_uploads WHERE token_hash=$1 AND status='pending'`, tokenHash(token)).Scan(&pending.ID, &pending.SenderID, &pending.AudioFormat, &pending.Metadata, &pending.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, 404, errorResponse{"upload not found or already completed"})
		return
	}
	if err != nil {
		s.internal(w, err)
		return
	}
	if time.Now().UTC().After(pending.ExpiresAt) {
		_, _ = s.db.Exec(r.Context(), `UPDATE pending_uploads SET status='expired' WHERE id=$1`, pending.ID)
		writeJSON(w, 410, errorResponse{"upload token expired"})
		return
	}
	legacyBearer := r.Header.Get("X-Call-Recorder-Legacy") == "1"
	if !legacyBearer && (r.Header.Get("X-Call-Recorder-Sender") != pending.SenderID || !s.authenticate(r.Context(), pending.SenderID, r.Header.Get("X-Call-Recorder-Key"))) {
		writeJSON(w, http.StatusUnauthorized, errorResponse{"sender authentication failed"})
		return
	}
	if !contentTypeMatches(pending.AudioFormat, r.Header.Get("Content-Type")) {
		writeJSON(w, 415, errorResponse{"audio content type does not match declared format"})
		return
	}
	tmp, err := os.CreateTemp(s.cfg.AudioRoot, "upload-*.tmp")
	if err != nil {
		s.internal(w, err)
		return
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	h := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(r.Body, s.cfg.MaxAudioBytes+1))
	closeErr := tmp.Close()
	if copyErr != nil || closeErr != nil {
		s.internal(w, firstErr(copyErr, closeErr))
		return
	}
	if written == 0 || written > s.cfg.MaxAudioBytes {
		writeJSON(w, 413, errorResponse{"invalid audio size"})
		return
	}
	if err := validateAudioHeader(tmpName, pending.AudioFormat); err != nil {
		writeJSON(w, 415, errorResponse{err.Error()})
		return
	}
	var call callMetadata
	if err := json.Unmarshal(pending.Metadata, &call); err != nil {
		s.internal(w, err)
		return
	}
	if id, found, err := s.findDuplicate(r.Context(), pending.SenderID, call); err != nil {
		s.internal(w, err)
		return
	} else if found {
		_, _ = s.db.Exec(r.Context(), `UPDATE pending_uploads SET status='duplicate',completed_at=now() WHERE id=$1`, pending.ID)
		writeJSON(w, 200, createUploadResponse{Duplicate: true, CallID: id})
		return
	}
	callID, err := randomToken()
	if err != nil {
		s.internal(w, err)
		return
	}
	rel := filepath.Join(call.StartTime.UTC().Format("2006/01/02"), callID+"."+pending.AudioFormat)
	final := filepath.Join(s.cfg.AudioRoot, rel)
	if err := os.MkdirAll(filepath.Dir(final), 0o750); err != nil {
		s.internal(w, err)
		return
	}
	if err := os.Rename(tmpName, final); err != nil {
		s.internal(w, err)
		return
	}
	if s.cfg.TestFailFinalize {
		_ = os.Remove(final)
		writeJSON(w, http.StatusInternalServerError, errorResponse{"test-only finalization failure"})
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		_ = os.Remove(final)
		s.internal(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	_, err = tx.Exec(r.Context(), `INSERT INTO calls (id,sender_id,source_call_id,receiver_id,system_id,system_name,site_id,site_name,talkgroup_id,talkgroup_name,talkgroup_tag,radio_id,radio_name,radio_tag,frequency,lcn,voice_service,call_type,group_call,audio_offset_ms,start_time,duration_ms,transcript,notes,audio_format,audio_path,audio_size,audio_sha256) VALUES ($1,$2,NULLIF($3,''),NULLIF($4,''),$5,NULLIF($6,''),NULLIF($7,''),NULLIF($8,''),$9,NULLIF($10,''),NULLIF($11,''),NULLIF($12,''),NULLIF($13,''),NULLIF($14,''),NULLIF($15,''),NULLIF($16,''),NULLIF($17,''),NULLIF($18,''),$19,$20,$21,$22,NULLIF($23,''),NULLIF($24,''),$25,$26,$27,$28)`, callID, pending.SenderID, call.SourceCallID, call.ReceiverID, call.SystemID, call.SystemName, call.SiteID, call.SiteName, call.TalkgroupID, call.TalkgroupName, call.TalkgroupTag, call.RadioID, call.RadioName, call.RadioTag, call.Frequency, call.LCN, call.VoiceService, call.CallType, call.GroupCall, call.AudioOffsetMS, call.StartTime.UTC(), call.DurationMS, call.Transcript, call.Notes, pending.AudioFormat, rel, written, h.Sum(nil))
	if err == nil {
		for _, patch := range call.Patches {
			_, err = tx.Exec(r.Context(), `INSERT INTO call_targets (call_id,talkgroup_id,talkgroup_name) VALUES ($1,$2,NULLIF($3,''))`, callID, patch.TalkgroupID, patch.TalkgroupName)
			if err != nil {
				break
			}
		}
	}
	if err == nil && call.TalkgroupName != "" {
		_, err = tx.Exec(r.Context(), `INSERT INTO talkgroup_aliases (system_id,talkgroup_id,alias,source) VALUES ($1,$2,$3,'received') ON CONFLICT (system_id,talkgroup_id) DO UPDATE SET alias=EXCLUDED.alias,updated_at=now() WHERE talkgroup_aliases.source='received'`, call.SystemID, call.TalkgroupID, call.TalkgroupName)
	}
	if err == nil && call.RadioID != "" && call.RadioName != "" {
		_, err = tx.Exec(r.Context(), `INSERT INTO radio_aliases (system_id,radio_id,alias,source) VALUES ($1,$2,$3,'received') ON CONFLICT (system_id,radio_id) DO UPDATE SET alias=EXCLUDED.alias,updated_at=now() WHERE radio_aliases.source='received'`, call.SystemID, call.RadioID, call.RadioName)
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `UPDATE pending_uploads SET status='completed',completed_at=now(),completed_call_id=$2 WHERE id=$1`, pending.ID, callID)
	}
	if err != nil {
		_ = os.Remove(final)
		s.internal(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		_ = os.Remove(final)
		s.internal(w, err)
		return
	}
	s.enqueueNotifications(r.Context(), callID)
	_, _ = s.db.Exec(r.Context(), `INSERT INTO transcription_jobs(call_id,provider) SELECT $1,c.provider FROM transcription_config c JOIN talkgroup_aliases a ON a.system_id=(SELECT system_id FROM calls WHERE id=$1) AND a.talkgroup_id=(SELECT talkgroup_id FROM calls WHERE id=$1) WHERE c.id=true AND c.enabled AND a.transcription_enabled ON CONFLICT(call_id,provider) DO NOTHING`, callID)
	writeJSON(w, 201, map[string]string{"call_id": callID, "audio_path": rel})
}

func (s *server) callsPage(w http.ResponseWriter, r *http.Request) {
	f, ferr := filterFromQuery(r.URL.Query())
	data := map[string]any{"Filter": f, "RawQuery": r.URL.RawQuery}
	if rows, err := s.db.Query(r.Context(), `SELECT id,name FROM favourite_groups WHERE enabled ORDER BY display_order,id`); err == nil {
		var groups []map[string]any
		for rows.Next() {
			var id int64
			var name string
			if rows.Scan(&id, &name) == nil {
				groups = append(groups, map[string]any{"ID": id, "Name": name})
			}
		}
		rows.Close()
		data["FavouriteGroups"] = groups
	}
	// Suggestions are deliberately bounded and remain optional: typed query-string
	// filters continue to work for values not included here.
	for key, column := range map[string]string{"Senders": "sender_id", "Systems": "system_id", "Sites": "site_id", "Receivers": "receiver_id", "Talkgroups": "talkgroup_id", "Radios": "radio_id", "CallTypes": "call_type"} {
		rows, err := s.db.Query(r.Context(), `SELECT DISTINCT `+column+` FROM calls WHERE coalesce(`+column+`,'')<>'' ORDER BY `+column+` LIMIT 250`)
		if err != nil {
			continue
		}
		values := []string{}
		for rows.Next() {
			var value string
			if rows.Scan(&value) == nil {
				values = append(values, value)
			}
		}
		rows.Close()
		data[key] = values
	}
	if ferr != nil {
		data["Error"] = "Invalid filter values: dates must use YYYY-MM-DD."
	}
	s.page(w, r, "index.html", "Calls", "calls", data)
}

type filterChip struct{ Label, ClearURL string }

func callsURL(f callFilter, drop string, page int) string {
	v := url.Values{}
	set := func(k, val string) {
		if val != "" && k != drop {
			v.Set(k, val)
		}
	}
	set("q", f.Q)
	set("sender", f.Sender)
	set("system", f.System)
	set("site", f.Site)
	set("receiver", f.Receiver)
	set("talkgroup", f.Talkgroup)
	set("radio", f.Radio)
	set("call_type", f.CallType)
	set("group", f.Group)
	set("frequency", f.Frequency)
	set("min_duration", f.MinDuration)
	set("max_duration", f.MaxDuration)
	if f.Patched && drop != "patched" {
		v.Set("patched", "1")
	}
	set("date", f.Date)
	set("from", f.From)
	set("to", f.To)
	set("favourite", f.Favourite)
	if f.Sort != "newest" && drop != "sort" {
		v.Set("sort", f.Sort)
	}
	if f.SmartSort && drop != "smart_sort" {
		v.Set("smart_sort", "1")
	}
	if f.PageSize != 50 {
		v.Set("page_size", strconv.Itoa(f.PageSize))
	}
	if page > 1 {
		v.Set("page", strconv.Itoa(page))
	}
	if encoded := v.Encode(); encoded != "" {
		return "/calls?" + encoded
	}
	return "/calls"
}

func chipsFor(f callFilter) []filterChip {
	chips := []filterChip{}
	add := func(label, key, value string) {
		if value != "" {
			chips = append(chips, filterChip{Label: label + value, ClearURL: callsURL(f, key, 1)})
		}
	}
	add("Search: ", "q", f.Q)
	add("Sender: ", "sender", f.Sender)
	add("System: ", "system", f.System)
	add("Site: ", "site", f.Site)
	add("Receiver: ", "receiver", f.Receiver)
	add("Talkgroup: ", "talkgroup", f.Talkgroup)
	add("Radio: ", "radio", f.Radio)
	add("Type: ", "call_type", f.CallType)
	add("Call class: ", "group", f.Group)
	add("Frequency: ", "frequency", f.Frequency)
	add("Min duration: ", "min_duration", f.MinDuration)
	add("Max duration: ", "max_duration", f.MaxDuration)
	if f.Patched {
		chips = append(chips, filterChip{Label: "Patched", ClearURL: callsURL(f, "patched", 1)})
	}
	add("On ", "date", f.Date)
	add("From ", "from", f.From)
	add("To ", "to", f.To)
	add("Favourite: ", "favourite", f.Favourite)
	return chips
}

func (s *server) callsFragment(w http.ResponseWriter, r *http.Request) {
	f, ferr := filterFromQuery(r.URL.Query())
	if ferr != nil {
		s.render(w, "calls.html", map[string]any{"Error": "Invalid filter values: dates must use YYYY-MM-DD."})
		return
	}
	calls, total, err := s.queryCalls(r.Context(), f)
	if err != nil {
		s.logger.Error("call query failed", "error", err)
		s.render(w, "calls.html", map[string]any{"Error": "The call list could not be loaded. Try again shortly."})
		return
	}
	pages := (total + f.PageSize - 1) / f.PageSize
	data := map[string]any{"Calls": calls, "Total": total, "Pages": pages, "Filter": f, "Chips": chipsFor(f)}
	if f.Page > 1 {
		data["PrevURL"] = callsURL(f, "", f.Page-1)
		data["FirstURL"] = callsURL(f, "", 1)
	}
	if f.Page < pages {
		data["NextURL"] = callsURL(f, "", f.Page+1)
		data["LastURL"] = callsURL(f, "", pages)
	}
	s.render(w, "calls.html", data)
}
func (s *server) callDetail(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/call/")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	var c completedCall
	var raw []byte
	var patches []string
	err := s.db.QueryRow(r.Context(), `SELECT c.id,c.sender_id,coalesce(c.receiver_id,''),c.system_id,coalesce(c.system_name,''),coalesce(c.site_id,''),coalesce(c.site_name,''),c.talkgroup_id,coalesce(ta.alias,c.talkgroup_name,''),coalesce(c.talkgroup_tag,''),coalesce(c.radio_id,''),coalesce(ra.alias,c.radio_name,''),coalesce(c.radio_tag,''),coalesce(c.frequency,''),coalesce(c.lcn,''),coalesce(c.voice_service,''),c.start_time,c.duration_ms,c.audio_path,c.audio_format,c.audio_size,coalesce(c.transcript,''),coalesce(c.notes,''),coalesce(p.metadata,'{}'::jsonb),coalesce(c.call_type,''),c.protected,coalesce(c.protection_reason,''),coalesce(c.protected_by,''),c.protected_at,c.protection_expires_at,c.group_call,(SELECT count(*) FROM call_targets ct WHERE ct.call_id=c.id) FROM calls c LEFT JOIN pending_uploads p ON p.completed_call_id=c.id LEFT JOIN talkgroup_aliases ta ON ta.system_id=c.system_id AND ta.talkgroup_id=c.talkgroup_id AND ta.enabled LEFT JOIN radio_aliases ra ON ra.system_id=c.system_id AND ra.radio_id=coalesce(c.radio_id,'') AND ra.enabled WHERE c.id=$1`, id).Scan(&c.ID, &c.SenderID, &c.ReceiverID, &c.SystemID, &c.SystemName, &c.SiteID, &c.SiteName, &c.TalkgroupID, &c.TalkgroupName, &c.TalkgroupTag, &c.RadioID, &c.RadioName, &c.RadioTag, &c.Frequency, &c.LCN, &c.VoiceService, &c.StartTime, &c.DurationMS, &c.AudioPath, &c.AudioFormat, &c.AudioSize, &c.Transcript, &c.Notes, &raw, &c.CallType, &c.Protected, &c.ProtectionReason, &c.ProtectedBy, &c.ProtectedAt, &c.ProtectionExpiresAt, &c.GroupCall, &c.Patches)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	rows, err := s.db.Query(r.Context(), `SELECT talkgroup_id||coalesce(' '||talkgroup_name,'') FROM call_targets WHERE call_id=$1 ORDER BY talkgroup_id`, id)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var p string
			_ = rows.Scan(&p)
			patches = append(patches, p)
		}
	}
	meta := string(raw)
	var pretty bytes.Buffer
	if json.Indent(&pretty, raw, "", "  ") == nil {
		meta = pretty.String()
	}
	var generated string
	_ = s.db.QueryRow(r.Context(), `SELECT coalesce(edited_text,text,'') FROM transcripts WHERE call_id=$1 ORDER BY updated_at DESC LIMIT 1`, id).Scan(&generated)
	events := []map[string]any{}
	if er, e := s.db.Query(r.Context(), `SELECT protected,coalesce(reason,''),coalesce(identity,''),created_at FROM protection_events WHERE call_id=$1 ORDER BY created_at DESC LIMIT 20`, id); e == nil {
		defer er.Close()
		for er.Next() {
			var protected bool
			var reason, identity string
			var at time.Time
			if er.Scan(&protected, &reason, &identity, &at) == nil {
				events = append(events, map[string]any{"Protected": protected, "Reason": reason, "Identity": identity, "At": at})
			}
		}
	}
	s.page(w, r, "detail.html", "Call detail", "calls", map[string]any{"Call": c, "Patches": patches, "Metadata": meta, "GeneratedTranscript": generated, "ProtectionEvents": events})
}

func (s *server) adminUpdateCallNotes(w http.ResponseWriter, r *http.Request) {
	if !s.adminAuthorized(w, r) {
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/admin/call/")
	if id == "" || strings.Contains(id, "/") {
		http.Error(w, "invalid call ID", 400)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", 400)
		return
	}
	notes := strings.TrimSpace(r.FormValue("notes"))
	if len(notes) > 10000 {
		http.Error(w, "notes exceed 10000 characters", 400)
		return
	}
	identity := strings.TrimSpace(r.Header.Get("Cf-Access-Authenticated-User-Email"))
	if identity == "" {
		identity = "admin-token"
	}
	if _, err := s.db.Exec(r.Context(), `UPDATE calls SET notes=$1,notes_updated_at=now(),notes_updated_by=$2 WHERE id=$3`, notes, identity, id); err != nil {
		s.internal(w, err)
		return
	}
	http.Redirect(w, r, "/call/"+url.PathEscape(id), http.StatusSeeOther)
}

func (s *server) exportCallsCSV(w http.ResponseWriter, r *http.Request) {
	f, err := filterFromQuery(r.URL.Query())
	if err != nil {
		http.Error(w, "invalid filters", 400)
		return
	}
	f.Page = 1
	f.PageSize = 200
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="call-export.csv"`)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"Call ID", "Timestamp", "Sender", "Receiver", "System", "Site", "Talkgroup ID", "Talkgroup Alias", "Talkgroup Tag", "Radio ID", "Radio Alias", "Radio Tag", "Frequency", "LCN", "Voice Service", "Duration Seconds", "Call Type", "Patched Targets", "Transcript", "Notes", "Audio Format"})
	for {
		page, total, e := s.queryCalls(r.Context(), f)
		if e != nil {
			s.internal(w, e)
			return
		}
		for _, c := range page {
			_ = cw.Write([]string{c.ID, c.StartTime.UTC().Format(time.RFC3339Nano), c.SenderID, c.ReceiverID, c.SystemID, c.SiteID, c.TalkgroupID, c.TalkgroupName, c.TalkgroupTag, c.RadioID, c.RadioName, c.RadioTag, c.Frequency, c.LCN, c.VoiceService, fmt.Sprintf("%.3f", float64(c.DurationMS)/1000), c.CallType, strconv.Itoa(c.Patches), c.Transcript, c.Notes, c.AudioFormat})
		}
		cw.Flush()
		if cw.Error() != nil {
			return
		}
		if f.Page*f.PageSize >= total || len(page) == 0 {
			break
		}
		f.Page++
	}
}

func sanitizeExportMetadata(raw json.RawMessage) any {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return map[string]any{}
	}
	var clean func(any) any
	clean = func(v any) any {
		switch x := v.(type) {
		case map[string]any:
			for k := range x {
				switch strings.ToLower(k) {
				case "apikey", "api_key", "authorization", "cookie", "uploadtoken", "upload_token", "callaudiouploadid", "audiopath", "audio_path", "databaseurl", "database_url":
					delete(x, k)
				default:
					x[k] = clean(x[k])
				}
			}
		case []any:
			for i := range x {
				x[i] = clean(x[i])
			}
		}
		return v
	}
	return clean(value)
}

func (s *server) exportCallJSON(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/export/call/")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	var out struct {
		ID, SenderID, ReceiverID, SystemID, SystemName, SiteID, SiteName, TalkgroupID, TalkgroupName, RadioID, RadioName, Frequency, LCN, CallType, AudioFormat, Transcript, Notes string
		StartTime                                                                                                                                                                  time.Time
		DurationMS, AudioSize                                                                                                                                                      int64
		GroupCall                                                                                                                                                                  *bool
		Raw                                                                                                                                                                        json.RawMessage
	}
	err := s.db.QueryRow(r.Context(), `SELECT c.id,c.sender_id,coalesce(c.receiver_id,''),c.system_id,coalesce(c.system_name,''),coalesce(c.site_id,''),coalesce(c.site_name,''),c.talkgroup_id,coalesce(c.talkgroup_name,''),coalesce(c.radio_id,''),coalesce(c.radio_name,''),coalesce(c.frequency,''),coalesce(c.lcn,''),coalesce(c.call_type,''),c.audio_format,coalesce(c.transcript,''),coalesce(c.notes,''),c.start_time,c.duration_ms,c.audio_size,c.group_call,coalesce(p.metadata,'{}'::jsonb) FROM calls c LEFT JOIN pending_uploads p ON p.completed_call_id=c.id WHERE c.id=$1`, id).Scan(&out.ID, &out.SenderID, &out.ReceiverID, &out.SystemID, &out.SystemName, &out.SiteID, &out.SiteName, &out.TalkgroupID, &out.TalkgroupName, &out.RadioID, &out.RadioName, &out.Frequency, &out.LCN, &out.CallType, &out.AudioFormat, &out.Transcript, &out.Notes, &out.StartTime, &out.DurationMS, &out.AudioSize, &out.GroupCall, &out.Raw)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="call-`+id+`.json"`)
	// The preserved sender document is useful for interoperability, but exports
	// must never carry credentials, upload tokens, or server-local paths.
	clean := sanitizeExportMetadata(out.Raw)
	_ = json.NewEncoder(w).Encode(struct {
		ID, SenderID, ReceiverID, SystemID, SystemName, SiteID, SiteName, TalkgroupID, TalkgroupName, RadioID, RadioName, Frequency, LCN, CallType, AudioFormat, Transcript, Notes string
		StartTime                                                                                                                                                                  time.Time
		DurationMS, AudioSize                                                                                                                                                      int64
		GroupCall                                                                                                                                                                  *bool
		Raw                                                                                                                                                                        any
	}{out.ID, out.SenderID, out.ReceiverID, out.SystemID, out.SystemName, out.SiteID, out.SiteName, out.TalkgroupID, out.TalkgroupName, out.RadioID, out.RadioName, out.Frequency, out.LCN, out.CallType, out.AudioFormat, out.Transcript, out.Notes, out.StartTime, out.DurationMS, out.AudioSize, out.GroupCall, clean})
}

func (s *server) downloadCall(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/download/")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	var path, format string
	if err := s.db.QueryRow(r.Context(), `SELECT audio_path,audio_format FROM calls WHERE id=$1`, id).Scan(&path, &format); err != nil {
		http.NotFound(w, r)
		return
	}
	full := filepath.Join(s.cfg.AudioRoot, path)
	root := filepath.Clean(s.cfg.AudioRoot)
	if !strings.HasPrefix(filepath.Clean(full), root+string(os.PathSeparator)) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", mimeFor(path))
	w.Header().Set("Content-Disposition", `attachment; filename="call-`+id+`.`+format+`"`)
	http.ServeFile(w, r, full)
}

func (s *server) statusPage(w http.ResponseWriter, r *http.Request) {
	type row struct {
		Sender, System, Site string
		Calls                int
		Last                 time.Time
		Active               bool
	}
	rows, err := s.db.Query(r.Context(), `SELECT sender_id,coalesce(system_id,''),coalesce(site_id,''),count(*),max(start_time) FROM calls GROUP BY sender_id,system_id,site_id ORDER BY max(start_time) DESC`)
	if err != nil {
		s.internal(w, err)
		return
	}
	defer rows.Close()
	var items []row
	for rows.Next() {
		var x row
		if rows.Scan(&x.Sender, &x.System, &x.Site, &x.Calls, &x.Last) == nil {
			x.Active = time.Since(x.Last) <= 15*time.Minute
			items = append(items, x)
		}
	}
	s.page(w, r, "status.html", "Receiver status", "status", map[string]any{"Rows": items})
}

func (s *server) eventsCalls(w http.ResponseWriter, r *http.Request) {
	f, err := filterFromQuery(r.URL.Query())
	if err != nil {
		http.Error(w, "invalid filters", 400)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE unavailable", 500)
		return
	}
	_, _ = io.WriteString(w, "event: ready\ndata: {}\n\n")
	flusher.Flush()
	last := -1
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	maxDuration := time.NewTimer(30 * time.Minute)
	defer maxDuration.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-maxDuration.C:
			_, _ = io.WriteString(w, "event: reconnect\ndata: {}\n\n")
			flusher.Flush()
			return
		case <-ticker.C:
		}
		_, total, e := s.queryCalls(r.Context(), f)
		if e != nil {
			_, _ = io.WriteString(w, "event: error\ndata: {}\n\n")
			flusher.Flush()
			continue
		}
		if total != last {
			last = total
			payload, _ := json.Marshal(map[string]any{"count": total})
			_, _ = fmt.Fprintf(w, "event: calls\ndata: %s\n\n", payload)
			flusher.Flush()
		}
	}
}
func (s *server) adminOK(r *http.Request) bool {
	if r.Method == http.MethodPost {
		if strings.EqualFold(r.Header.Get("Sec-Fetch-Site"), "cross-site") {
			return false
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || u.Host != r.Host {
				return false
			}
		}
	}
	if s.cfg.CloudflareAccessEnabled {
		remote, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			remote = r.RemoteAddr
		}
		trusted := false
		for _, ip := range s.cfg.CloudflareTrustedProxyIPs {
			if remote == ip {
				trusted = true
				break
			}
		}
		if !trusted {
			return false
		}
		return s.cfg.CloudflareAdminEmail != "" && strings.EqualFold(strings.TrimSpace(r.Header.Get("Cf-Access-Authenticated-User-Email")), s.cfg.CloudflareAdminEmail)
	}
	if s.cfg.AdminOpen {
		return true
	}
	if s.cfg.AdminToken == "" {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Call-Recorder-Admin")), []byte(s.cfg.AdminToken)) == 1 {
		return true
	}
	if c, err := r.Cookie("call_recorder_admin"); err == nil {
		expected := sha256.Sum256([]byte(s.cfg.AdminToken))
		return subtle.ConstantTimeCompare([]byte(c.Value), []byte(hex.EncodeToString(expected[:]))) == 1
	}
	return false
}
func (s *server) adminAuthorized(w http.ResponseWriter, r *http.Request) bool {
	if !s.adminOK(r) {
		s.renderStatus(w, r, http.StatusUnauthorized, "admin_required.html", "Administration sign-in required", "", nil)
		return false
	}
	return true
}
func (s *server) adminLogin(w http.ResponseWriter, r *http.Request) {
	if s.cfg.CloudflareAccessEnabled {
		s.renderStatus(w, r, http.StatusUnauthorized, "admin_required.html", "Cloudflare Access administration", "", nil)
		return
	}
	if r.Method == http.MethodGet {
		s.page(w, r, "admin_login.html", "Administration sign-in", "", nil)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.Form.Get("token")), []byte(s.cfg.AdminToken)) != 1 {
		s.renderStatus(w, r, http.StatusUnauthorized, "admin_login.html", "Administration sign-in", "", map[string]any{"Error": "Incorrect administration token."})
		return
	}
	h := sha256.Sum256([]byte(s.cfg.AdminToken))
	http.SetCookie(w, &http.Cookie{Name: "call_recorder_admin", Value: hex.EncodeToString(h[:]), Path: "/admin", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil, MaxAge: 3600})
	http.Redirect(w, r, "/admin/talkgroups", http.StatusSeeOther)
}
func (s *server) adminSenders(w http.ResponseWriter, r *http.Request) {
	s.adminSendersPage(w, r, "", "")
}
func (s *server) adminSendersPage(w http.ResponseWriter, r *http.Request, oneTimeSender, oneTimeKey string) {
	if !s.adminAuthorized(w, r) {
		return
	}
	rows, err := s.db.Query(r.Context(), `SELECT sender_id,enabled,created_at FROM remote_senders ORDER BY sender_id`)
	if err != nil {
		s.internal(w, err)
		return
	}
	defer rows.Close()
	type senderRow struct {
		ID      string
		Enabled bool
		Created time.Time
	}
	items := []senderRow{}
	for rows.Next() {
		var x senderRow
		if err := rows.Scan(&x.ID, &x.Enabled, &x.Created); err != nil {
			s.internal(w, err)
			return
		}
		items = append(items, x)
	}
	s.page(w, r, "admin_senders.html", "Sender credentials", "senders", map[string]any{"Senders": items, "OneTimeKey": oneTimeKey, "OneTimeSender": oneTimeSender})
}
func (s *server) adminSenderWrite(w http.ResponseWriter, r *http.Request, replace bool) (string, string, error) {
	if !s.adminAuthorized(w, r) {
		return "", "", errors.New("unauthorized")
	}
	v, err := adminForm(r)
	if err != nil {
		return "", "", errors.New("invalid form")
	}
	id := strings.TrimSpace(v.Get("sender_id"))
	if id == "" || len(id) > 100 || strings.ContainsAny(id, " \t\r\n") {
		return "", "", errors.New("sender ID must be 1-100 characters without whitespace")
	}
	key, err := generateKey()
	if err != nil {
		return "", "", err
	}
	hash, err := hashAPIKey(key)
	if err != nil {
		return "", "", err
	}
	if replace {
		_, err = s.db.Exec(r.Context(), `INSERT INTO remote_senders(sender_id,key_hash,enabled) VALUES($1,$2,true) ON CONFLICT(sender_id) DO UPDATE SET key_hash=EXCLUDED.key_hash,enabled=true`, id, []byte(hash))
	} else {
		_, err = s.db.Exec(r.Context(), `INSERT INTO remote_senders(sender_id,key_hash,enabled) VALUES($1,$2,true)`, id, []byte(hash))
	}
	if err != nil {
		return "", "", err
	}
	return id, key, nil
}
func (s *server) adminCreateSender(w http.ResponseWriter, r *http.Request) {
	id, key, err := s.adminSenderWrite(w, r, false)
	if err != nil {
		if err.Error() == "unauthorized" {
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.adminSendersPage(w, r, id, key)
}
func (s *server) adminReplaceSender(w http.ResponseWriter, r *http.Request) {
	id, key, err := s.adminSenderWrite(w, r, true)
	if err != nil {
		if err.Error() == "unauthorized" {
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.adminSendersPage(w, r, id, key)
}
func (s *server) adminDisableSender(w http.ResponseWriter, r *http.Request) {
	if !s.adminAuthorized(w, r) {
		return
	}
	v, err := adminForm(r)
	if err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	id := strings.TrimSpace(v.Get("sender_id"))
	if id == "" {
		http.Error(w, "sender ID is required", http.StatusBadRequest)
		return
	}
	if _, err := s.db.Exec(r.Context(), `UPDATE remote_senders SET enabled=false WHERE sender_id=$1`, id); err != nil {
		s.internal(w, err)
		return
	}
	http.Redirect(w, r, "/admin/senders", http.StatusSeeOther)
}
func adminForm(r *http.Request) (url.Values, error) {
	if err := r.ParseForm(); err != nil {
		return nil, err
	}
	return r.PostForm, nil
}
func aliasInput(v url.Values) (system, id, alias, description, category, source string, priority int, enabled bool, err error) {
	system, id, alias = strings.TrimSpace(v.Get("system")), strings.TrimSpace(v.Get("id")), strings.TrimSpace(v.Get("alias"))
	description, category, source = strings.TrimSpace(v.Get("description")), strings.TrimSpace(v.Get("category")), strings.TrimSpace(v.Get("source"))
	if system == "" || id == "" || len(system) > 120 || len(id) > 80 || len(alias) > 240 || len(description) > 2000 || len(category) > 120 {
		err = errors.New("invalid system, ID, or field length")
		return
	}
	if source != "manual" && source != "imported" {
		source = "manual"
	}
	if raw := strings.TrimSpace(v.Get("priority")); raw != "" {
		if priority, err = strconv.Atoi(raw); err != nil {
			err = errors.New("priority must be an integer")
			return
		}
	}
	enabled = v.Get("enabled") == "on"
	return
}
func (s *server) adminSaveTalkgroup(w http.ResponseWriter, r *http.Request) {
	if !s.adminAuthorized(w, r) {
		return
	}
	v, err := adminForm(r)
	if err != nil {
		http.Error(w, "invalid form", 400)
		return
	}
	system, id, alias, desc, category, source, priority, enabled, err := aliasInput(v)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	_, err = s.db.Exec(r.Context(), `INSERT INTO talkgroup_aliases(system_id,talkgroup_id,alias,description,category,priority,enabled,source,transcription_enabled,transcription_language,notification_eligible) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,NULLIF($10,''),$11) ON CONFLICT(system_id,talkgroup_id) DO UPDATE SET alias=EXCLUDED.alias,description=EXCLUDED.description,category=EXCLUDED.category,priority=EXCLUDED.priority,enabled=EXCLUDED.enabled,source=EXCLUDED.source,transcription_enabled=EXCLUDED.transcription_enabled,transcription_language=EXCLUDED.transcription_language,notification_eligible=EXCLUDED.notification_eligible,updated_at=now()`, system, id, alias, desc, category, priority, enabled, source, v.Get("transcription_enabled") == "on", strings.TrimSpace(v.Get("transcription_language")), v.Get("notification_eligible") != "off")
	if err != nil {
		s.internal(w, err)
		return
	}
	http.Redirect(w, r, "/admin/talkgroups?q="+url.QueryEscape(v.Get("q")), http.StatusSeeOther)
}
func (s *server) adminSaveRadio(w http.ResponseWriter, r *http.Request) {
	if !s.adminAuthorized(w, r) {
		return
	}
	v, err := adminForm(r)
	if err != nil {
		http.Error(w, "invalid form", 400)
		return
	}
	system, id, alias, desc, category, source, _, enabled, err := aliasInput(v)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	_, err = s.db.Exec(r.Context(), `INSERT INTO radio_aliases(system_id,radio_id,alias,description,category,enabled,source) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(system_id,radio_id) DO UPDATE SET alias=EXCLUDED.alias,description=EXCLUDED.description,category=EXCLUDED.category,enabled=EXCLUDED.enabled,source=EXCLUDED.source,updated_at=now()`, system, id, alias, desc, category, enabled, source)
	if err != nil {
		s.internal(w, err)
		return
	}
	http.Redirect(w, r, "/admin/radios?q="+url.QueryEscape(v.Get("q")), http.StatusSeeOther)
}
func (s *server) adminTalkgroups(w http.ResponseWriter, r *http.Request) {
	if !s.adminAuthorized(w, r) {
		return
	}
	q := r.URL.Query().Get("q")
	rows, err := s.db.Query(r.Context(), `SELECT a.system_id,a.talkgroup_id,coalesce(a.alias,''),coalesce(a.description,''),coalesce(a.category,''),a.priority,a.enabled,a.source,count(c.id),max(c.start_time) FROM talkgroup_aliases a LEFT JOIN calls c ON c.system_id=a.system_id AND c.talkgroup_id=a.talkgroup_id WHERE $1='' OR a.system_id ILIKE '%'||$1||'%' OR a.talkgroup_id ILIKE '%'||$1||'%' OR coalesce(a.alias,'') ILIKE '%'||$1||'%' GROUP BY a.system_id,a.talkgroup_id,a.alias,a.description,a.category,a.priority,a.enabled,a.source ORDER BY a.system_id,a.talkgroup_id LIMIT 500`, q)
	if err != nil {
		s.internal(w, err)
		return
	}
	defer rows.Close()
	type row struct {
		System, ID, Alias, Description, Category, Source string
		Priority                                         int
		Enabled                                          bool
		Calls                                            int
		Latest                                           *time.Time
	}
	list := []row{}
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.System, &x.ID, &x.Alias, &x.Description, &x.Category, &x.Priority, &x.Enabled, &x.Source, &x.Calls, &x.Latest); err != nil {
			s.internal(w, err)
			return
		}
		list = append(list, x)
	}
	s.page(w, r, "admin_aliases.html", "Talkgroup aliases", "talkgroups", map[string]any{"Title": "Talkgroup aliases", "Kind": "talkgroups", "Aliases": list, "Q": q})
}
func (s *server) adminRadios(w http.ResponseWriter, r *http.Request) {
	if !s.adminAuthorized(w, r) {
		return
	}
	q := r.URL.Query().Get("q")
	rows, err := s.db.Query(r.Context(), `SELECT a.system_id,a.radio_id,coalesce(a.alias,''),coalesce(a.description,''),coalesce(a.category,''),a.enabled,a.source,count(c.id),max(c.start_time) FROM radio_aliases a LEFT JOIN calls c ON c.system_id=a.system_id AND c.radio_id=a.radio_id WHERE $1='' OR a.system_id ILIKE '%'||$1||'%' OR a.radio_id ILIKE '%'||$1||'%' OR coalesce(a.alias,'') ILIKE '%'||$1||'%' GROUP BY a.system_id,a.radio_id,a.alias,a.description,a.category,a.enabled,a.source ORDER BY a.system_id,a.radio_id LIMIT 500`, q)
	if err != nil {
		s.internal(w, err)
		return
	}
	defer rows.Close()
	type row struct {
		System, ID, Alias, Description, Category, Source string
		Enabled                                          bool
		Calls                                            int
		Latest                                           *time.Time
		Priority                                         int
	}
	list := []row{}
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.System, &x.ID, &x.Alias, &x.Description, &x.Category, &x.Enabled, &x.Source, &x.Calls, &x.Latest); err != nil {
			s.internal(w, err)
			return
		}
		list = append(list, x)
	}
	s.page(w, r, "admin_aliases.html", "Radio aliases", "radios", map[string]any{"Title": "Radio aliases", "Kind": "radios", "Aliases": list, "Q": q})
}
func (s *server) adminRetention(w http.ResponseWriter, r *http.Request) {
	if !s.adminAuthorized(w, r) {
		return
	}
	rows, err := s.db.Query(r.Context(), `SELECT id,name,enabled,dry_run,retention_days,coalesce(sender_filter,''),coalesce(system_filter,''),coalesce(talkgroup_filter,''),coalesce(call_type_filter,''),priority,coalesce(min_duration_ms::text,''),coalesce(max_duration_ms::text,'') FROM retention_policies ORDER BY priority DESC,id`)
	if err != nil {
		s.internal(w, err)
		return
	}
	defer rows.Close()
	type policy struct {
		ID, Days, Priority                        int
		Name, Sender, System, Talkgroup, CallType string
		Min, Max                                  string
		Enabled, DryRun                           bool
	}
	items := []policy{}
	for rows.Next() {
		var p policy
		if err := rows.Scan(&p.ID, &p.Name, &p.Enabled, &p.DryRun, &p.Days, &p.Sender, &p.System, &p.Talkgroup, &p.CallType, &p.Priority, &p.Min, &p.Max); err != nil {
			s.internal(w, err)
			return
		}
		items = append(items, p)
	}
	var edit *policy
	if raw := r.URL.Query().Get("edit"); raw != "" {
		id, parseErr := strconv.Atoi(raw)
		if parseErr != nil {
			http.Error(w, "invalid policy ID", http.StatusBadRequest)
			return
		}
		var selected policy
		if err := s.db.QueryRow(r.Context(), `SELECT id,name,enabled,dry_run,retention_days,coalesce(sender_filter,''),coalesce(system_filter,''),coalesce(talkgroup_filter,''),coalesce(call_type_filter,''),priority,coalesce(min_duration_ms::text,''),coalesce(max_duration_ms::text,'') FROM retention_policies WHERE id=$1`, id).Scan(&selected.ID, &selected.Name, &selected.Enabled, &selected.DryRun, &selected.Days, &selected.Sender, &selected.System, &selected.Talkgroup, &selected.CallType, &selected.Priority, &selected.Min, &selected.Max); err != nil {
			http.Error(w, "policy not found", http.StatusNotFound)
			return
		}
		edit = &selected
	}
	history, _ := s.db.Query(r.Context(), `SELECT r.id,coalesce(r.policy_id,0),coalesce(p.name,'(deleted policy)'),r.dry_run,r.calls_matched,r.calls_deleted,r.audio_files_deleted,r.failures,r.started_at,r.ended_at FROM retention_runs r LEFT JOIN retention_policies p ON p.id=r.policy_id ORDER BY r.id DESC LIMIT 10`)
	runs := scanRetentionRuns(history)
	s.page(w, r, "admin_retention.html", "Retention policies", "retention", map[string]any{"Policies": items, "Runs": runs, "Edit": edit})
}

type retentionRun struct {
	ID, Policy, Matched, Deleted, Audio, Failures int
	PolicyName                                    string
	Dry                                           bool
	Started, Ended                                *time.Time
}

func scanRetentionRuns(rows pgx.Rows) []retentionRun {
	runs := []retentionRun{}
	if rows == nil {
		return runs
	}
	defer rows.Close()
	for rows.Next() {
		var x retentionRun
		if rows.Scan(&x.ID, &x.Policy, &x.PolicyName, &x.Dry, &x.Matched, &x.Deleted, &x.Audio, &x.Failures, &x.Started, &x.Ended) == nil {
			runs = append(runs, x)
		}
	}
	return runs
}

func (s *server) adminRetentionHistory(w http.ResponseWriter, r *http.Request) {
	if !s.adminAuthorized(w, r) {
		return
	}
	rows, err := s.db.Query(r.Context(), `SELECT r.id,coalesce(r.policy_id,0),coalesce(p.name,'(deleted policy)'),r.dry_run,r.calls_matched,r.calls_deleted,r.audio_files_deleted,r.failures,r.started_at,r.ended_at FROM retention_runs r LEFT JOIN retention_policies p ON p.id=r.policy_id ORDER BY r.id DESC LIMIT 200`)
	if err != nil {
		s.internal(w, err)
		return
	}
	s.page(w, r, "admin_history.html", "Retention history", "history", map[string]any{"Runs": scanRetentionRuns(rows)})
}
func (s *server) adminSaveRetention(w http.ResponseWriter, r *http.Request) {
	if !s.adminAuthorized(w, r) {
		return
	}
	v, err := adminForm(r)
	if err != nil {
		http.Error(w, "invalid form", 400)
		return
	}
	days, err := strconv.Atoi(v.Get("retention_days"))
	if err != nil || days < 1 || days > 36500 {
		http.Error(w, "retention days must be between 1 and 36500", 400)
		return
	}
	priority, err := strconv.Atoi(v.Get("priority"))
	if err != nil {
		http.Error(w, "priority must be an integer", 400)
		return
	}
	min, max := v.Get("min_duration_ms"), v.Get("max_duration_ms")
	if min != "" {
		if _, e := strconv.ParseInt(min, 10, 64); e != nil {
			http.Error(w, "invalid minimum duration", 400)
			return
		}
	}
	if max != "" {
		if _, e := strconv.ParseInt(max, 10, 64); e != nil {
			http.Error(w, "invalid maximum duration", 400)
			return
		}
	}
	name := strings.TrimSpace(v.Get("name"))
	if name == "" || len(name) > 160 {
		http.Error(w, "invalid policy name", 400)
		return
	}
	id := v.Get("id")
	args := []any{name, v.Get("sender"), v.Get("system"), v.Get("talkgroup"), v.Get("call_type"), min, max, days, priority, v.Get("enabled") == "on", v.Get("dry_run") != "off"}
	var q string
	if id == "" {
		q = `INSERT INTO retention_policies(name,sender_filter,system_filter,talkgroup_filter,call_type_filter,min_duration_ms,max_duration_ms,retention_days,priority,enabled,dry_run) VALUES(NULLIF($1,''),NULLIF($2,''),NULLIF($3,''),NULLIF($4,''),NULLIF($5,''),NULLIF($6,'')::bigint,NULLIF($7,'')::bigint,$8,$9,$10,$11)`
	} else {
		pid, e := strconv.Atoi(id)
		if e != nil {
			http.Error(w, "invalid policy ID", 400)
			return
		}
		args = append(args, pid)
		q = `UPDATE retention_policies SET name=$1,sender_filter=NULLIF($2,''),system_filter=NULLIF($3,''),talkgroup_filter=NULLIF($4,''),call_type_filter=NULLIF($5,''),min_duration_ms=NULLIF($6,'')::bigint,max_duration_ms=NULLIF($7,'')::bigint,retention_days=$8,priority=$9,enabled=$10,dry_run=$11,updated_at=now() WHERE id=$12`
	}
	if _, err = s.db.Exec(r.Context(), q, args...); err != nil {
		s.internal(w, err)
		return
	}
	http.Redirect(w, r, "/admin/retention", 303)
}
func (s *server) adminDeleteRetention(w http.ResponseWriter, r *http.Request) {
	if !s.adminAuthorized(w, r) {
		return
	}
	v, e := adminForm(r)
	if e != nil {
		http.Error(w, "invalid form", 400)
		return
	}
	id, e := strconv.Atoi(v.Get("id"))
	if e != nil {
		http.Error(w, "invalid policy ID", 400)
		return
	}
	if _, e = s.db.Exec(r.Context(), `DELETE FROM retention_policies WHERE id=$1`, id); e != nil {
		s.internal(w, e)
		return
	}
	http.Redirect(w, r, "/admin/retention", 303)
}
func (s *server) adminRunRetention(w http.ResponseWriter, r *http.Request) {
	if !s.adminAuthorized(w, r) {
		return
	}
	v, e := adminForm(r)
	if e != nil {
		http.Error(w, "invalid form", 400)
		return
	}
	id, e := strconv.Atoi(v.Get("id"))
	if e != nil {
		http.Error(w, "invalid policy ID", 400)
		return
	}
	var days int
	var sender, system, tg, ct *string
	e = s.db.QueryRow(r.Context(), `SELECT retention_days,sender_filter,system_filter,talkgroup_filter,call_type_filter FROM retention_policies WHERE id=$1`, id).Scan(&days, &sender, &system, &tg, &ct)
	if e != nil {
		http.Error(w, "policy not found", 404)
		return
	}
	q := `SELECT count(*) FROM calls WHERE start_time < now()-($1::int * interval '1 day') AND (NOT protected OR protection_expires_at IS NOT NULL AND protection_expires_at <= now())`
	a := []any{days}
	for _, f := range []struct {
		v *string
		c string
	}{{sender, "sender_id"}, {system, "system_id"}, {tg, "talkgroup_id"}, {ct, "call_type"}} {
		if f.v != nil {
			a = append(a, *f.v)
			q += fmt.Sprintf(" AND %s=$%d", f.c, len(a))
		}
	}
	var n int
	if e = s.db.QueryRow(r.Context(), q, a...).Scan(&n); e != nil {
		s.internal(w, e)
		return
	}
	_, e = s.db.Exec(r.Context(), `INSERT INTO retention_runs(policy_id,ended_at,dry_run,calls_matched,summary) VALUES($1,now(),true,$2,'{"mode":"admin-dry-run"}')`, id, n)
	if e != nil {
		s.internal(w, e)
		return
	}
	http.Redirect(w, r, "/admin/retention", 303)
}

type callFilter struct {
	Q, Sender, System, Site, Receiver, Talkgroup, Radio, CallType, Group, Frequency, MinDuration, MaxDuration, Date, From, To, Favourite string
	Sort                                                                                                                                 string
	SmartSort                                                                                                                            bool
	Patched                                                                                                                              bool
	Page, PageSize                                                                                                                       int
}

func filterFromQuery(q url.Values) (callFilter, error) {
	multi := func(key string) string {
		var out []string
		seen := map[string]bool{}
		for _, raw := range q[key] {
			for _, part := range strings.Split(raw, ",") {
				part = strings.TrimSpace(part)
				if part != "" && !seen[part] {
					out = append(out, part)
					seen[part] = true
				}
			}
		}
		return strings.Join(out, ",")
	}
	f := callFilter{Q: strings.TrimSpace(q.Get("q")), Sender: multi("sender"), System: multi("system"), Site: multi("site"), Receiver: multi("receiver"), Talkgroup: multi("talkgroup"), Radio: multi("radio"), CallType: multi("call_type"), Group: strings.TrimSpace(q.Get("group")), Frequency: strings.TrimSpace(q.Get("frequency")), MinDuration: strings.TrimSpace(q.Get("min_duration")), MaxDuration: strings.TrimSpace(q.Get("max_duration")), Date: q.Get("date"), From: q.Get("from"), To: q.Get("to"), Favourite: strings.TrimSpace(q.Get("favourite")), Sort: strings.TrimSpace(q.Get("sort")), SmartSort: q.Get("smart_sort") == "1", Page: 1, PageSize: 50, Patched: q.Get("patched") == "1" || strings.EqualFold(q.Get("patched"), "true")}
	if f.Group != "" && f.Group != "group" && f.Group != "private" {
		f.Group = ""
	}
	switch f.Sort {
	case "oldest", "talkgroup", "radio", "duration", "frequency", "system", "site", "calltype", "lcn", "receiver", "talkgroup_label", "radio_label":
	default:
		f.Sort = "newest"
	}
	for _, d := range []string{f.Date, f.From, f.To} {
		if d != "" {
			if _, err := time.Parse("2006-01-02", d); err != nil {
				return f, errors.New("dates must use YYYY-MM-DD")
			}
		}
	}
	for _, d := range []string{f.MinDuration, f.MaxDuration} {
		if d != "" {
			if n, err := strconv.ParseFloat(d, 64); err != nil || n < 0 || n > 86400 {
				return f, errors.New("durations must be nonnegative seconds")
			}
		}
	}
	if n, err := strconv.Atoi(q.Get("page")); err == nil && n > 0 {
		f.Page = n
	}
	if n, err := strconv.Atoi(q.Get("page_size")); err == nil {
		switch n {
		case 25, 50, 100, 250:
			f.PageSize = n
		}
	}
	return f, nil
}

const callsFrom = ` FROM calls c LEFT JOIN talkgroup_aliases ta ON ta.system_id=c.system_id AND ta.talkgroup_id=c.talkgroup_id AND ta.enabled LEFT JOIN radio_aliases ra ON ra.system_id=c.system_id AND ra.radio_id=coalesce(c.radio_id,'') AND ra.enabled`
const callsWhere = ` WHERE ($1='' OR c.search_document @@ plainto_tsquery('simple',$1) OR c.search_document::text ILIKE '%'||lower($1)||'%') AND ($2='' OR c.sender_id=ANY(string_to_array($2,','))) AND ($3='' OR c.system_id=ANY(string_to_array($3,','))) AND ($4='' OR c.site_id=ANY(string_to_array($4,','))) AND ($5='' OR c.receiver_id=ANY(string_to_array($5,','))) AND ($6='' OR c.talkgroup_id=ANY(string_to_array($6,','))) AND ($7='' OR c.radio_id=ANY(string_to_array($7,','))) AND ($8='' OR c.call_type=ANY(string_to_array($8,','))) AND ($9='' OR ($9='group' AND c.group_call=true) OR ($9='private' AND c.group_call=false)) AND ($10='' OR c.frequency ILIKE '%'||$10||'%') AND ($11='' OR c.duration_ms >= ($11::double precision*1000)) AND ($12='' OR c.duration_ms <= ($12::double precision*1000)) AND ($13='' OR c.start_time::date=$13::date) AND ($14='' OR c.start_time::date>=$14::date) AND ($15='' OR c.start_time::date<=$15::date) AND (NOT $16 OR EXISTS (SELECT 1 FROM call_targets ct WHERE ct.call_id=c.id)) AND ($17='' OR EXISTS (SELECT 1 FROM favourite_members fm WHERE fm.system_id=c.system_id AND fm.talkgroup_id=c.talkgroup_id AND fm.group_id=$17::bigint))`

func (s *server) queryCalls(ctx context.Context, f callFilter) ([]completedCall, int, error) {
	args := []any{f.Q, f.Sender, f.System, f.Site, f.Receiver, f.Talkgroup, f.Radio, f.CallType, f.Group, f.Frequency, f.MinDuration, f.MaxDuration, f.Date, f.From, f.To, f.Patched, f.Favourite}
	var total int
	if err := s.db.QueryRow(ctx, `SELECT count(*)`+callsFrom+callsWhere, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	orderBy := "c.start_time DESC"
	if f.SmartSort {
		orderBy = "CASE WHEN lower(coalesce(c.call_type,'')) IN ('emergency','priority') THEN 0 ELSE 1 END,c.start_time DESC"
	}
	switch f.Sort {
	case "oldest":
		orderBy = "c.start_time ASC"
	case "talkgroup":
		orderBy = "c.talkgroup_id,c.start_time DESC"
	case "radio":
		orderBy = "c.radio_id,c.start_time DESC"
	case "duration":
		orderBy = "c.duration_ms DESC,c.start_time DESC"
	case "frequency":
		orderBy = "c.frequency,c.start_time DESC"
	case "system":
		orderBy = "c.system_id,c.start_time DESC"
	case "site":
		orderBy = "c.site_id,c.start_time DESC"
	case "calltype":
		orderBy = "c.call_type,c.start_time DESC"
	case "lcn":
		orderBy = "c.lcn,c.start_time DESC"
	case "receiver":
		orderBy = "c.receiver_id,c.start_time DESC"
	case "talkgroup_label":
		orderBy = "coalesce(ta.alias,c.talkgroup_name,''),c.start_time DESC"
	case "radio_label":
		orderBy = "coalesce(ra.alias,c.radio_name,''),c.start_time DESC"
	}
	query := `SELECT c.id,c.sender_id,coalesce(c.receiver_id,''),c.system_id,coalesce(c.system_name,''),coalesce(c.site_id,''),coalesce(c.site_name,''),c.talkgroup_id,coalesce(ta.alias,c.talkgroup_name,''),coalesce(c.talkgroup_tag,''),coalesce(c.radio_id,''),coalesce(ra.alias,c.radio_name,''),coalesce(c.radio_tag,''),coalesce(c.frequency,''),coalesce(c.lcn,''),coalesce(c.voice_service,''),c.start_time,c.duration_ms,c.audio_path,c.audio_format,c.audio_size,coalesce(c.transcript,''),coalesce(c.notes,''),coalesce(c.call_type,''),c.protected,c.group_call,(SELECT count(*) FROM call_targets ct WHERE ct.call_id=c.id),EXISTS(SELECT 1 FROM transcripts t WHERE t.call_id=c.id),coalesce((SELECT tj.status FROM transcription_jobs tj WHERE tj.call_id=c.id ORDER BY tj.updated_at DESC LIMIT 1),'')` + callsFrom + callsWhere + ` ORDER BY ` + orderBy + ` LIMIT $18 OFFSET $19`
	result, err := s.db.Query(ctx, query, append(args, f.PageSize, (f.Page-1)*f.PageSize)...)
	if err != nil {
		return nil, 0, err
	}
	defer result.Close()
	calls := []completedCall{}
	for result.Next() {
		var c completedCall
		if err := result.Scan(&c.ID, &c.SenderID, &c.ReceiverID, &c.SystemID, &c.SystemName, &c.SiteID, &c.SiteName, &c.TalkgroupID, &c.TalkgroupName, &c.TalkgroupTag, &c.RadioID, &c.RadioName, &c.RadioTag, &c.Frequency, &c.LCN, &c.VoiceService, &c.StartTime, &c.DurationMS, &c.AudioPath, &c.AudioFormat, &c.AudioSize, &c.Transcript, &c.Notes, &c.CallType, &c.Protected, &c.GroupCall, &c.Patches, &c.GeneratedTranscript, &c.TranscriptionStatus); err != nil {
			return nil, 0, err
		}
		calls = append(calls, c)
	}
	return calls, total, result.Err()
}
func (s *server) media(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/media/")
	if len(id) < 16 || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	var path string
	err := s.db.QueryRow(r.Context(), `SELECT audio_path FROM calls WHERE id=$1`, id).Scan(&path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	full := filepath.Join(s.cfg.AudioRoot, path)
	if !strings.HasPrefix(filepath.Clean(full), filepath.Clean(s.cfg.AudioRoot)+string(os.PathSeparator)) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", mimeFor(path))
	http.ServeFile(w, r, full)
}
func (s *server) authenticate(ctx context.Context, sender, key string) bool {
	if sender == "" || key == "" {
		return false
	}
	var hash []byte
	var enabled bool
	err := s.db.QueryRow(ctx, `SELECT key_hash,enabled FROM remote_senders WHERE sender_id=$1`, sender).Scan(&hash, &enabled)
	return err == nil && enabled && verifyAPIKey(string(hash), key)
}

// authenticateLegacy accepts case variations from legacy senders while keeping
// the canonical sender_id stored in remote_senders and calls. Modern API
// authentication remains case-sensitive.
func (s *server) authenticateLegacy(ctx context.Context, sender, key string) (string, bool) {
	if sender == "" || key == "" {
		return "", false
	}
	var canonical string
	var hash []byte
	var enabled bool
	err := s.db.QueryRow(ctx, `SELECT sender_id,key_hash,enabled FROM remote_senders WHERE lower(sender_id)=lower($1) LIMIT 1`, sender).Scan(&canonical, &hash, &enabled)
	if err != nil || !enabled || !verifyAPIKey(string(hash), key) {
		return "", false
	}
	return canonical, true
}
func (s *server) findDuplicate(ctx context.Context, senderID string, c callMetadata) (string, bool, error) {
	var id string
	err := s.db.QueryRow(ctx, `SELECT id FROM calls WHERE sender_id=$1 AND system_id=$2 AND talkgroup_id=$3 AND coalesce(radio_id,'')=coalesce(NULLIF($4,''),'') AND coalesce(site_id,'')=coalesce(NULLIF($5,''),'') AND coalesce(voice_service,'')=coalesce(NULLIF($6,''),'') AND coalesce(call_type,'')=coalesce(NULLIF($7,''),'') AND start_time BETWEEN $8::timestamptz - ($9::bigint * interval '1 millisecond') AND $8::timestamptz + ($9::bigint * interval '1 millisecond') AND abs(duration_ms-$10) <= $11 ORDER BY start_time DESC LIMIT 1`, senderID, c.SystemID, c.TalkgroupID, c.RadioID, c.SiteID, c.VoiceService, c.CallType, c.StartTime.UTC(), s.cfg.StartToleranceMS, c.DurationMS, s.cfg.DurationTolMS).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	return id, err == nil, err
}
func validateMetadata(r createUploadRequest) error {
	if r.SenderID == "" || len(r.SenderID) > 100 {
		return errors.New("sender_id is required and limited to 100 characters")
	}
	if r.AudioFormat != "mp3" && r.AudioFormat != "wav" {
		return errors.New("audio_format must be mp3 or wav")
	}
	c := r.Call
	if c.StartTime.IsZero() || c.DurationMS <= 0 || c.DurationMS > 86400000 {
		return errors.New("start_time and a valid duration_ms are required")
	}
	if c.SystemID == "" || c.TalkgroupID == "" {
		return errors.New("system_id and talkgroup_id are required")
	}
	return nil
}
func contentTypeMatches(format, ct string) bool {
	ct = strings.ToLower(strings.Split(ct, ";")[0])
	return (format == "mp3" && (ct == "audio/mpeg" || ct == "audio/mp3")) || (format == "wav" && (ct == "audio/wav" || ct == "audio/x-wav" || ct == "audio/wave"))
}
func validateAudioHeader(path, format string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	b := make([]byte, 12)
	n, _ := io.ReadFull(f, b)
	if n < 4 {
		return errors.New("audio file is too short")
	}
	if format == "wav" && (n < 12 || string(b[:4]) != "RIFF" || string(b[8:12]) != "WAVE") {
		return errors.New("invalid WAV header")
	}
	if format == "mp3" && !(string(b[:3]) == "ID3" || (b[0] == 0xff && (b[1]&0xe0) == 0xe0)) {
		return errors.New("invalid MP3 header")
	}
	return nil
}
func randomToken() (string, error) {
	b := make([]byte, 24)
	_, err := rand.Read(b)
	return hex.EncodeToString(b), err
}
func tokenHash(value string) []byte { h := sha256.Sum256([]byte(value)); return h[:] }

func generateKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func hashAPIKey(value string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	digest := argon2.IDKey([]byte(value), salt, 3, 64*1024, 2, 32)
	return "argon2id$v=19$m=65536,t=3,p=2$" + hex.EncodeToString(salt) + "$" + hex.EncodeToString(digest), nil
}

func verifyAPIKey(encoded, value string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 5 || parts[0] != "argon2id" {
		return false
	}
	var memory, iterations uint32
	var parallelism uint8
	if _, err := fmt.Sscanf(parts[2], "m=%d,t=%d,p=%d", &memory, &iterations, &parallelism); err != nil {
		return false
	}
	salt, err := hex.DecodeString(parts[3])
	if err != nil {
		return false
	}
	expected, err := hex.DecodeString(parts[4])
	if err != nil {
		return false
	}
	actual := argon2.IDKey([]byte(value), salt, iterations, memory, parallelism, uint32(len(expected)))
	return subtle.ConstantTimeCompare(actual, expected) == 1
}
func mimeFor(path string) string {
	if strings.HasSuffix(path, ".wav") {
		return "audio/wav"
	}
	return "audio/mpeg"
}
func formatDuration(ms int64) string {
	if ms <= 0 {
		return "0:00"
	}
	total := ms / 1000
	m, s := total/60, total%60
	if m >= 60 {
		return fmt.Sprintf("%d:%02d:%02d", m/60, m%60, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}
func formatTimePtr(t *time.Time) string {
	if t == nil {
		return "—"
	}
	return t.Local().Format("2006-01-02 15:04")
}
func sourceBadge(source, alias string) template.HTML {
	if alias == "" {
		return `<span class="badge">numeric fallback</span>`
	}
	switch source {
	case "manual":
		return `<span class="badge info">manual</span>`
	case "imported":
		return `<span class="badge purple">imported</span>`
	default:
		return `<span class="badge">received</span>`
	}
}
func firstErr(a, b error) error {
	if a != nil {
		return a
	}
	return b
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func (s *server) internal(w http.ResponseWriter, err error) {
	s.logger.Error("request failed", "error", err)
	writeJSON(w, 500, errorResponse{"internal server error"})
}
func (s *server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, name, data); err != nil {
		s.internal(w, err)
	}
}

type navContext struct {
	Title, Active, Version   string
	AdminEnabled, Authorized bool
}

func (s *server) nav(r *http.Request, active, title string) navContext {
	return navContext{Title: title, Active: active, Version: version, AdminEnabled: s.cfg.AdminEnabled, Authorized: s.cfg.AdminEnabled && s.adminOK(r)}
}
func (s *server) renderStatus(w http.ResponseWriter, r *http.Request, status int, name, title, active string, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	data["Nav"] = s.nav(r, active, title)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := s.templates.ExecuteTemplate(w, name, data); err != nil {
		s.logger.Error("render failed", "template", name, "error", err)
	}
}
func (s *server) page(w http.ResponseWriter, r *http.Request, name, title, active string, data map[string]any) {
	s.renderStatus(w, r, http.StatusOK, name, title, active, data)
}
