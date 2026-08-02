package main

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseDigitalProScanRecording(t *testing.T) {
	audio := syntheticProScanMP3(map[string]string{
		"TRDA": "20260802115230-20260802115233",
		"TIT2": "35680|Renfrew D1",
		"TPE1": "Pembroke Twr|MOH Renfrew CACC",
		"TPE2": "08/02/26 11:52:30|BCD996P2|143.9700|NFM|||48327|",
	}, map[string]string{
		"Scanner": "BCD996P2", "EndingDate": "20260802115233", "SystemName": "Pembroke Twr",
		"DepartmentName": "MOH Renfrew CACC", "ChannelName": "Renfrew D1", "Frequency": "143.9700",
		"Modulation": "NFM", "TGID": "35680", "RSSI": "451",
	})
	location, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatal(err)
	}
	watch := watchConfig{SystemID: "SCANNER-DIGITAL", SystemName: "Scanner Digital", ConventionalIDPrefix: "CONV"}
	recording, err := parseProScanRecording(audio, "08-02-26 11-52-30 - Pembroke Twr - Renfrew D1.mp3", watch, "windows-proscan", location)
	if err != nil {
		t.Fatal(err)
	}
	call := recording.Request.Call
	if call.StartTime != "2026-08-02T15:52:30Z" {
		t.Fatalf("start time = %q", call.StartTime)
	}
	if call.SystemID != "SCANNER-DIGITAL" || call.ReceiverID != "BCD996P2" || call.SiteID != "PEMBROKE-TWR" {
		t.Fatalf("incorrect mapping: %#v", call)
	}
	if call.TalkgroupID != "35680" || call.TalkgroupName != "Renfrew D1" || call.RadioID != "48327" {
		t.Fatalf("incorrect call identity: %#v", call)
	}
	if call.DurationMS < 250 || call.DurationMS > 270 {
		t.Fatalf("unexpected frame duration: %d", call.DurationMS)
	}
	if !strings.Contains(call.Notes, "RSSI: 451") {
		t.Fatalf("notes did not preserve RSSI: %q", call.Notes)
	}
}

func TestParseConventionalProScanRecording(t *testing.T) {
	audio := syntheticProScanMP3(map[string]string{
		"TRDA": "20260802115554-20260802115557",
		"TIT2": "|DEEP RIVER PS",
		"TPE1": "RDIO-ANALOG|Ontario",
		"TPE2": "08/02/26 11:55:54|BCT15X|150.4700|NFM|CTCSS 173.8Hz||||",
	}, map[string]string{
		"Scanner": "BCT15X", "SystemName": "RDIO-ANALOG", "DepartmentName": "Ontario",
		"ChannelName": "DEEP RIVER PS", "Frequency": "150.4700", "Modulation": "NFM", "Tone": "CTCSS 173.8Hz",
	})
	location, _ := time.LoadLocation("America/Toronto")
	watch := watchConfig{SystemID: "SCANNER-ANALOG", SystemName: "Scanner Analog", ReceiverID: "BCT15X", ConventionalIDPrefix: "CONV"}
	recording, err := parseProScanRecording(audio, "DEEP RIVER PS_20260802_11-55-54.mp3", watch, "windows-proscan", location)
	if err != nil {
		t.Fatal(err)
	}
	call := recording.Request.Call
	if call.TalkgroupID != "CONV-150.4700-CTCSS-173.8HZ" || call.CallType != "conventional" {
		t.Fatalf("unexpected conventional identity: %#v", call)
	}
	if call.RadioID != "" {
		t.Fatalf("unexpected radio ID: %q", call.RadioID)
	}
}

func TestLoadConfigRejectsUnknownFieldsAndAppliesMappings(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "config.yaml")
	raw := `version: 1
logger:
  url: http://127.0.0.1:8080
  sender_id: windows-proscan
  api_key: synthetic-key
spool_directory: spool
watch_directories:
  - path: 'E:\BCD996'
    system_id: SCANNER-DIGITAL
  - path: 'E:\BCT15X'
    system_id: SCANNER-ANALOG
`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WatchDirectories[0].SystemName != "SCANNER-DIGITAL" || !cfg.WatchDirectories[0].recursive() || cfg.SettleSeconds != 3 {
		t.Fatalf("defaults not applied: %#v", cfg)
	}
	if !strings.HasSuffix(cfg.SpoolDirectory, string(filepath.Separator)+"spool") {
		t.Fatalf("relative spool was not resolved: %q", cfg.SpoolDirectory)
	}
	bad := strings.Replace(raw, "version: 1", "version: 1\nunknown_setting: true", 1)
	if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfig(path); err == nil {
		t.Fatal("unknown YAML field was accepted")
	}
}

func syntheticProScanMP3(frames, custom map[string]string) []byte {
	var payload bytes.Buffer
	for _, id := range []string{"TSEE", "TRDA", "TIT2", "TPE1", "TPE2"} {
		value := frames[id]
		if id == "TSEE" && value == "" {
			value = "ProScan"
		}
		body := append([]byte{0}, append([]byte(value), 0)...)
		payload.WriteString(id)
		_ = binary.Write(&payload, binary.BigEndian, uint32(len(body)))
		payload.Write([]byte{0, 0})
		payload.Write(body)
	}
	var customBody bytes.Buffer
	for _, key := range []string{"Scanner", "EndingDate", "SystemName", "SiteName", "DepartmentName", "ChannelName", "Frequency", "Modulation", "Tone", "TGID", "UID", "UID#", "RSSI"} {
		customBody.WriteString(key + ":" + custom[key])
		customBody.WriteByte(0)
	}
	payload.WriteString("pros")
	_ = binary.Write(&payload, binary.LittleEndian, uint32(customBody.Len()))
	payload.Write([]byte{0, 0})
	payload.Write(customBody.Bytes())
	result := []byte{'I', 'D', '3', 3, 0, 0}
	result = append(result, syncSafeBytes(payload.Len())...)
	result = append(result, payload.Bytes()...)
	for range 10 {
		frame := make([]byte, 104)
		copy(frame, []byte{0xff, 0xfb, 0x10, 0xc0})
		result = append(result, frame...)
	}
	return result
}

func syncSafeBytes(value int) []byte {
	return []byte{byte(value >> 21 & 0x7f), byte(value >> 14 & 0x7f), byte(value >> 7 & 0x7f), byte(value & 0x7f)}
}
