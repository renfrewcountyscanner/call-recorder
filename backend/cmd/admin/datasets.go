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
	"math"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const datasetSchemaVersion = 2

type datasetItem struct {
	CallID, EffectiveText, ReceivedText, GeneratedText, EditedText string
	ReviewStatus, LabelSource, Reviewer, ReviewNotes               string
	Language, Provider, Model, Profile, ConversationGroup, Split   string
	AudioPath, AudioFormat                                         string
	AudioSize, DurationMS, SettingsVersion                         int64
	AudioSHA, TimedSegments                                        []byte
	Sender, Receiver, SystemID, SystemName, SiteID, SiteName       string
	TalkgroupID, TalkgroupName, RadioID, RadioName                 string
	StartTime                                                      time.Time
	ReviewedAt                                                     *time.Time
}

type datasetManifestRecord struct {
	SchemaVersion       int      `json:"schema_version"`
	ExportTime          string   `json:"export_time"`
	ExportType          string   `json:"export_type"`
	CallID              string   `json:"call_id"`
	ParentCallID        string   `json:"parent_call_id"`
	SegmentIndex        int      `json:"segment_index"`
	SegmentStart        float64  `json:"segment_start"`
	SegmentEnd          float64  `json:"segment_end"`
	AudioFile           string   `json:"audio_file"`
	AudioSHA256         string   `json:"audio_sha256"`
	SourceAudioSHA256   string   `json:"source_audio_sha256"`
	Codec               string   `json:"codec"`
	SampleRate          int      `json:"sample_rate"`
	Channels            int      `json:"channels"`
	DurationSeconds     float64  `json:"duration_seconds"`
	CallTimestamp       string   `json:"call_timestamp"`
	Sender              string   `json:"sender,omitempty"`
	Receiver            string   `json:"receiver,omitempty"`
	System              string   `json:"system"`
	SystemName          string   `json:"system_name,omitempty"`
	Site                string   `json:"site,omitempty"`
	SiteName            string   `json:"site_name,omitempty"`
	Talkgroup           string   `json:"talkgroup"`
	TalkgroupName       string   `json:"talkgroup_name,omitempty"`
	RadioID             string   `json:"radio_id"`
	RadioName           string   `json:"radio_name,omitempty"`
	Language            string   `json:"language,omitempty"`
	Provider            string   `json:"provider"`
	Model               string   `json:"model"`
	Profile             string   `json:"profile"`
	SettingsVersion     int64    `json:"transcription_settings_version"`
	ReceivedText        string   `json:"received_text"`
	GeneratedText       string   `json:"generated_text"`
	EditedText          string   `json:"edited_text"`
	EffectiveText       string   `json:"effective_text"`
	LabelSource         string   `json:"label_source"`
	ReviewStatus        string   `json:"review_status"`
	ReviewedAt          string   `json:"reviewed_at"`
	Reviewer            string   `json:"reviewer"`
	ReviewNotes         string   `json:"review_notes"`
	ConversationGroupID string   `json:"conversation_group_id"`
	Split               string   `json:"split"`
	ValidationWarnings  []string `json:"validation_warnings"`
}

type audioInfo struct {
	Codec      string
	SampleRate int
	Channels   int
	Duration   float64
}

type sampleRange struct{ Start, End float64 }

func datasetsCommand(pool *pgxpool.Pool, args []string) {
	if len(args) != 1 || args[0] != "run" {
		fatal(errors.New("usage: call-recorder-admin datasets run"))
	}
	audioRoot, exportRoot := strings.TrimSpace(os.Getenv("CALL_RECORDER_AUDIO_ROOT")), strings.TrimSpace(os.Getenv("CALL_RECORDER_EXPORT_ROOT"))
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
	err := pool.QueryRow(ctx, `WITH candidate AS (SELECT id FROM dataset_exports WHERE status='pending' OR (status='running' AND lease_expires_at<now()) ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1) UPDATE dataset_exports e SET status='running',started_at=coalesce(started_at,now()),lease_owner=$1,lease_expires_at=now()+interval '2 minutes',error=NULL,updated_at=now() FROM candidate c WHERE e.id=c.id RETURNING e.id`, worker).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	return id, err == nil, err
}

