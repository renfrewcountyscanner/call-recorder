package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *server) requestIdentity(r *http.Request) string {
	if value := strings.TrimSpace(r.Header.Get("X-Call-Recorder-User")); value != "" {
		return value
	}
	if value := strings.TrimSpace(r.Header.Get("Cf-Access-Authenticated-User-Email")); value != "" {
		return value
	}
	return "admin-token"
}

func (s *server) recordAudit(ctx context.Context, r *http.Request, action, targetType, targetID string, details any) {
	raw, err := json.Marshal(details)
	if err != nil {
		raw = []byte(`{}`)
	}
	_, err = s.db.Exec(ctx, `INSERT INTO audit_events(actor,action,target_type,target_id,request_id,details) VALUES($1,$2,$3,NULLIF($4,''),NULLIF($5,''),$6)`,
		s.requestIdentity(r), action, targetType, targetID, r.Header.Get("X-Request-ID"), raw)
	if err != nil {
		s.logger.Error("audit event failed", "action", action, "target_type", targetType, "target_id", targetID, "error", err)
	}
}

func receiverStatusForm(r *http.Request) (sender, receiver, system, site string, err error) {
	if err = r.ParseForm(); err != nil {
		return
	}
	sender = strings.TrimSpace(r.FormValue("sender"))
	receiver = strings.TrimSpace(r.FormValue("receiver"))
	system = strings.TrimSpace(r.FormValue("system"))
	site = strings.TrimSpace(r.FormValue("site"))
	if sender == "" || system == "" || len(sender) > 100 || len(receiver) > 240 || len(system) > 240 || len(site) > 240 {
		err = errors.New("invalid receiver status identity")
	}
	return
}

func receiverStatusID(sender, receiver, system, site string) string {
	return sender + "/" + receiver + "/" + system + "/" + site
}

