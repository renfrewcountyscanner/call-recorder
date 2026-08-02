package main

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type datasetItem struct {
	CallID, EffectiveText, ReceivedText, GeneratedText, EditedText string
	ReviewStatus, Language, Provider, Model, Split                 string
	AudioPath, AudioFormat                                         string
	AudioSize, DurationMS                                          int64
	AudioSHA                                                       []byte
	Sender, Receiver, SystemID, SystemName, SiteID, SiteName       string
	TalkgroupID, TalkgroupName, RadioID, RadioName                 string
	StartTime                                                      time.Time
}

type datasetManifestRecord struct {
	SchemaVersion int    `json:"schema_version"`
	CallID        string `json:"call_id"`
	AudioFile     string `json:"audio_file"`
	AudioSHA256   string `json:"audio_sha256"`
	AudioFormat   string `json:"audio_format"`
	AudioBytes    int64  `json:"audio_bytes"`
	DurationMS    int64  `json:"duration_ms"`
	StartTime     string `json:"start_time"`
	Sender        string `json:"sender"`
	Receiver      string `json:"receiver,omitempty"`
	SystemID      string `json:"system_id"`
	SystemName    string `json:"system_name,omitempty"`
	SiteID        string `json:"site_id,omitempty"`
	SiteName      string `json:"site_name,omitempty"`
	TalkgroupID   string `json:"talkgroup_id"`
	TalkgroupName string `json:"talkgroup_name,omitempty"`
	RadioID       string `json:"radio_id,omitempty"`
	RadioName     string `json:"radio_name,omitempty"`
	Language      string `json:"language,omitempty"`
	Provider      string `json:"provider,omitempty"`
	Model         string `json:"model,omitempty"`
	ReceivedText  string `json:"received_text,omitempty"`
	GeneratedText string `json:"generated_text,omitempty"`
	EditedText    string `json:"edited_text,omitempty"`
	EffectiveText string `json:"effective_text"`
	ReviewStatus  string `json:"review_status"`
	Split         string `json:"split"`
}