func loadDatasetItems(ctx context.Context, pool *pgxpool.Pool, id string) ([]datasetItem, string, error) {
	var exportType string
	if err := pool.QueryRow(ctx, `SELECT export_type FROM dataset_exports WHERE id=$1`, id).Scan(&exportType); err != nil {
		return nil, "", err
	}
	rows, err := pool.Query(ctx, `SELECT i.call_id,i.effective_text,coalesce(i.received_text,''),coalesce(i.generated_text,''),coalesce(i.edited_text,''),i.review_status,coalesce(i.label_source,''),coalesce(i.reviewer,''),coalesce(i.review_notes,''),coalesce(i.language,''),coalesce(i.provider,''),coalesce(i.model,''),coalesce(i.profile,''),coalesce(i.conversation_group_id,''),i.split,coalesce(i.settings_version,0),coalesce(i.timed_segments,'[]'::jsonb),i.reviewed_at,c.audio_path,c.audio_format,c.audio_size,c.audio_sha256,c.duration_ms,c.start_time,c.sender_id,coalesce(c.receiver_id,''),c.system_id,coalesce(c.system_name,''),coalesce(c.site_id,''),coalesce(c.site_name,''),c.talkgroup_id,coalesce(c.talkgroup_name,''),coalesce(c.radio_id,''),coalesce(c.radio_name,'') FROM dataset_export_items i JOIN calls c ON c.id=i.call_id WHERE i.export_id=$1 ORDER BY c.start_time,c.id`, id)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	items := []datasetItem{}
	for rows.Next() {
		var x datasetItem
		if err := rows.Scan(&x.CallID, &x.EffectiveText, &x.ReceivedText, &x.GeneratedText, &x.EditedText, &x.ReviewStatus, &x.LabelSource, &x.Reviewer, &x.ReviewNotes, &x.Language, &x.Provider, &x.Model, &x.Profile, &x.ConversationGroup, &x.Split, &x.SettingsVersion, &x.TimedSegments, &x.ReviewedAt, &x.AudioPath, &x.AudioFormat, &x.AudioSize, &x.AudioSHA, &x.DurationMS, &x.StartTime, &x.Sender, &x.Receiver, &x.SystemID, &x.SystemName, &x.SiteID, &x.SiteName, &x.TalkgroupID, &x.TalkgroupName, &x.RadioID, &x.RadioName); err != nil {
			return nil, "", err
		}
		items = append(items, x)
	}
	return items, exportType, rows.Err()
}

