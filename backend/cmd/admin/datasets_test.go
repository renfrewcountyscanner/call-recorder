package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAlignTextUsesTimedSegments(t *testing.T) {
	timed := []byte(`[{"start":0,"end":4,"text":"first phrase"},{"start":4,"end":8,"text":"second phrase"}]`)
	got := alignText("first phrase second phrase", timed, sampleRange{Start: 4, End: 8}, 8, true)
	if got != "second phrase" {
		t.Fatalf("alignText = %q", got)
	}
}

func TestValidateTranscriptFlagsRepeatedPhrases(t *testing.T) {
	warnings := validateTranscript("go now go now go now ordinary trailing words", 8, "asr_finetune")
	if !strings.Contains(strings.Join(warnings, ","), "consecutive_repeated_phrase") {
		t.Fatalf("warnings = %#v", warnings)
	}
}

func TestDatasetManifestAlwaysIncludesRequiredProvenance(t *testing.T) {
	raw, err := json.Marshal(datasetManifestRecord{})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"schema_version", "export_time", "provider", "model", "profile", "transcription_settings_version", "system", "talkgroup", "call_timestamp", "audio_sha256", "codec", "sample_rate", "channels", "duration_seconds", "conversation_group_id", "validation_warnings"} {
		if !strings.Contains(string(raw), `"`+key+`"`) {
			t.Errorf("required key %q missing from %s", key, raw)
		}
	}
}
