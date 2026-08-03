package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestApplicationDiscoversSettlesSpoolsAndUploads(t *testing.T) {
	uploaded := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/uploads":
			response.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(response, `{"upload_token":"watch-token"}`)
		case "/api/v1/uploads/watch-token":
			response.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(response, `{"call_id":"watch-call"}`)
			select {
			case uploaded <- struct{}{}:
			default:
			}
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	root := t.TempDir()
	watchDirectory := filepath.Join(root, "recordings")
	if err := os.Mkdir(watchDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := config{
		Logger:         loggerConfig{URL: server.URL, SenderID: "watch-sender", APIKey: "watch-key", RequestTimeoutSeconds: 5},
		SpoolDirectory: filepath.Join(root, "spool"), Timezone: "America/Toronto", SettleSeconds: 1,
		RescanSeconds: 2, UploadWorkers: 1, MaxAudioBytes: 1024 * 1024,
		WatchDirectories: []watchConfig{{Path: watchDirectory, SystemID: "SCANNER-DIGITAL", SystemName: "Scanner Digital", ConventionalIDPrefix: "CONV"}},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	app, err := newUploaderApplication(cfg, logger)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()
	time.Sleep(200 * time.Millisecond)
	recording := syntheticProScanMP3(map[string]string{
		"TRDA": "20260802115230-20260802115233", "TIT2": "35680|Renfrew D1",
		"TPE1": "Pembroke Twr|MOH Renfrew CACC", "TPE2": "08/02/26 11:52:30|BCD996P2|143.9700|NFM|||48327|",
	}, map[string]string{"Scanner": "BCD996P2", "SystemName": "Pembroke Twr", "DepartmentName": "MOH Renfrew CACC", "ChannelName": "Renfrew D1", "Frequency": "143.9700", "Modulation": "NFM", "TGID": "35680"})
	if err := os.WriteFile(filepath.Join(watchDirectory, "call.mp3"), recording, 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-uploaded:
	case <-time.After(8 * time.Second):
		t.Fatal("recording was not uploaded")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, err := os.Stat(filepath.Join(watchDirectory, "call.mp3"))
		if os.IsNotExist(err) {
			break
		}
		if err != nil || time.Now().After(deadline) {
			t.Fatalf("uploaded source recording was not deleted: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("application did not stop")
	}
}