func datasetsCommand(pool *pgxpool.Pool, args []string) {
	if len(args) != 1 || args[0] != "run" {
		fatal(errors.New("usage: call-recorder-admin datasets run"))
	}
	audioRoot := strings.TrimSpace(os.Getenv("CALL_RECORDER_AUDIO_ROOT"))
	exportRoot := strings.TrimSpace(os.Getenv("CALL_RECORDER_EXPORT_ROOT"))
	if audioRoot == "" || exportRoot == "" {
		fatal(errors.New("CALL_RECORDER_AUDIO_ROOT and CALL_RECORDER_EXPORT_ROOT are required"))
	}
	if err := os.MkdirAll(exportRoot, 0750); err != nil {
		fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		if err := expireDatasets(ctx, pool, exportRoot); err != nil && !errors.Is(err, context.Canceled) {
			fmt.Fprintln(os.Stderr, "dataset cleanup:", err)
		}
		id, ok, err := claimDataset(ctx, pool)
		if err != nil && !errors.Is(err, context.Canceled) {
			fmt.Fprintln(os.Stderr, "dataset claim:", err)
		} else if ok {
			if err := buildDataset(ctx, pool, audioRoot, exportRoot, id); err != nil && !errors.Is(err, context.Canceled) {
				safe := sanitizeDatasetError(err)
				_, _ = pool.Exec(context.Background(), `UPDATE dataset_exports SET status=CASE WHEN status='cancelled' THEN status ELSE 'failed' END,error=$2,lease_owner=NULL,lease_expires_at=NULL,updated_at=now() WHERE id=$1`, id, safe)
				fmt.Fprintf(os.Stderr, "dataset=%s status=failed error=%s\n", id, safe)
			}
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func claimDataset(ctx context.Context, pool *pgxpool.Pool) (string, bool, error) {
	worker := fmt.Sprintf("dataset-worker-%d", os.Getpid())
	var id string
	err := pool.QueryRow(ctx, `WITH candidate AS (
		SELECT id FROM dataset_exports WHERE status='pending' OR (status='running' AND lease_expires_at<now()) ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1
	) UPDATE dataset_exports e SET status='running',started_at=coalesce(started_at,now()),lease_owner=$1,lease_expires_at=now()+interval '2 minutes',error=NULL,updated_at=now()
	FROM candidate c WHERE e.id=c.id RETURNING e.id`, worker).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	return id, err == nil, err
}

func loadDatasetItems(ctx context.Context, pool *pgxpool.Pool, id string) ([]datasetItem, error) {
	rows, err := pool.Query(ctx, `SELECT i.call_id,i.effective_text,coalesce(i.received_text,''),coalesce(i.generated_text,''),coalesce(i.edited_text,''),i.review_status,coalesce(i.language,''),coalesce(i.provider,''),coalesce(i.model,''),i.split,
		c.audio_path,c.audio_format,c.audio_size,c.audio_sha256,c.duration_ms,c.start_time,c.sender_id,coalesce(c.receiver_id,''),c.system_id,coalesce(c.system_name,''),coalesce(c.site_id,''),coalesce(c.site_name,''),c.talkgroup_id,coalesce(c.talkgroup_name,''),coalesce(c.radio_id,''),coalesce(c.radio_name,'')
		FROM dataset_export_items i JOIN calls c ON c.id=i.call_id WHERE i.export_id=$1 ORDER BY c.start_time,c.id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []datasetItem{}
	for rows.Next() {
		var item datasetItem
		if err := rows.Scan(&item.CallID, &item.EffectiveText, &item.ReceivedText, &item.GeneratedText, &item.EditedText, &item.ReviewStatus, &item.Language, &item.Provider, &item.Model, &item.Split,
			&item.AudioPath, &item.AudioFormat, &item.AudioSize, &item.AudioSHA, &item.DurationMS, &item.StartTime, &item.Sender, &item.Receiver, &item.SystemID, &item.SystemName, &item.SiteID, &item.SiteName, &item.TalkgroupID, &item.TalkgroupName, &item.RadioID, &item.RadioName); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func buildDataset(ctx context.Context, pool *pgxpool.Pool, audioRoot, exportRoot, id string) error {
	items, err := loadDatasetItems(ctx, pool, id)
	if err != nil {
		return err
	}
	archive, err := os.CreateTemp(exportRoot, "dataset-*.zip.tmp")
	if err != nil {
		return err
	}
	archiveName := archive.Name()
	defer archive.Close()
	defer os.Remove(archiveName)
	manifest, err := os.CreateTemp(exportRoot, "manifest-*.jsonl.tmp")
	if err != nil {
		archive.Close()
		return err
	}
	manifestName := manifest.Name()
	defer manifest.Close()
	defer os.Remove(manifestName)
	warnings, err := os.CreateTemp(exportRoot, "errors-*.jsonl.tmp")
	if err != nil {
		archive.Close()
		manifest.Close()
		return err
	}
	warningsName := warnings.Name()
	defer warnings.Close()
	defer os.Remove(warningsName)

	zw := zip.NewWriter(archive)
	readme, _ := zw.CreateHeader(&zip.FileHeader{Name: "README.md", Method: zip.Deflate})
	_, _ = io.WriteString(readme, "# Call Recorder speech-to-text dataset\n\n`manifest.jsonl` contains one record per included original audio file. `effective_text` prefers edited, then generated, then received text. The deterministic split is 90% train, 5% validation, and 5% test. Review unreviewed transcripts before using them as ground truth.\n")
	manifestEncoder := json.NewEncoder(manifest)
	warningEncoder := json.NewEncoder(warnings)
	processed, warningCount := 0, 0
	for _, item := range items {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		var status string
		if err := pool.QueryRow(ctx, `SELECT status FROM dataset_exports WHERE id=$1`, id).Scan(&status); err != nil {
			return err
		}
		if status == "cancelled" {
			return nil
		}
		full := filepath.Join(audioRoot, item.AudioPath)
		if !strings.HasPrefix(filepath.Clean(full), filepath.Clean(audioRoot)+string(os.PathSeparator)) {
			warningCount++
			_ = warningEncoder.Encode(map[string]any{"call_id": item.CallID, "error": "unsafe audio path"})
			continue
		}
		file, err := os.Open(full)
		if err != nil {
			warningCount++
			_ = warningEncoder.Encode(map[string]any{"call_id": item.CallID, "error": "audio unavailable"})
			continue
		}
		audioName := "audio/" + item.CallID + "." + item.AudioFormat
		header := &zip.FileHeader{Name: audioName, Method: zip.Store}
		header.SetMode(0600)
		header.Modified = item.StartTime
		entry, createErr := zw.CreateHeader(header)
		if createErr == nil {
			_, createErr = io.Copy(entry, file)
		}
		_ = file.Close()
		if createErr != nil {
			return createErr
		}
		record := datasetManifestRecord{SchemaVersion: 1, CallID: item.CallID, AudioFile: audioName, AudioSHA256: hex.EncodeToString(item.AudioSHA), AudioFormat: item.AudioFormat, AudioBytes: item.AudioSize, DurationMS: item.DurationMS, StartTime: item.StartTime.UTC().Format(time.RFC3339Nano), Sender: item.Sender, Receiver: item.Receiver, SystemID: item.SystemID, SystemName: item.SystemName, SiteID: item.SiteID, SiteName: item.SiteName, TalkgroupID: item.TalkgroupID, TalkgroupName: item.TalkgroupName, RadioID: item.RadioID, RadioName: item.RadioName, Language: item.Language, Provider: item.Provider, Model: item.Model, ReceivedText: item.ReceivedText, GeneratedText: item.GeneratedText, EditedText: item.EditedText, EffectiveText: item.EffectiveText, ReviewStatus: item.ReviewStatus, Split: item.Split}
		if err := manifestEncoder.Encode(record); err != nil {
			return err
		}
		processed++
		_, err = pool.Exec(ctx, `UPDATE dataset_exports SET processed_items=$2,warning_count=$3,lease_expires_at=now()+interval '2 minutes',updated_at=now() WHERE id=$1 AND status='running'`, id, processed, warningCount)
		if err != nil {
			return err
		}
	}
	if err := manifest.Close(); err != nil {
		return err
	}
	if err := warnings.Close(); err != nil {
		return err
	}
	if err := addDiskFileToZip(zw, "manifest.jsonl", manifestName); err != nil {
		return err
	}
	if warningCount > 0 {
		if err := addDiskFileToZip(zw, "errors.jsonl", warningsName); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	if err := archive.Sync(); err != nil {
		return err
	}
	if err := archive.Close(); err != nil {
		return err
	}
	input, err := os.Open(archiveName)
	if err != nil {
		return err
	}
	hash := sha256.New()
	size, err := io.Copy(hash, input)
	_ = input.Close()
	if err != nil {
		return err
	}
	finalRel := "dataset-" + id + ".zip"
	final := filepath.Join(exportRoot, finalRel)
	if err := os.Rename(archiveName, final); err != nil {
		return err
	}
	status := "completed"
	if warningCount > 0 {
		status = "completed_with_warnings"
	}
	_, err = pool.Exec(ctx, `UPDATE dataset_exports SET status=$2,processed_items=$3,warning_count=$4,output_path=$5,output_size=$6,output_sha256=$7,completed_at=now(),lease_owner=NULL,lease_expires_at=NULL,updated_at=now() WHERE id=$1 AND status='running'`, id, status, processed, warningCount, finalRel, size, hash.Sum(nil))
	if err != nil {
		_ = os.Remove(final)
		return err
	}
	fmt.Printf("dataset=%s status=%s items=%d warnings=%d bytes=%d\n", id, status, processed, warningCount, size)
	return nil
}

func addDiskFileToZip(zw *zip.Writer, name, path string) error {
	input, err := os.Open(path)
	if err != nil {
		return err
	}
	defer input.Close()
	header := &zip.FileHeader{Name: name, Method: zip.Deflate}
	header.SetMode(0600)
	entry, err := zw.CreateHeader(header)
	if err != nil {
		return err
	}
	_, err = io.Copy(entry, input)
	return err
}

func expireDatasets(ctx context.Context, pool *pgxpool.Pool, exportRoot string) error {
	rows, err := pool.Query(ctx, `SELECT id,coalesce(output_path,'') FROM dataset_exports WHERE expires_at<=now() AND status IN ('completed','completed_with_warnings','failed','cancelled')`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type expired struct{ id, path string }
	var items []expired
	for rows.Next() {
		var item expired
		if err := rows.Scan(&item.id, &item.path); err != nil {
			return err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, item := range items {
		if item.path != "" {
			full := filepath.Join(exportRoot, item.path)
			if strings.HasPrefix(filepath.Clean(full), filepath.Clean(exportRoot)+string(os.PathSeparator)) {
				if err := os.Remove(full); err != nil && !errors.Is(err, os.ErrNotExist) {
					return err
				}
			}
		}
		if _, err := pool.Exec(ctx, `UPDATE dataset_exports SET status='expired',output_path=NULL,output_size=NULL,output_sha256=NULL,updated_at=now() WHERE id=$1`, item.id); err != nil {
			return err
		}
	}
	return nil
}

func sanitizeDatasetError(err error) string {
	if err == nil {
		return ""
	}
	value := strings.TrimSpace(err.Error())
	// Export paths can reveal host layout. Operators only need the basename.
	if strings.Contains(value, "/") {
		value = "dataset export file operation failed"
	}
	if len(value) > 500 {
		value = value[:500]
	}
	return value
}
