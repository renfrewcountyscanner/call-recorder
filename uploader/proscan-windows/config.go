package main

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"gopkg.in/yaml.v3"
)

type config struct {
	Version             int           `yaml:"version"`
	Logger              loggerConfig  `yaml:"logger"`
	SpoolDirectory      string        `yaml:"spool_directory"`
	Timezone            string        `yaml:"timezone"`
	SettleSeconds       int           `yaml:"settle_seconds"`
	RescanSeconds       int           `yaml:"rescan_seconds"`
	UploadWorkers       int           `yaml:"upload_workers"`
	MaxAudioBytes       int64         `yaml:"max_audio_bytes"`
	IncludeExisting     *bool         `yaml:"include_existing"`
	DeleteUploadedFiles *bool         `yaml:"delete_uploaded_files"`
	LogFile             string        `yaml:"log_file"`
	WatchDirectories    []watchConfig `yaml:"watch_directories"`
	configPath          string
}

type loggerConfig struct {
	URL                   string `yaml:"url"`
	SenderID              string `yaml:"sender_id"`
	APIKey                string `yaml:"api_key,omitempty"`
	APIKeyFile            string `yaml:"api_key_file,omitempty"`
	APIKeyEnvironment     string `yaml:"api_key_environment,omitempty"`
	RequestTimeoutSeconds int    `yaml:"request_timeout_seconds"`
}

type watchConfig struct {
	Path                 string `yaml:"path"`
	SystemID             string `yaml:"system_id"`
	SystemName           string `yaml:"system_name,omitempty"`
	ReceiverID           string `yaml:"receiver_id,omitempty"`
	SenderID             string `yaml:"sender_id,omitempty"`
	APIKeyFile           string `yaml:"api_key_file,omitempty"`
	Recursive            *bool  `yaml:"recursive,omitempty"`
	UseTPE2RadioID       *bool  `yaml:"use_tpe2_radio_id,omitempty"`
	ProScanSystemAsSite  *bool  `yaml:"proscan_system_as_site,omitempty"`
	ConventionalIDPrefix string `yaml:"conventional_id_prefix,omitempty"`
}

func loadConfig(path string) (config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return config{}, fmt.Errorf("read config: %w", err)
	}
	var cfg config
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return config{}, fmt.Errorf("parse config: %w", err)
	}
	cfg.configPath, _ = filepath.Abs(path)
	cfg.applyDefaults()
	cfg.resolveRelativePaths(filepath.Dir(cfg.configPath))
	if err := cfg.validate(); err != nil {
		return config{}, err
	}
	return cfg, nil
}

func (cfg *config) applyDefaults() {
	if cfg.Version == 0 {
		cfg.Version = 1
	}
	if cfg.Timezone == "" {
		cfg.Timezone = "America/Toronto"
	}
	if cfg.SettleSeconds == 0 {
		cfg.SettleSeconds = 3
	}
	if cfg.RescanSeconds == 0 {
		cfg.RescanSeconds = 30
	}
	if cfg.UploadWorkers == 0 {
		cfg.UploadWorkers = 2
	}
	if cfg.MaxAudioBytes == 0 {
		cfg.MaxAudioBytes = 100 * 1024 * 1024
	}
	if cfg.Logger.RequestTimeoutSeconds == 0 {
		cfg.Logger.RequestTimeoutSeconds = 60
	}
	if cfg.Logger.APIKeyEnvironment == "" {
		cfg.Logger.APIKeyEnvironment = "CALL_LOGGER_API_KEY"
	}
	for index := range cfg.WatchDirectories {
		watch := &cfg.WatchDirectories[index]
		if watch.SystemName == "" {
			watch.SystemName = watch.SystemID
		}
		if watch.ConventionalIDPrefix == "" {
			watch.ConventionalIDPrefix = "CONV"
		}
	}
}

func (cfg *config) resolveRelativePaths(base string) {
	for _, target := range []*string{&cfg.SpoolDirectory, &cfg.LogFile, &cfg.Logger.APIKeyFile} {
		if *target != "" && !configuredPathIsAbs(*target) {
			*target = filepath.Join(base, *target)
		}
	}
	for index := range cfg.WatchDirectories {
		target := &cfg.WatchDirectories[index].APIKeyFile
		if *target != "" && !configuredPathIsAbs(*target) {
			*target = filepath.Join(base, *target)
		}
	}
}

