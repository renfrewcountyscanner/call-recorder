package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDurableSpoolSurvivesRestartAndRemembersCompletion(t *testing.T) {
	root := t.TempDir()
	spool, err := openDurableSpool(root)
	if err != nil {
		t.Fatal(err)
	}
	recording := parsedRecording{AudioBytes: []byte("ID3 synthetic"), AudioFormat: "mp3", Request: createUploadRequest{SenderID: "sender", IdempotencyKey: "call-one", AudioFormat: "mp3", Call: callMetadata{SystemID: "system", TalkgroupID: "1"}}}
	id, queued, err := spool.Queue(`E:\BCD996\call.mp3`, "fingerprint-one", recording)
	if err != nil || !queued || id == "" {
		t.Fatalf("queue: id=%q queued=%t err=%v", id, queued, err)
	}
	if err := spool.Close(); err != nil {
		t.Fatal(err)
	}
	spool, err = openDurableSpool(root)
	if err != nil {
		t.Fatal(err)
	}
	item, ok := spool.ClaimDue(time.Now().Add(time.Second))
	if !ok || item.ID != id {
		t.Fatalf("queued item did not survive restart: %#v", item)
	}
	if _, err := os.Stat(filepath.Join(item.directory, item.AudioFile)); err != nil {
		t.Fatal(err)
	}
	if err := spool.Retry(item, os.ErrDeadlineExceeded); err != nil {
		t.Fatal(err)
	}
	spool.mu.Lock()
	spool.items[id].NextAttempt = time.Now().Add(-time.Second)
	if err := writeJSONAtomic(filepath.Join(spool.items[id].directory, "manifest.json"), spool.items[id]); err != nil {
		spool.mu.Unlock()
		t.Fatal(err)
	}
	spool.mu.Unlock()
	item, ok = spool.ClaimDue(time.Now())
	if !ok || item.Attempts != 1 {
		t.Fatalf("retry manifest was not replaced: %#v", item)
	}
	if err := spool.Complete(item); err != nil {
		t.Fatal(err)
	}
	if !spool.Known("fingerprint-one") {
		t.Fatal("completed fingerprint was forgotten")
	}
	if err := spool.Close(); err != nil {
		t.Fatal(err)
	}
	spool, err = openDurableSpool(root)
	if err != nil {
		t.Fatal(err)
	}
	defer spool.Close()
	if !spool.Known("fingerprint-one") {
		t.Fatal("processed ledger did not survive restart")
	}
}
