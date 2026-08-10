package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type spoolItem struct {
	ID          string              `json:"id"`
	SourcePath  string              `json:"source_path"`
	Fingerprint string              `json:"fingerprint"`
	AudioFile   string              `json:"audio_file"`
	Request     createUploadRequest `json:"request"`
	Attempts    int                 `json:"attempts"`
	NextAttempt time.Time           `json:"next_attempt"`
	LastError   string              `json:"last_error,omitempty"`
	CreatedAt   time.Time           `json:"created_at"`
	directory   string
}

type completedRecord struct {
	Fingerprint string    `json:"fingerprint"`
	SourcePath  string    `json:"source_path"`
	CompletedAt time.Time `json:"completed_at"`
}

type durableSpool struct {
	root      string
	mu        sync.Mutex
	items     map[string]*spoolItem
	known     map[string]bool
	inFlight  map[string]bool
	processed *os.File
}

func openDurableSpool(root string) (*durableSpool, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	for _, directory := range []string{root, filepath.Join(root, "pending"), filepath.Join(root, "state")} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return nil, fmt.Errorf("create spool directory: %w", err)
		}
	}
	ledgerPath := filepath.Join(root, "state", "processed.jsonl")
	known := map[string]bool{}
	if ledger, openErr := os.Open(ledgerPath); openErr == nil {
		scanner := bufio.NewScanner(ledger)
		buffer := make([]byte, 64*1024)
		scanner.Buffer(buffer, 1024*1024)
		for scanner.Scan() {
			var record completedRecord
			if json.Unmarshal(scanner.Bytes(), &record) == nil && record.Fingerprint != "" {
				known[record.Fingerprint] = true
			}
		}
		_ = ledger.Close()
		if err := scanner.Err(); err != nil {
			return nil, fmt.Errorf("read processed ledger: %w", err)
		}
	} else if !errors.Is(openErr, os.ErrNotExist) {
		return nil, openErr
	}
	if info, statErr := os.Stat(ledgerPath); statErr == nil && info.Size() > 16*1024*1024 {
		if err := compactProcessedLedger(ledgerPath, known); err != nil {
			return nil, fmt.Errorf("compact processed ledger: %w", err)
		}
	}
	processed, err := os.OpenFile(ledgerPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	spool := &durableSpool{root: root, items: map[string]*spoolItem{}, known: known, inFlight: map[string]bool{}, processed: processed}
	entries, err := os.ReadDir(filepath.Join(root, "pending"))
	if err != nil {
		_ = processed.Close()
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		directory := filepath.Join(root, "pending", entry.Name())
		item, loadErr := readSpoolManifest(filepath.Join(directory, "manifest.json"))
		if loadErr != nil {
			_ = processed.Close()
			return nil, fmt.Errorf("load spool item %s: %w", entry.Name(), loadErr)
		}
		item.directory = directory
		spool.items[item.ID] = item
		spool.known[item.Fingerprint] = true
	}
	return spool, nil
}

func compactProcessedLedger(path string, known map[string]bool) error {
	temporary := path + ".compact"
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(file)
	fingerprints := make([]string, 0, len(known))
	for fingerprint := range known {
		fingerprints = append(fingerprints, fingerprint)
	}
	sort.Strings(fingerprints)
	for _, fingerprint := range fingerprints {
		if err := encoder.Encode(completedRecord{Fingerprint: fingerprint}); err != nil {
			_ = file.Close()
			return err
		}
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return replaceFile(temporary, path)
}

func (spool *durableSpool) Close() error {
	spool.mu.Lock()
	defer spool.mu.Unlock()
	return spool.processed.Close()
}

func (spool *durableSpool) Known(fingerprint string) bool {
	spool.mu.Lock()
	defer spool.mu.Unlock()
	return spool.known[fingerprint]
}

func (spool *durableSpool) Queue(sourcePath, fingerprint string, recording parsedRecording) (string, bool, error) {
	spool.mu.Lock()
	defer spool.mu.Unlock()
	if spool.known[fingerprint] {
		return "", false, nil
	}
	digest := sha256.Sum256([]byte(recording.Request.IdempotencyKey + "\x00" + fingerprint))
	id := hex.EncodeToString(digest[:16])
	if _, exists := spool.items[id]; exists {
		spool.known[fingerprint] = true
		return id, false, nil
	}
	pendingRoot := filepath.Join(spool.root, "pending")
	temporary, err := os.MkdirTemp(pendingRoot, ".queue-")
	if err != nil {
		return "", false, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(temporary)
		}
	}()
	audioName := "audio." + recording.AudioFormat
	if err := os.WriteFile(filepath.Join(temporary, audioName), recording.AudioBytes, 0o600); err != nil {
		return "", false, err
	}
	item := &spoolItem{ID: id, SourcePath: sourcePath, Fingerprint: fingerprint, AudioFile: audioName, Request: recording.Request, NextAttempt: time.Now().UTC(), CreatedAt: time.Now().UTC()}
	if err := writeJSONAtomic(filepath.Join(temporary, "manifest.json"), item); err != nil {
		return "", false, err
	}
	finalDirectory := filepath.Join(pendingRoot, id)
	if err := os.Rename(temporary, finalDirectory); err != nil {
		return "", false, err
	}
	committed = true
	item.directory = finalDirectory
	spool.items[id] = item
	spool.known[fingerprint] = true
	return id, true, nil
}