func (s *server) adminDismissReceiverStatus(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	sender, receiver, system, site, err := receiverStatusForm(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	staleMinutes := 15
	_ = s.db.QueryRow(r.Context(), `SELECT (setting_value #>> '{}')::int FROM application_settings WHERE setting_key='receiver_stale_minutes'`).Scan(&staleMinutes)
	actor := s.requestIdentity(r)
	tag, err := s.db.Exec(r.Context(), `UPDATE receiver_status_entries SET dismissed_at=now(),dismissed_by=$5,dismissed_last_call_at=last_call_at,updated_at=now()
		WHERE sender_id=$1 AND receiver_id=$2 AND system_id=$3 AND site_id=$4
		AND dismissed_at IS NULL AND last_call_at < now()-($6::int*interval '1 minute')`, sender, receiver, system, site, actor, staleMinutes)
	if err != nil {
		s.internal(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		http.Error(w, "receiver is active, already dismissed, or no longer exists", http.StatusConflict)
		return
	}
	target := receiverStatusID(sender, receiver, system, site)
	s.recordAudit(r.Context(), r, "receiver.dismiss", "receiver_status", target, map[string]any{"stale_minutes": staleMinutes})
	http.Redirect(w, r, "/status?dismissed=1", http.StatusSeeOther)
}

func (s *server) adminRestoreReceiverStatus(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	sender, receiver, system, site, err := receiverStatusForm(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	tag, err := s.db.Exec(r.Context(), `UPDATE receiver_status_entries SET dismissed_at=NULL,dismissed_by=NULL,dismissed_last_call_at=NULL,updated_at=now()
		WHERE sender_id=$1 AND receiver_id=$2 AND system_id=$3 AND site_id=$4 AND dismissed_at IS NOT NULL`, sender, receiver, system, site)
	if err != nil {
		s.internal(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		http.Error(w, "dismissed receiver not found", http.StatusNotFound)
		return
	}
	target := receiverStatusID(sender, receiver, system, site)
	s.recordAudit(r.Context(), r, "receiver.restore", "receiver_status", target, map[string]any{})
	http.Redirect(w, r, "/status?restored=1", http.StatusSeeOther)
}

func (s *server) adminReceiverStatusSettings(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	minutes, err := strconv.Atoi(strings.TrimSpace(r.FormValue("stale_minutes")))
	if err != nil || minutes < 1 || minutes > 1440 {
		http.Error(w, "stale threshold must be between 1 and 1440 minutes", http.StatusBadRequest)
		return
	}
	actor := s.requestIdentity(r)
	_, err = s.db.Exec(r.Context(), `INSERT INTO application_settings(setting_key,setting_value,updated_by) VALUES('receiver_stale_minutes',to_jsonb($1::int),$2)
		ON CONFLICT(setting_key) DO UPDATE SET setting_value=EXCLUDED.setting_value,updated_by=EXCLUDED.updated_by,updated_at=now()`, minutes, actor)
	if err != nil {
		s.internal(w, err)
		return
	}
	s.recordAudit(r.Context(), r, "receiver.settings", "application_setting", "receiver_stale_minutes", map[string]any{"minutes": minutes})
	http.Redirect(w, r, "/status?saved=1", http.StatusSeeOther)
}

type datasetRow struct {
	ID, RequestedBy, Status, Error, SHA256 string
	Total, Processed, Warnings             int
	EstimatedBytes, OutputSize             int64
	CreatedAt, ExpiresAt                   time.Time
	CompletedAt                            *time.Time
}

func (s *server) adminDatasets(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	rows, err := s.db.Query(r.Context(), `SELECT id,requested_by,status,total_items,processed_items,warning_count,estimated_bytes,coalesce(output_size,0),coalesce(encode(output_sha256,'hex'),''),coalesce(error,''),created_at,expires_at,completed_at
		FROM dataset_exports ORDER BY created_at DESC LIMIT 100`)
	if err != nil {
		s.internal(w, err)
		return
	}
	defer rows.Close()
	items := []datasetRow{}
	for rows.Next() {
		var item datasetRow
		if err := rows.Scan(&item.ID, &item.RequestedBy, &item.Status, &item.Total, &item.Processed, &item.Warnings, &item.EstimatedBytes, &item.OutputSize, &item.SHA256, &item.Error, &item.CreatedAt, &item.ExpiresAt, &item.CompletedAt); err != nil {
			s.internal(w, err)
			return
		}
		items = append(items, item)
	}
	s.page(w, r, "admin_datasets.html", "Training datasets", "transcription", map[string]any{"Exports": items})
}

type datasetFilters struct {
	From      string `json:"from,omitempty"`
	To        string `json:"to,omitempty"`
	Sender    string `json:"sender,omitempty"`
	System    string `json:"system,omitempty"`
	Talkgroup string `json:"talkgroup,omitempty"`
	Language  string `json:"language,omitempty"`
	Provider  string `json:"provider,omitempty"`
	Review    string `json:"review_status,omitempty"`
}

func (s *server) adminCreateDataset(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	f := datasetFilters{
		From: strings.TrimSpace(r.FormValue("from")), To: strings.TrimSpace(r.FormValue("to")),
		Sender: strings.TrimSpace(r.FormValue("sender")), System: strings.TrimSpace(r.FormValue("system")),
		Talkgroup: strings.TrimSpace(r.FormValue("talkgroup")), Language: strings.TrimSpace(r.FormValue("language")),
		Provider: strings.TrimSpace(r.FormValue("provider")), Review: strings.TrimSpace(r.FormValue("review_status")),
	}
	for _, date := range []string{f.From, f.To} {
		if date != "" {
			if _, err := time.Parse("2006-01-02", date); err != nil {
				http.Error(w, "dates must use YYYY-MM-DD", http.StatusBadRequest)
				return
			}
		}
	}
	if f.Review != "" && f.Review != "unreviewed" && f.Review != "reviewed" && f.Review != "rejected" && f.Review != "needs_review" {
		http.Error(w, "invalid review status", http.StatusBadRequest)
		return
	}
	id, err := randomToken()
	if err != nil {
		s.internal(w, err)
		return
	}
	rawFilters, _ := json.Marshal(f)
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.internal(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	_, err = tx.Exec(r.Context(), `INSERT INTO dataset_exports(id,requested_by,filters) VALUES($1,$2,$3)`, id, s.requestIdentity(r), rawFilters)
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO dataset_export_items(export_id,call_id,transcript_id,effective_text,received_text,generated_text,edited_text,review_status,language,provider,model,split)
			SELECT $1,c.id,t.id,coalesce(NULLIF(t.edited_text,''),NULLIF(t.text,''),NULLIF(c.transcript,'')),NULLIF(c.transcript,''),NULLIF(t.text,''),NULLIF(t.edited_text,''),coalesce(t.review_status,'unreviewed'),t.language,t.provider,t.model,
			CASE WHEN mod(abs(hashtext(c.id)::bigint),100)<90 THEN 'train' WHEN mod(abs(hashtext(c.id)::bigint),100)<95 THEN 'validation' ELSE 'test' END
			FROM calls c LEFT JOIN LATERAL (SELECT * FROM transcripts x WHERE x.call_id=c.id ORDER BY x.updated_at DESC LIMIT 1) t ON true
			WHERE coalesce(NULLIF(t.edited_text,''),NULLIF(t.text,''),NULLIF(c.transcript,'')) IS NOT NULL
			AND ($2='' OR c.start_time::date >= $2::date) AND ($3='' OR c.start_time::date <= $3::date)
			AND ($4='' OR c.sender_id=$4) AND ($5='' OR c.system_id=$5) AND ($6='' OR c.talkgroup_id=$6)
			AND ($7='' OR coalesce(t.language,'')=$7) AND ($8='' OR coalesce(t.provider,'')=$8) AND ($9='' OR coalesce(t.review_status,'unreviewed')=$9)`,
			id, f.From, f.To, f.Sender, f.System, f.Talkgroup, f.Language, f.Provider, f.Review)
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `UPDATE dataset_exports e SET total_items=x.n,estimated_bytes=x.bytes,updated_at=now()
			FROM (SELECT count(*)::int n,coalesce(sum(c.audio_size),0)::bigint bytes FROM dataset_export_items i JOIN calls c ON c.id=i.call_id WHERE i.export_id=$1) x WHERE e.id=$1`, id)
	}
	if err == nil {
		var itemCount int
		err = tx.QueryRow(r.Context(), `SELECT total_items FROM dataset_exports WHERE id=$1`, id).Scan(&itemCount)
		if err == nil && itemCount == 0 {
			_ = tx.Rollback(r.Context())
			http.Error(w, "no transcribed calls match those filters", http.StatusBadRequest)
			return
		}
	}
	if err != nil {
		s.internal(w, err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		s.internal(w, err)
		return
	}
	s.recordAudit(r.Context(), r, "dataset.create", "dataset_export", id, f)
	http.Redirect(w, r, "/admin/datasets?created=1", http.StatusSeeOther)
}

func (s *server) adminCancelDataset(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	id := r.PathValue("id")
	tag, err := s.db.Exec(r.Context(), `UPDATE dataset_exports SET status='cancelled',lease_expires_at=NULL,updated_at=now() WHERE id=$1 AND status IN ('pending','running')`, id)
	if err != nil {
		s.internal(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		http.Error(w, "active dataset export not found", http.StatusNotFound)
		return
	}
	s.recordAudit(r.Context(), r, "dataset.cancel", "dataset_export", id, map[string]any{})
	http.Redirect(w, r, "/admin/datasets", http.StatusSeeOther)
}

func (s *server) adminDeleteDataset(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	id := r.PathValue("id")
	var rel string
	err := s.db.QueryRow(r.Context(), `SELECT coalesce(output_path,'') FROM dataset_exports WHERE id=$1 AND status NOT IN ('pending','running')`, id).Scan(&rel)
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "completed dataset export not found", http.StatusNotFound)
		return
	}
	if err != nil {
		s.internal(w, err)
		return
	}
	if rel != "" {
		full := filepath.Join(s.cfg.ExportRoot, rel)
		if strings.HasPrefix(filepath.Clean(full), filepath.Clean(s.cfg.ExportRoot)+string(os.PathSeparator)) {
			if err := os.Remove(full); err != nil && !errors.Is(err, os.ErrNotExist) {
				s.internal(w, err)
				return
			}
		}
	}
	if _, err := s.db.Exec(r.Context(), `DELETE FROM dataset_exports WHERE id=$1`, id); err != nil {
		s.internal(w, err)
		return
	}
	s.recordAudit(r.Context(), r, "dataset.delete", "dataset_export", id, map[string]any{})
	http.Redirect(w, r, "/admin/datasets", http.StatusSeeOther)
}

func (s *server) adminDownloadDataset(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	id := r.PathValue("id")
	var rel, status string
	var expires time.Time
	err := s.db.QueryRow(r.Context(), `SELECT coalesce(output_path,''),status,expires_at FROM dataset_exports WHERE id=$1`, id).Scan(&rel, &status, &expires)
	if err != nil || rel == "" || (status != "completed" && status != "completed_with_warnings") || time.Now().After(expires) {
		http.NotFound(w, r)
		return
	}
	full := filepath.Join(s.cfg.ExportRoot, rel)
	if !strings.HasPrefix(filepath.Clean(full), filepath.Clean(s.cfg.ExportRoot)+string(os.PathSeparator)) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="dataset-%s.zip"`, id))
	http.ServeFile(w, r, full)
}
