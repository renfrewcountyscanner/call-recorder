package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
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

type config struct {
	ListenAddr       string
	DatabaseURL      string
	AudioRoot        string
	MaxAudioBytes    int64
	PendingTTL       time.Duration
	StartToleranceMS int64
	DurationTolMS    int64
	BootstrapSender  string
	BootstrapKey     string
	LegacyEnabled    bool
	LegacyAuthID     string
	LegacyAPIKey     string
	TestFailFinalize bool
	AdminEnabled     bool
	AdminToken       string
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
	ID, SenderID, ReceiverID, SystemID, SystemName, SiteID, SiteName, TalkgroupID, TalkgroupName, RadioID, RadioName, Frequency, AudioPath, AudioFormat, Transcript, Notes string
	StartTime                                                                                                                                                              time.Time
	DurationMS                                                                                                                                                             int64
	AudioSize                                                                                                                                                              int64
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
	s := &server{cfg: cfg, db: pool, logger: slog.Default(), templates: template.Must(template.ParseFS(templatesFS, "web/templates/*.html"))}
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
	mux.HandleFunc("GET /", s.callsPage)
	mux.HandleFunc("GET /calls", s.callsFragment)
	mux.HandleFunc("GET /call/", s.callDetail)
	if cfg.AdminEnabled && cfg.AdminToken != "" {
		mux.HandleFunc("GET /admin/login", s.adminLogin)
		mux.HandleFunc("POST /admin/login", s.adminLogin)
		mux.HandleFunc("GET /admin/talkgroups", s.adminTalkgroups)
		mux.HandleFunc("POST /admin/talkgroups", s.adminSaveTalkgroup)
		mux.HandleFunc("GET /admin/radios", s.adminRadios)
		mux.HandleFunc("POST /admin/radios", s.adminSaveRadio)
		mux.HandleFunc("GET /admin/retention", s.adminRetention)
		mux.HandleFunc("POST /admin/retention", s.adminSaveRetention)
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
	return config{ListenAddr: env("CALL_RECORDER_LISTEN_ADDRESS", "0.0.0.0") + ":" + env("CALL_RECORDER_LISTEN_PORT", "8080"), DatabaseURL: os.Getenv("CALL_RECORDER_DATABASE_URL"), AudioRoot: env("CALL_RECORDER_AUDIO_ROOT", "/var/lib/call-recorder/audio"), MaxAudioBytes: envInt64("CALL_RECORDER_MAX_AUDIO_BYTES", 104857600), PendingTTL: time.Duration(envInt64("CALL_RECORDER_PENDING_TTL_SECONDS", 900)) * time.Second, StartToleranceMS: envInt64("CALL_RECORDER_DUPLICATE_START_TOLERANCE_MS", 2000), DurationTolMS: envInt64("CALL_RECORDER_DUPLICATE_DURATION_TOLERANCE_MS", 300), BootstrapSender: os.Getenv("CALL_RECORDER_BOOTSTRAP_SENDER_ID"), BootstrapKey: os.Getenv("CALL_RECORDER_BOOTSTRAP_SENDER_KEY"), LegacyEnabled: env("CALL_RECORDER_LEGACY_INGESTION_ENABLED", "false") == "true", LegacyAuthID: os.Getenv("CALL_RECORDER_LEGACY_AUTH_ID"), LegacyAPIKey: os.Getenv("CALL_RECORDER_LEGACY_API_KEY"), TestFailFinalize: env("CALL_RECORDER_TEST_FAIL_FINALIZE", "false") == "true", AdminEnabled: env("CALL_RECORDER_ADMIN_ENABLED", "false") == "true", AdminToken: os.Getenv("CALL_RECORDER_ADMIN_TOKEN")}
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
		next.ServeHTTP(w, r)
	})
}
func (s *server) health(w http.ResponseWriter, r *http.Request) {
	if err := s.db.Ping(r.Context()); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{"database unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
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
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.UseNumber()
	if err := decoder.Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"Status": 400, "StatusMessage": "invalid JSON"})
		return
	}
	if request.AuthID != s.cfg.LegacyAuthID || subtle.ConstantTimeCompare([]byte(request.APIKey), []byte(s.cfg.LegacyAPIKey)) != 1 {
		writeJSON(w, http.StatusOK, map[string]any{"Status": 403, "StatusMessage": "authentication failed"})
		return
	}
	if len(request.RecordedCall.TalkGroupInfo.CallTargets) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"Status": 400, "StatusMessage": "missing call target"})
		return
	}
	start, err := time.Parse(time.RFC3339Nano, request.RecordedCall.StartTime)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"Status": 400, "StatusMessage": "invalid start time"})
		return
	}
	target := request.RecordedCall.TalkGroupInfo.CallTargets[0]
	info := request.RecordedCall.TalkGroupInfo
	call := callMetadata{StartTime: start, DurationMS: int64(request.RecordedCall.Duration * 1000), ReceiverID: info.Receiver, SystemID: fmt.Sprint(info.SystemID), SystemName: info.SystemLabel, SiteID: fmt.Sprint(info.SiteID), SiteName: info.SiteLabel, TalkgroupID: target.ID.String(), TalkgroupName: target.Label, TalkgroupTag: target.Tag, RadioID: fmt.Sprint(info.SourceID), RadioName: info.SourceLabel, RadioTag: info.SourceTag, Frequency: fmt.Sprint(info.Frequency), LCN: fmt.Sprint(info.LCN), VoiceService: info.VoiceService, CallType: fmt.Sprint(info.CallType)}
	body, _ := json.Marshal(createUploadRequest{SenderID: s.cfg.LegacyAuthID, IdempotencyKey: "legacy-" + request.RecordedCall.StartTime + "-" + target.ID.String(), AudioFormat: strings.ToLower(request.AudioFormat), Call: call})
	forward := r.Clone(r.Context())
	forward.Body = io.NopCloser(bytes.NewReader(body))
	forward.ContentLength = int64(len(body))
	forward.Header = make(http.Header)
	forward.Header.Set("X-Call-Recorder-Key", s.cfg.LegacyAPIKey)
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
	writeJSON(w, http.StatusOK, map[string]any{"Status": status, "StatusMessage": message, "Duplicate": response.Duplicate, "CallAudioID": response.UploadToken})
}