func (cfg config) validate() error {
	if cfg.Version != 1 {
		return fmt.Errorf("unsupported config version %d", cfg.Version)
	}
	parsedURL, err := url.Parse(strings.TrimRight(strings.TrimSpace(cfg.Logger.URL), "/"))
	if err != nil || parsedURL.Scheme == "" || parsedURL.Host == "" {
		return errors.New("logger.url must be an absolute HTTP(S) URL")
	}
	if parsedURL.Scheme != "https" && !(parsedURL.Scheme == "http" && isLoopbackHost(parsedURL.Hostname())) {
		return errors.New("logger.url must use HTTPS unless it points to localhost")
	}
	globalSender := strings.TrimSpace(cfg.Logger.SenderID)
	credentialSources := 0
	for _, value := range []string{cfg.Logger.APIKey, cfg.Logger.APIKeyFile, os.Getenv(cfg.Logger.APIKeyEnvironment)} {
		if strings.TrimSpace(value) != "" {
			credentialSources++
		}
	}
	if credentialSources > 1 {
		return errors.New("configure exactly one API key source")
	}
	if cfg.SpoolDirectory == "" {
		return errors.New("spool_directory is required")
	}
	if cfg.SettleSeconds < 1 || cfg.SettleSeconds > 300 || cfg.RescanSeconds < 2 || cfg.RescanSeconds > 3600 {
		return errors.New("settle_seconds must be 1-300 and rescan_seconds must be 2-3600")
	}
	if cfg.UploadWorkers < 1 || cfg.UploadWorkers > 16 {
		return errors.New("upload_workers must be between 1 and 16")
	}
	if cfg.MaxAudioBytes < 1024 || cfg.MaxAudioBytes > 2*1024*1024*1024 {
		return errors.New("max_audio_bytes must be between 1 KiB and 2 GiB")
	}
	if len(cfg.WatchDirectories) == 0 {
		return errors.New("at least one watch_directory is required")
	}
	seen := map[string]bool{}
	for index, watch := range cfg.WatchDirectories {
		if strings.TrimSpace(watch.Path) == "" || strings.TrimSpace(watch.SystemID) == "" {
			return fmt.Errorf("watch_directories[%d] requires path and system_id", index)
		}
		key := strings.ToLower(strings.TrimRight(watch.Path, `\/`))
		if seen[key] {
			return fmt.Errorf("watch directory %q is configured more than once", watch.Path)
		}
		seen[key] = true
		if strings.ContainsAny(watch.SystemID+watch.ReceiverID+watch.SenderID, "\x00\r\n") {
			return fmt.Errorf("watch_directories[%d] contains a control character", index)
		}
		if (strings.TrimSpace(watch.SenderID) == "") != (strings.TrimSpace(watch.APIKeyFile) == "") {
			return fmt.Errorf("watch_directories[%d] must provide both sender_id and api_key_file", index)
		}
	}
	if credentialSources == 0 && globalSender == "" {
		for index, watch := range cfg.WatchDirectories {
			if strings.TrimSpace(watch.SenderID) == "" || strings.TrimSpace(watch.APIKeyFile) == "" {
				return fmt.Errorf("watch_directories[%d] requires sender_id and api_key_file when no global credential is configured", index)
			}
		}
	}
	if credentialSources > 0 && globalSender == "" {
		return errors.New("logger.sender_id is required when a global API key is configured")
	}
	return nil
}

func (cfg config) credentialsForWatch(watch watchConfig) (string, string, error) {
	sender := strings.TrimSpace(cfg.Logger.SenderID)
	if strings.TrimSpace(watch.SenderID) != "" {
		sender = strings.TrimSpace(watch.SenderID)
	}
	if sender == "" {
		return "", "", errors.New("sender_id is required")
	}
	if strings.TrimSpace(watch.APIKeyFile) != "" {
		raw, err := os.ReadFile(watch.APIKeyFile)
		if err != nil {
			return "", "", fmt.Errorf("read watch API key file: %w", err)
		}
		key := strings.TrimSpace(string(raw))
		if key == "" {
			return "", "", errors.New("watch API key file is empty")
		}
		return sender, key, nil
	}
	key, err := cfg.apiKey()
	return sender, key, err
}

func (cfg config) apiKey() (string, error) {
	if value := strings.TrimSpace(cfg.Logger.APIKey); value != "" {
		return value, nil
	}
	if value := strings.TrimSpace(os.Getenv(cfg.Logger.APIKeyEnvironment)); value != "" {
		return value, nil
	}
	raw, err := os.ReadFile(cfg.Logger.APIKeyFile)
	if err != nil {
		return "", fmt.Errorf("read API key file: %w", err)
	}
	value := strings.TrimSpace(string(raw))
	if value == "" {
		return "", errors.New("API key file is empty")
	}
	return value, nil
}

func (cfg config) includeExisting() bool { return cfg.IncludeExisting == nil || *cfg.IncludeExisting }

// deleteUploadedFiles defaults to true because the durable spool already holds
// a private copy until the logger has confirmed receipt.
func (cfg config) deleteUploadedFiles() bool {
	return cfg.DeleteUploadedFiles == nil || *cfg.DeleteUploadedFiles
}
func (watch watchConfig) recursive() bool { return watch.Recursive == nil || *watch.Recursive }
func (watch watchConfig) useTPE2RadioID() bool {
	return watch.UseTPE2RadioID == nil || *watch.UseTPE2RadioID
}
func (watch watchConfig) proScanSystemAsSite() bool {
	return watch.ProScanSystemAsSite == nil || *watch.ProScanSystemAsSite
}

func configuredPathIsAbs(path string) bool {
	if filepath.IsAbs(path) {
		return true
	}
	if len(path) >= 3 && ((path[0] >= 'A' && path[0] <= 'Z') || (path[0] >= 'a' && path[0] <= 'z')) && path[1] == ':' && (path[2] == '\\' || path[2] == '/') {
		return true
	}
	return strings.HasPrefix(path, `\\`)
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func platformDefaultConfigPath() string {
	if runtime.GOOS == "windows" {
		if root := os.Getenv("ProgramData"); root != "" {
			return filepath.Join(root, "CallLogger", "proscan-uploader.yaml")
		}
	}
	return "proscan-uploader.yaml"
}
