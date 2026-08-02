package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestLoggerClientPerformsAuthenticatedTwoStageUpload(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests++
		if request.Header.Get("X-Call-Recorder-Key") != "test-key" {
			t.Errorf("missing sender key")
		}
		switch request.URL.Path {
		case "/api/v1/uploads":
			var payload createUploadRequest
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Error(err)
			}
			if payload.SenderID != "test-sender" || payload.Call.SystemID != "SCANNER-DIGITAL" {
				t.Errorf("unexpected metadata: %#v", payload)
			}
			response.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(response, `{"upload_token":"temporary-token"}`)
		case "/api/v1/uploads/temporary-token":
			if request.Header.Get("X-Call-Recorder-Sender") != "test-sender" {
				t.Errorf("missing sender header")
			}
			raw, _ := io.ReadAll(request.Body)
			if string(raw) != "ID3audio" {
				t.Errorf("unexpected audio: %q", raw)
			}
			response.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(response, `{"call_id":"completed-call"}`)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "audio.mp3"), []byte("ID3audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	client := &loggerClient{baseURL: server.URL, senderID: "test-sender", apiKey: "test-key", httpClient: server.Client()}
	item := &spoolItem{directory: directory, AudioFile: "audio.mp3", Request: createUploadRequest{SenderID: "test-sender", AudioFormat: "mp3", Call: callMetadata{SystemID: "SCANNER-DIGITAL", TalkgroupID: "35680"}}}
	callID, duplicate, err := client.Upload(t.Context(), item)
	if err != nil {
		t.Fatal(err)
	}
	if callID != "completed-call" || duplicate || requests != 2 {
		t.Fatalf("call=%q duplicate=%t requests=%d", callID, duplicate, requests)
	}
}