func (s *server) legacyReceiveAudio(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.URL.Path, "/api/callaudioupload/")
	forward := r.Clone(r.Context())
	forward.URL.Path = "/api/v1/uploads/" + token
	forward.Header = r.Header.Clone()
	forward.Header.Set("X-Call-Recorder-Sender", s.cfg.LegacyAuthID)
	forward.Header.Set("X-Call-Recorder-Key", s.cfg.LegacyAPIKey)
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
	if r.Header.Get("X-Call-Recorder-Sender") != pending.SenderID || !s.authenticate(r.Context(), pending.SenderID, r.Header.Get("X-Call-Recorder-Key")) {
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
	writeJSON(w, 201, map[string]string{"call_id": callID, "audio_path": rel})
}

func (s *server) callsPage(w http.ResponseWriter, r *http.Request) {
	s.render(w, "index.html", map[string]any{"Title": "Call Recorder"})
}
func (s *server) callsFragment(w http.ResponseWriter, r *http.Request) {
	rows, err := s.queryCalls(r.Context(), r.URL.Query())
	if err != nil {
		s.internal(w, err)
		return
	}
	s.render(w, "calls.html", map[string]any{"Calls": rows})
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
	err := s.db.QueryRow(r.Context(), `SELECT c.id,c.sender_id,coalesce(c.receiver_id,''),c.system_id,coalesce(c.system_name,''),coalesce(c.site_id,''),coalesce(c.site_name,''),c.talkgroup_id,coalesce(ta.alias,c.talkgroup_name,''),coalesce(c.radio_id,''),coalesce(ra.alias,c.radio_name,''),coalesce(c.frequency,''),c.start_time,c.duration_ms,c.audio_path,c.audio_format,c.audio_size,coalesce(c.transcript,''),coalesce(c.notes,''),coalesce(p.metadata,'{}'::jsonb) FROM calls c LEFT JOIN pending_uploads p ON p.completed_call_id=c.id LEFT JOIN talkgroup_aliases ta ON ta.system_id=c.system_id AND ta.talkgroup_id=c.talkgroup_id AND ta.enabled LEFT JOIN radio_aliases ra ON ra.system_id=c.system_id AND ra.radio_id=coalesce(c.radio_id,'') AND ra.enabled WHERE c.id=$1`, id).Scan(&c.ID, &c.SenderID, &c.ReceiverID, &c.SystemID, &c.SystemName, &c.SiteID, &c.SiteName, &c.TalkgroupID, &c.TalkgroupName, &c.RadioID, &c.RadioName, &c.Frequency, &c.StartTime, &c.DurationMS, &c.AudioPath, &c.AudioFormat, &c.AudioSize, &c.Transcript, &c.Notes, &raw)
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
	s.render(w, "detail.html", map[string]any{"Call": c, "Patches": patches, "Metadata": string(raw)})
}
func (s *server) adminAuthorized(w http.ResponseWriter, r *http.Request) bool {
	headerOK := subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Call-Recorder-Admin")), []byte(s.cfg.AdminToken)) == 1
	cookieOK := false
	if c, err := r.Cookie("call_recorder_admin"); err == nil {
		expected := sha256.Sum256([]byte(s.cfg.AdminToken))
		cookieOK = subtle.ConstantTimeCompare([]byte(c.Value), []byte(hex.EncodeToString(expected[:]))) == 1
	}
	if !headerOK && !cookieOK {
		http.Error(w, "administration authorization required", http.StatusUnauthorized)
		return false
	}
	return true
}
func (s *server) adminLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		s.render(w, "admin_login.html", nil)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.Form.Get("token")), []byte(s.cfg.AdminToken)) != 1 {
		http.Error(w, "administration authorization required", http.StatusUnauthorized)
		return
	}
	h := sha256.Sum256([]byte(s.cfg.AdminToken))
	http.SetCookie(w, &http.Cookie{Name: "call_recorder_admin", Value: hex.EncodeToString(h[:]), Path: "/admin", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil, MaxAge: 3600})
	http.Redirect(w, r, "/admin/talkgroups", http.StatusSeeOther)
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
	priority, err = strconv.Atoi(v.Get("priority"))
	if err != nil {
		err = errors.New("priority must be an integer")
		return
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
	_, err = s.db.Exec(r.Context(), `INSERT INTO talkgroup_aliases(system_id,talkgroup_id,alias,description,category,priority,enabled,source) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(system_id,talkgroup_id) DO UPDATE SET alias=EXCLUDED.alias,description=EXCLUDED.description,category=EXCLUDED.category,priority=EXCLUDED.priority,enabled=EXCLUDED.enabled,source=EXCLUDED.source,updated_at=now()`, system, id, alias, desc, category, priority, enabled, source)
	if err != nil {
		s.internal(w, err)
		return
	}
	http.Redirect(w, r, "/admin/talkgroups", http.StatusSeeOther)
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
	http.Redirect(w, r, "/admin/radios", http.StatusSeeOther)
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
	s.render(w, "admin_aliases.html", map[string]any{"Title": "Talkgroup aliases", "Kind": "talkgroups", "Aliases": list})
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
	s.render(w, "admin_aliases.html", map[string]any{"Title": "Radio aliases", "Kind": "radios", "Aliases": list})
}
func (s *server) adminRetention(w http.ResponseWriter, r *http.Request) {
	if !s.adminAuthorized(w, r) {
		return
	}
	rows, err := s.db.Query(r.Context(), `SELECT id,name,enabled,dry_run,retention_days,coalesce(sender_filter,''),coalesce(system_filter,''),coalesce(talkgroup_filter,''),coalesce(call_type_filter,''),priority FROM retention_policies ORDER BY priority DESC,id`)
	if err != nil {
		s.internal(w, err)
		return
	}
	defer rows.Close()
	type policy struct {
		ID, Days, Priority                        int
		Name, Sender, System, Talkgroup, CallType string
		Enabled, DryRun                           bool
	}
	items := []policy{}
	for rows.Next() {
		var p policy
		if err := rows.Scan(&p.ID, &p.Name, &p.Enabled, &p.DryRun, &p.Days, &p.Sender, &p.System, &p.Talkgroup, &p.CallType, &p.Priority); err != nil {
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
		if err := s.db.QueryRow(r.Context(), `SELECT id,name,enabled,dry_run,retention_days,coalesce(sender_filter,''),coalesce(system_filter,''),coalesce(talkgroup_filter,''),coalesce(call_type_filter,''),priority FROM retention_policies WHERE id=$1`, id).Scan(&selected.ID, &selected.Name, &selected.Enabled, &selected.DryRun, &selected.Days, &selected.Sender, &selected.System, &selected.Talkgroup, &selected.CallType, &selected.Priority); err != nil {
			http.Error(w, "policy not found", http.StatusNotFound)
			return
		}
		edit = &selected
	}
	history, _ := s.db.Query(r.Context(), `SELECT id,coalesce(policy_id,0),dry_run,calls_matched,calls_deleted,audio_files_deleted,failures,ended_at FROM retention_runs ORDER BY id DESC LIMIT 25`)
	type run struct {
		ID, Policy, Matched, Deleted, Audio, Failures int
		Dry                                           bool
		Ended                                         *time.Time
	}
	runs := []run{}
	if history != nil {
		defer history.Close()
		for history.Next() {
			var x run
			if history.Scan(&x.ID, &x.Policy, &x.Dry, &x.Matched, &x.Deleted, &x.Audio, &x.Failures, &x.Ended) == nil {
				runs = append(runs, x)
			}
		}
	}
	s.render(w, "admin_retention.html", map[string]any{"Policies": items, "Runs": runs, "Edit": edit})
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
	q := `SELECT count(*) FROM calls WHERE start_time < now()-($1::int * interval '1 day')`
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
func (s *server) queryCalls(ctx context.Context, q url.Values) ([]completedCall, error) {
	query := `SELECT c.id,c.sender_id,coalesce(c.receiver_id,''),c.system_id,coalesce(c.system_name,''),coalesce(c.site_id,''),coalesce(c.site_name,''),c.talkgroup_id,coalesce(ta.alias,c.talkgroup_name,''),coalesce(c.radio_id,''),coalesce(ra.alias,c.radio_name,''),coalesce(c.frequency,''),c.start_time,c.duration_ms,c.audio_path,c.audio_format,c.audio_size,coalesce(c.transcript,''),coalesce(c.notes,'') FROM calls c LEFT JOIN talkgroup_aliases ta ON ta.system_id=c.system_id AND ta.talkgroup_id=c.talkgroup_id AND ta.enabled LEFT JOIN radio_aliases ra ON ra.system_id=c.system_id AND ra.radio_id=coalesce(c.radio_id,'') AND ra.enabled WHERE ($1='' OR c.system_id ILIKE '%'||$1||'%' OR c.talkgroup_id ILIKE '%'||$1||'%' OR coalesce(ta.alias,c.talkgroup_name,'') ILIKE '%'||$1||'%' OR coalesce(c.radio_id,'') ILIKE '%'||$1||'%' OR coalesce(ra.alias,c.radio_name,'') ILIKE '%'||$1||'%' OR coalesce(c.transcript,'') ILIKE '%'||$1||'%') AND ($2='' OR c.sender_id=$2) AND ($3='' OR c.system_id=$3) AND ($4='' OR c.talkgroup_id=$4) AND ($5='' OR c.radio_id=$5) AND ($6='' OR c.start_time::date=$6::date) ORDER BY c.start_time DESC LIMIT 100`
	result, err := s.db.Query(ctx, query, q.Get("q"), q.Get("sender"), q.Get("system"), q.Get("talkgroup"), q.Get("radio"), q.Get("date"))
	if err != nil {
		return nil, err
	}
	defer result.Close()
	calls := []completedCall{}
	for result.Next() {
		var c completedCall
		if err := result.Scan(&c.ID, &c.SenderID, &c.ReceiverID, &c.SystemID, &c.SystemName, &c.SiteID, &c.SiteName, &c.TalkgroupID, &c.TalkgroupName, &c.RadioID, &c.RadioName, &c.Frequency, &c.StartTime, &c.DurationMS, &c.AudioPath, &c.AudioFormat, &c.AudioSize, &c.Transcript, &c.Notes); err != nil {
			return nil, err
		}
		calls = append(calls, c)
	}
	return calls, result.Err()
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