func buildDataset(ctx context.Context, pool *pgxpool.Pool, audioRoot, exportRoot, id string) error {
	items, exportType, err := loadDatasetItems(ctx, pool, id)
	if err != nil {
		return err
	}
	exportTime := time.Now().UTC().Format(time.RFC3339Nano)
	archive, err := os.CreateTemp(exportRoot, "dataset-*.zip.tmp")
	if err != nil {
		return err
	}
	archiveName := archive.Name()
	defer os.Remove(archiveName)
	manifest, err := os.CreateTemp(exportRoot, "manifest-*.jsonl.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(manifest.Name())
	errorsFile, err := os.CreateTemp(exportRoot, "errors-*.jsonl.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(errorsFile.Name())
	splitFiles := map[string]*os.File{}
	for _, name := range []string{"train", "validation", "test"} {
		f, e := os.CreateTemp(exportRoot, name+"-*.jsonl.tmp")
		if e != nil {
			return e
		}
		splitFiles[name] = f
		defer os.Remove(f.Name())
	}
	zw := zip.NewWriter(archive)
	readme, _ := zw.CreateHeader(&zip.FileHeader{Name: "README.md", Method: zip.Deflate})
	_, _ = io.WriteString(readme, "# Review-safe Whisper dataset\n\nSchema v2. Fine-tuning exports contain human-reviewed labels only. `manifest.jsonl` preserves all label sources and provenance. `train.jsonl`, `validation.jsonl`, and `test.jsonl` contain `{audio,text}` records. Splits are deterministic by five-minute conversation group. Calls over 30 seconds are silence/VAD segmented, never truncated. `validation_warnings` must be reviewed before training.\n")
	manifestEncoder, errorEncoder := json.NewEncoder(manifest), json.NewEncoder(errorsFile)
	splitEncoders := map[string]*json.Encoder{}
	for k, f := range splitFiles {
		splitEncoders[k] = json.NewEncoder(f)
	}
	processedCalls, exportedSamples, warningsCount := 0, 0, 0
	for _, item := range items {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var status string
		if err := pool.QueryRow(ctx, `SELECT status FROM dataset_exports WHERE id=$1`, id).Scan(&status); err != nil {
			return err
		}
		if status == "cancelled" {
			return nil
		}
		processedCalls++
		if _, err = pool.Exec(ctx, `UPDATE dataset_exports SET processed_items=$2,warning_count=$3,lease_expires_at=now()+interval '2 minutes',updated_at=now() WHERE id=$1 AND status='running'`, id, processedCalls, warningsCount); err != nil {
			return err
		}
		full := filepath.Join(audioRoot, item.AudioPath)
		if !strings.HasPrefix(filepath.Clean(full), filepath.Clean(audioRoot)+string(os.PathSeparator)) {
			warningsCount++
			_ = errorEncoder.Encode(map[string]any{"call_id": item.CallID, "error": "unsafe audio path"})
			continue
		}
		info, err := probeAudio(ctx, full)
		if err != nil || validateAudioDecode(ctx, full) != nil {
			warningsCount++
			_ = errorEncoder.Encode(map[string]any{"call_id": item.CallID, "error": "audio does not decode successfully"})
			continue
		}
		ranges := []sampleRange{{0, info.Duration}}
		if info.Duration > 30 {
			ranges, err = vadRanges(ctx, full, info.Duration)
			if err != nil {
				warningsCount++
				_ = errorEncoder.Encode(map[string]any{"call_id": item.CallID, "error": "VAD segmentation failed"})
				continue
			}
		}
		if exportType == "asr_finetune" && len(ranges) > 1 && !(item.LabelSource == "generated" && len(item.TimedSegments) > 2) {
			warningsCount++
			if err := errorEncoder.Encode(map[string]any{"call_id": item.CallID, "error": "long reviewed label excluded because segment-level transcript alignment is unavailable"}); err != nil {
				return err
			}
			continue
		}
		for index, rng := range ranges {
			generated := alignText(item.GeneratedText, item.TimedSegments, rng, info.Duration, item.LabelSource == "generated")
			received := alignText(item.ReceivedText, nil, rng, info.Duration, false)
			edited := alignText(item.EditedText, nil, rng, info.Duration, false)
			effective := alignText(item.EffectiveText, item.TimedSegments, rng, info.Duration, item.LabelSource == "generated")
			validation := validateTranscript(effective, rng.End-rng.Start, exportType)
			if len(ranges) > 1 && !(item.LabelSource == "generated" && len(item.TimedSegments) > 2) {
				validation = append(validation, "proportional_transcript_alignment")
			}
			if exportType == "asr_finetune" && strings.TrimSpace(effective) == "" {
				warningsCount++
				_ = errorEncoder.Encode(map[string]any{"call_id": item.CallID, "segment_index": index, "error": "empty transcript excluded"})
				continue
			}
			var samplePath, audioName string
			cleanup := func() {}
			if len(ranges) == 1 && info.Duration <= 30 {
				samplePath = full
				audioName = fmt.Sprintf("audio/%s/%s.%s", item.Split, item.CallID, safeExtension(item.AudioFormat))
			} else {
				f, e := os.CreateTemp(exportRoot, "segment-*.wav")
				if e != nil {
					return e
				}
				samplePath = f.Name()
				_ = f.Close()
				cleanup = func() { _ = os.Remove(samplePath) }
				if e = extractAudioSegment(ctx, full, samplePath, rng); e != nil {
					cleanup()
					warningsCount++
					_ = errorEncoder.Encode(map[string]any{"call_id": item.CallID, "segment_index": index, "error": "segment extraction failed"})
					continue
				}
				audioName = fmt.Sprintf("audio/%s/%s-%03d.wav", item.Split, item.CallID, index)
			}
			sampleInfo, e := probeAudio(ctx, samplePath)
			if e != nil || validateAudioDecode(ctx, samplePath) != nil {
				cleanup()
				warningsCount++
				_ = errorEncoder.Encode(map[string]any{"call_id": item.CallID, "segment_index": index, "error": "exported segment does not decode"})
				continue
			}
			hash, size, e := hashFile(samplePath)
			if e != nil {
				cleanup()
				return e
			}
			if e = addDiskFileToZipStored(zw, audioName, samplePath, item.StartTime); e != nil {
				cleanup()
				return e
			}
			cleanup()
			if len(validation) > 0 {
				warningsCount += len(validation)
			}
			reviewedAt := ""
			if item.ReviewedAt != nil {
				reviewedAt = item.ReviewedAt.UTC().Format(time.RFC3339Nano)
			}
			record := datasetManifestRecord{SchemaVersion: datasetSchemaVersion, ExportTime: exportTime, ExportType: exportType, CallID: item.CallID, ParentCallID: item.CallID, SegmentIndex: index, SegmentStart: roundMillis(rng.Start), SegmentEnd: roundMillis(rng.End), AudioFile: audioName, AudioSHA256: hex.EncodeToString(hash), SourceAudioSHA256: hex.EncodeToString(item.AudioSHA), Codec: sampleInfo.Codec, SampleRate: sampleInfo.SampleRate, Channels: sampleInfo.Channels, DurationSeconds: roundMillis(sampleInfo.Duration), CallTimestamp: item.StartTime.UTC().Format(time.RFC3339Nano), Sender: item.Sender, Receiver: item.Receiver, System: item.SystemID, SystemName: item.SystemName, Site: item.SiteID, SiteName: item.SiteName, Talkgroup: item.TalkgroupID, TalkgroupName: item.TalkgroupName, RadioID: item.RadioID, RadioName: item.RadioName, Language: item.Language, Provider: item.Provider, Model: item.Model, Profile: item.Profile, SettingsVersion: item.SettingsVersion, ReceivedText: received, GeneratedText: generated, EditedText: edited, EffectiveText: effective, LabelSource: item.LabelSource, ReviewStatus: item.ReviewStatus, ReviewedAt: reviewedAt, Reviewer: item.Reviewer, ReviewNotes: item.ReviewNotes, ConversationGroupID: item.ConversationGroup, Split: item.Split, ValidationWarnings: validation}
			if err := manifestEncoder.Encode(record); err != nil {
				return err
			}
			whisper := map[string]any{"audio": audioName, "text": effective, "call_id": item.CallID, "conversation_group_id": item.ConversationGroup, "split": item.Split}
			if exportType != "asr_finetune" {
				whisper["expected_text"] = effective
				whisper["review_status"] = item.ReviewStatus
			}
			if err := splitEncoders[item.Split].Encode(whisper); err != nil {
				return err
			}
			_ = size
			exportedSamples++
		}
		if _, err = pool.Exec(ctx, `UPDATE dataset_exports SET processed_items=$2,warning_count=$3,lease_expires_at=now()+interval '2 minutes',updated_at=now() WHERE id=$1 AND status='running'`, id, processedCalls, warningsCount); err != nil {
			return err
		}
	}
	if err := manifest.Close(); err != nil {
		return err
	}
	if err := errorsFile.Close(); err != nil {
		return err
	}
	for _, f := range splitFiles {
		if err := f.Close(); err != nil {
			return err
		}
	}
	if err := addDiskFileToZip(zw, "manifest.jsonl", manifest.Name()); err != nil {
		return err
	}
	if warningsCount > 0 {
		if err := addDiskFileToZip(zw, "errors.jsonl", errorsFile.Name()); err != nil {
			return err
		}
	}
	for _, name := range []string{"train", "validation", "test"} {
		if err := addDiskFileToZip(zw, name+".jsonl", splitFiles[name].Name()); err != nil {
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
	hash, size, err := hashFile(archiveName)
	if err != nil {
		return err
	}
	finalRel := "dataset-" + id + ".zip"
	final := filepath.Join(exportRoot, finalRel)
	if err := os.Rename(archiveName, final); err != nil {
		return err
	}
	finalStatus := "completed"
	if warningsCount > 0 {
		finalStatus = "completed_with_warnings"
	}
	_, err = pool.Exec(ctx, `UPDATE dataset_exports SET status=$2,processed_items=$3,warning_count=$4,output_path=$5,output_size=$6,output_sha256=$7,completed_at=now(),lease_owner=NULL,lease_expires_at=NULL,updated_at=now() WHERE id=$1 AND status='running'`, id, finalStatus, processedCalls, warningsCount, finalRel, size, hash)
	if err != nil {
		_ = os.Remove(final)
		return err
	}
	fmt.Printf("dataset=%s status=%s calls=%d samples=%d warnings=%d bytes=%d\n", id, finalStatus, processedCalls, exportedSamples, warningsCount, size)
	return nil
}

func probeAudio(ctx context.Context, path string) (audioInfo, error) {
	c, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	raw, err := exec.CommandContext(c, "ffprobe", "-v", "error", "-show_entries", "stream=codec_name,sample_rate,channels", "-show_entries", "format=duration", "-of", "json", path).Output()
	if err != nil {
		return audioInfo{}, err
	}
	var v struct {
		Streams []struct {
			Codec      string `json:"codec_name"`
			SampleRate string `json:"sample_rate"`
			Channels   int    `json:"channels"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if json.Unmarshal(raw, &v) != nil || len(v.Streams) == 0 {
		return audioInfo{}, errors.New("no audio stream")
	}
	rate, _ := strconv.Atoi(v.Streams[0].SampleRate)
	dur, _ := strconv.ParseFloat(v.Format.Duration, 64)
	if dur <= 0 {
		return audioInfo{}, errors.New("invalid duration")
	}
	return audioInfo{Codec: v.Streams[0].Codec, SampleRate: rate, Channels: v.Streams[0].Channels, Duration: dur}, nil
}

func validateAudioDecode(ctx context.Context, path string) error {
	c, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	return exec.CommandContext(c, "ffmpeg", "-v", "error", "-i", path, "-f", "null", "-").Run()
}

var silencePattern = regexp.MustCompile(`silence_(start|end):\s*([0-9.]+)`)

func vadRanges(ctx context.Context, path string, duration float64) ([]sampleRange, error) {
	c, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	raw, err := exec.CommandContext(c, "ffmpeg", "-hide_banner", "-i", path, "-af", "silencedetect=noise=-35dB:d=0.35", "-f", "null", "-").CombinedOutput()
	if err != nil {
		return nil, err
	}
	matches := silencePattern.FindAllStringSubmatch(string(raw), -1)
	starts := []float64{}
	breaks := []float64{}
	for _, m := range matches {
		v, _ := strconv.ParseFloat(m[2], 64)
		if m[1] == "start" {
			starts = append(starts, v)
		} else if len(starts) > 0 {
			breaks = append(breaks, (starts[len(starts)-1]+v)/2)
		}
	}
	sort.Float64s(breaks)
	ranges := []sampleRange{}
	cursor := 0.0
	for duration-cursor > 30 {
		cut := cursor + 30
		for _, b := range breaks {
			if b > cursor+3 && b <= cursor+30 {
				cut = b
			}
		}
		if cut <= cursor {
			cut = math.Min(cursor+30, duration)
		}
		ranges = append(ranges, sampleRange{cursor, cut})
		cursor = cut
	}
	if duration-cursor > .05 {
		ranges = append(ranges, sampleRange{cursor, duration})
	}
	return ranges, nil
}

func extractAudioSegment(ctx context.Context, input, output string, r sampleRange) error {
	c, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	return exec.CommandContext(c, "ffmpeg", "-v", "error", "-ss", fmt.Sprintf("%.3f", r.Start), "-t", fmt.Sprintf("%.3f", r.End-r.Start), "-i", input, "-vn", "-ac", "1", "-ar", "16000", "-c:a", "pcm_s16le", "-y", output).Run()
}

func alignText(text string, timed []byte, r sampleRange, total float64, useTimed bool) string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return ""
	}
	if useTimed && len(timed) > 2 {
		var segs []struct {
			Start, End float64
			Text       string
		}
		if json.Unmarshal(timed, &segs) == nil {
			parts := []string{}
			for _, s := range segs {
				if s.End > r.Start && s.Start < r.End {
					parts = append(parts, strings.TrimSpace(s.Text))
				}
			}
			if joined := strings.TrimSpace(strings.Join(parts, " ")); joined != "" {
				return joined
			}
		}
	}
	start := int(math.Floor(r.Start / total * float64(len(words))))
	end := int(math.Ceil(r.End / total * float64(len(words))))
	if start < 0 {
		start = 0
	}
	if end > len(words) {
		end = len(words)
	}
	if end < start {
		end = start
	}
	return strings.Join(words[start:end], " ")
}

func validateTranscript(text string, duration float64, exportType string) []string {
	warnings := []string{}
	n := len([]rune(strings.TrimSpace(text)))
	if n == 0 && exportType == "asr_finetune" {
		warnings = append(warnings, "empty_transcript")
	}
	if duration > 0 && n > 0 {
		rate := float64(n) / duration
		if rate < 0.25 {
			warnings = append(warnings, "very_low_text_to_duration_ratio")
		}
		if rate > 25 {
			warnings = append(warnings, "very_high_text_to_duration_ratio")
		}
	}
	words := strings.Fields(strings.ToLower(text))
	if len(words) >= 8 {
		unique := map[string]bool{}
		for _, w := range words {
			unique[w] = true
		}
		if float64(len(unique))/float64(len(words)) < .25 {
			warnings = append(warnings, "repeated_text_hallucination_risk")
		}
		for size := 2; size <= 5; size++ {
			for i := 0; i+size*3 <= len(words); i++ {
				a := strings.Join(words[i:i+size], " ")
				if a == strings.Join(words[i+size:i+size*2], " ") && a == strings.Join(words[i+size*2:i+size*3], " ") {
					warnings = append(warnings, "consecutive_repeated_phrase")
					return warnings
				}
			}
		}
	}
	return warnings
}

func hashFile(path string) ([]byte, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return h.Sum(nil), n, err
}
func roundMillis(v float64) float64 { return math.Round(v*1000) / 1000 }
func safeExtension(v string) string {
	v = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(v), "."))
	if regexp.MustCompile(`^[a-z0-9]{1,8}$`).MatchString(v) {
		return v
	}
	return "audio"
}
func addDiskFileToZipStored(zw *zip.Writer, name, path string, modified time.Time) error {
	input, err := os.Open(path)
	if err != nil {
		return err
	}
	defer input.Close()
	h := &zip.FileHeader{Name: name, Method: zip.Store}
	h.SetMode(0600)
	h.Modified = modified
	entry, err := zw.CreateHeader(h)
	if err != nil {
		return err
	}
	_, err = io.Copy(entry, input)
	return err
}
func addDiskFileToZip(zw *zip.Writer, name, path string) error {
	input, err := os.Open(path)
	if err != nil {
		return err
	}
	defer input.Close()
	h := &zip.FileHeader{Name: name, Method: zip.Deflate}
	h.SetMode(0600)
	entry, err := zw.CreateHeader(h)
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
		var x expired
		if err := rows.Scan(&x.id, &x.path); err != nil {
			return err
		}
		items = append(items, x)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, x := range items {
		if x.path != "" {
			full := filepath.Join(exportRoot, x.path)
			if strings.HasPrefix(filepath.Clean(full), filepath.Clean(exportRoot)+string(os.PathSeparator)) {
				if err := os.Remove(full); err != nil && !errors.Is(err, os.ErrNotExist) {
					return err
				}
			}
		}
		if _, err := pool.Exec(ctx, `UPDATE dataset_exports SET status='expired',output_path=NULL,output_size=NULL,output_sha256=NULL,updated_at=now() WHERE id=$1`, x.id); err != nil {
			return err
		}
	}
	return nil
}
func sanitizeDatasetError(err error) string {
	if err == nil {
		return ""
	}
	v := strings.TrimSpace(err.Error())
	if strings.Contains(v, "/") {
		v = "dataset export file operation failed"
	}
	if len(v) > 500 {
		v = v[:500]
	}
	return v
}