func (spool *durableSpool) ClaimDue(now time.Time) (*spoolItem, bool) {
	spool.mu.Lock()
	defer spool.mu.Unlock()
	items := make([]*spoolItem, 0, len(spool.items))
	for _, item := range spool.items {
		if !spool.inFlight[item.ID] && !item.NextAttempt.After(now) {
			items = append(items, item)
		}
	}
	if len(items) == 0 {
		return nil, false
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].NextAttempt.Equal(items[j].NextAttempt) {
			return items[i].CreatedAt.Before(items[j].CreatedAt)
		}
		return items[i].NextAttempt.Before(items[j].NextAttempt)
	})
	selected := *items[0]
	spool.inFlight[selected.ID] = true
	return &selected, true
}

func (spool *durableSpool) Retry(item *spoolItem, failure error) error {
	spool.mu.Lock()
	defer spool.mu.Unlock()
	current, exists := spool.items[item.ID]
	if !exists {
		delete(spool.inFlight, item.ID)
		return nil
	}
	current.Attempts++
	current.LastError = sanitizeError(failure)
	delay := retryDelay(current.Attempts)
	current.NextAttempt = time.Now().UTC().Add(delay)
	err := writeJSONAtomic(filepath.Join(current.directory, "manifest.json"), current)
	delete(spool.inFlight, item.ID)
	return err
}

func (spool *durableSpool) Complete(item *spoolItem) error {
	spool.mu.Lock()
	defer spool.mu.Unlock()
	current, exists := spool.items[item.ID]
	if !exists {
		delete(spool.inFlight, item.ID)
		return nil
	}
	record := completedRecord{Fingerprint: current.Fingerprint, SourcePath: current.SourcePath, CompletedAt: time.Now().UTC()}
	raw, _ := json.Marshal(record)
	if _, err := spool.processed.Write(append(raw, '\n')); err != nil {
		delete(spool.inFlight, item.ID)
		return err
	}
	if err := spool.processed.Sync(); err != nil {
		delete(spool.inFlight, item.ID)
		return err
	}
	if err := os.RemoveAll(current.directory); err != nil {
		delete(spool.inFlight, item.ID)
		return err
	}
	delete(spool.items, item.ID)
	delete(spool.inFlight, item.ID)
	return nil
}

func (spool *durableSpool) Remember(fingerprint, sourcePath string) error {
	spool.mu.Lock()
	defer spool.mu.Unlock()
	if spool.known[fingerprint] {
		return nil
	}
	record := completedRecord{Fingerprint: fingerprint, SourcePath: sourcePath, CompletedAt: time.Now().UTC()}
	raw, _ := json.Marshal(record)
	if _, err := spool.processed.Write(append(raw, '\n')); err != nil {
		return err
	}
	if err := spool.processed.Sync(); err != nil {
		return err
	}
	spool.known[fingerprint] = true
	return nil
}

func (spool *durableSpool) Counts() (pending, active int) {
	spool.mu.Lock()
	defer spool.mu.Unlock()
	return len(spool.items), len(spool.inFlight)
}

func readSpoolManifest(path string) (*spoolItem, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var item spoolItem
	if err := json.Unmarshal(raw, &item); err != nil {
		return nil, err
	}
	if item.ID == "" || item.Fingerprint == "" || item.AudioFile == "" || item.Request.SenderID == "" {
		return nil, errors.New("incomplete manifest")
	}
	return &item, nil
}

func writeJSONAtomic(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	if err := replaceFile(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

func retryDelay(attempt int) time.Duration {
	if attempt < 1 {
		return time.Second
	}
	delay := time.Second << min(attempt, 8)
	if delay > 5*time.Minute {
		return 5 * time.Minute
	}
	return delay
}

func sanitizeError(err error) string {
	if err == nil {
		return ""
	}
	value := strings.Join(strings.Fields(err.Error()), " ")
	if len(value) > 500 {
		value = value[:500]
	}
	return value
}

func sourceFingerprint(path string, info os.FileInfo) string {
	canonical, _ := filepath.Abs(path)
	material := strings.ToLower(filepath.Clean(canonical)) + "\x00" + fmt.Sprint(info.Size()) + "\x00" + fmt.Sprint(info.ModTime().UnixNano())
	digest := sha256.Sum256([]byte(material))
	return hex.EncodeToString(digest[:])
}

func recordingFingerprint(audio []byte, callIdentity string) string {
	digest := sha256.New()
	_, _ = digest.Write([]byte(callIdentity))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write(audio)
	return hex.EncodeToString(digest.Sum(nil))
}
