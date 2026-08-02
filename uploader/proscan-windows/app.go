package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

type observedFile struct {
	path        string
	watch       watchConfig
	size        int64
	modified    time.Time
	stableSince time.Time
	nextTry     time.Time
	lastError   string
}

type uploaderApplication struct {
	cfg          config
	location     *time.Location
	logger       *slog.Logger
	spool        *durableSpool
	client       *loggerClient
	clients      map[string]*loggerClient
	watcher      *fsnotify.Watcher
	observations map[string]*observedFile
	watchedDirs  map[string]bool
	workers      sync.WaitGroup
}

func newUploaderApplication(cfg config, logger *slog.Logger) (*uploaderApplication, error) {
	location, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return nil, fmt.Errorf("load timezone %q: %w", cfg.Timezone, err)
	}
	spool, err := openDurableSpool(cfg.SpoolDirectory)
	if err != nil {
		return nil, err
	}
	clients := map[string]*loggerClient{}
	for _, watch := range cfg.WatchDirectories {
		watchClient, clientErr := newLoggerClientForWatch(cfg, watch)
		if clientErr != nil {
			_ = spool.Close()
			return nil, clientErr
		}
		clients[watchClient.senderID] = watchClient
	}
	var client *loggerClient
	if globalClient, clientErr := newLoggerClient(cfg); clientErr == nil {
		client = globalClient
		clients[globalClient.senderID] = globalClient
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		_ = spool.Close()
		return nil, err
	}
	return &uploaderApplication{cfg: cfg, location: location, logger: logger, spool: spool, client: client, clients: clients, watcher: watcher, observations: map[string]*observedFile{}, watchedDirs: map[string]bool{}}, nil
}

func (app *uploaderApplication) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer app.watcher.Close()
	defer app.spool.Close()
	for _, watch := range app.cfg.WatchDirectories {
		info, err := os.Stat(watch.Path)
		if err != nil || !info.IsDir() {
			return fmt.Errorf("watch directory %q is unavailable", watch.Path)
		}
		if err := app.addDirectoryTree(watch.Path, watch); err != nil {
			return err
		}
		if err := app.scanDirectory(watch, true); err != nil {
			return err
		}
	}
	for worker := 0; worker < app.cfg.UploadWorkers; worker++ {
		app.workers.Add(1)
		go app.uploadWorker(ctx, worker+1)
	}
	defer func() {
		cancel()
		app.workers.Wait()
	}()
	settleTicker := time.NewTicker(500 * time.Millisecond)
	defer settleTicker.Stop()
	rescanTicker := time.NewTicker(time.Duration(app.cfg.RescanSeconds) * time.Second)
	defer rescanTicker.Stop()
	statusTicker := time.NewTicker(5 * time.Minute)
	defer statusTicker.Stop()
	app.logger.Info("ProScan uploader started", "watches", len(app.cfg.WatchDirectories), "workers", app.cfg.UploadWorkers, "spool", app.cfg.SpoolDirectory)
	for {
		select {
		case <-ctx.Done():
			app.logger.Info("ProScan uploader stopping")
			return nil
		case event, ok := <-app.watcher.Events:
			if !ok {
				return errors.New("directory watcher stopped unexpectedly")
			}
			app.handleEvent(event)
		case err, ok := <-app.watcher.Errors:
			if ok {
				app.logger.Error("directory watcher error", "error", err)
			}
		case <-settleTicker.C:
			app.processReadyFiles()
		case <-rescanTicker.C:
			for _, watch := range app.cfg.WatchDirectories {
				if err := app.scanDirectory(watch, false); err != nil {
					app.logger.Error("periodic directory scan failed", "path", watch.Path, "error", err)
				}
			}
		case <-statusTicker.C:
			pending, active := app.spool.Counts()
			app.logger.Info("uploader status", "pending", pending, "active_uploads", active, "observed_files", len(app.observations))
		}
	}
}

func (app *uploaderApplication) scanDirectory(watch watchConfig, initial bool) error {
	return filepath.WalkDir(watch.Path, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path != watch.Path && !watch.recursive() {
				return filepath.SkipDir
			}
			if watch.recursive() {
				if addErr := app.addWatch(path); addErr != nil {
					return addErr
				}
			}
			return nil
		}
		if !isSupportedRecording(path) {
			return nil
		}
		info, statErr := entry.Info()
		if statErr != nil {
			return statErr
		}
		fingerprint := sourceFingerprint(path, info)
		if initial && !app.cfg.includeExisting() {
			return app.spool.Remember(fingerprint, path)
		}
		app.observe(path, watch, info)
		return nil
	})
}

func (app *uploaderApplication) addDirectoryTree(root string, watch watchConfig) error {
	if !watch.recursive() {
		return app.addWatch(root)
	}
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return app.addWatch(path)
		}
		return nil
	})
}

func (app *uploaderApplication) addWatch(path string) error {
	key := normalizePath(path)
	if app.watchedDirs[key] {
		return nil
	}
	if err := app.watcher.Add(path); err != nil {
		return fmt.Errorf("watch directory %q: %w", path, err)
	}
	app.watchedDirs[key] = true
	return nil
}

func (app *uploaderApplication) handleEvent(event fsnotify.Event) {
	watch, found := app.watchForPath(event.Name)
	if !found {
		return
	}
	if event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
		delete(app.observations, normalizePath(event.Name))
		return
	}
	if !(event.Has(fsnotify.Create) || event.Has(fsnotify.Write)) {
		return
	}
	info, err := os.Stat(event.Name)
	if err != nil {
		return
	}
	if info.IsDir() {
		if watch.recursive() {
			if err := app.addDirectoryTree(event.Name, watch); err != nil {
				app.logger.Error("could not watch new directory", "path", event.Name, "error", err)
			}
		}
		return
	}
	if isSupportedRecording(event.Name) {
		app.observe(event.Name, watch, info)
	}
}

func (app *uploaderApplication) observe(path string, watch watchConfig, info os.FileInfo) {
	key := normalizePath(path)
	now := time.Now()
	current, exists := app.observations[key]
	if !exists {
		app.observations[key] = &observedFile{path: path, watch: watch, size: info.Size(), modified: info.ModTime(), stableSince: now}
		return
	}
	if current.size != info.Size() || !current.modified.Equal(info.ModTime()) {
		current.size, current.modified, current.stableSince = info.Size(), info.ModTime(), now
		current.nextTry, current.lastError = time.Time{}, ""
	}
}

func (app *uploaderApplication) processReadyFiles() {
	now := time.Now()
	for key, observed := range app.observations {
		if now.Before(observed.nextTry) || now.Sub(observed.stableSince) < time.Duration(app.cfg.SettleSeconds)*time.Second {
			continue
		}
		info, err := os.Stat(observed.path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				delete(app.observations, key)
			}
			continue
		}
		if info.Size() != observed.size || !info.ModTime().Equal(observed.modified) {
			observed.size, observed.modified, observed.stableSince = info.Size(), info.ModTime(), now
			continue
		}
		fingerprint := sourceFingerprint(observed.path, info)
		if app.spool.Known(fingerprint) {
			delete(app.observations, key)
			continue
		}
		file, ready, openErr := openExclusive(observed.path)
		if openErr != nil {
			app.deferFile(observed, openErr)
			continue
		}
		if !ready {
			// Windows reports this while ProScan still holds the recording.
			continue
		}
		lockedInfo, statErr := file.Stat()
		if statErr != nil {
			_ = file.Close()
			app.deferFile(observed, statErr)
			continue
		}
		if lockedInfo.Size() != observed.size || !lockedInfo.ModTime().Equal(observed.modified) {
			_ = file.Close()
			observed.size, observed.modified, observed.stableSince = lockedInfo.Size(), lockedInfo.ModTime(), now
			continue
		}
		fingerprint = sourceFingerprint(observed.path, lockedInfo)
		raw, readErr := io.ReadAll(io.LimitReader(file, app.cfg.MaxAudioBytes+1))
		closeErr := file.Close()
		if readErr == nil {
			readErr = closeErr
		}
		if readErr != nil {
			app.deferFile(observed, readErr)
			continue
		}
		if int64(len(raw)) > app.cfg.MaxAudioBytes {
			app.deferFile(observed, fmt.Errorf("recording exceeds max_audio_bytes (%d)", app.cfg.MaxAudioBytes))
			continue
		}
		senderID, _, credentialErr := app.cfg.credentialsForWatch(observed.watch)
		if credentialErr != nil {
			app.deferFile(observed, credentialErr)
			continue
		}
		recording, parseErr := parseProScanRecording(raw, observed.path, observed.watch, senderID, app.location)
		if parseErr != nil {
			app.deferFile(observed, parseErr)
			continue
		}
		id, queued, queueErr := app.spool.Queue(observed.path, fingerprint, recording)
		if queueErr != nil {
			app.deferFile(observed, queueErr)
			continue
		}
		delete(app.observations, key)
		if queued {
			app.logger.Info("recording queued", "item", id, "file", filepath.Base(observed.path), "system", recording.Request.Call.SystemID, "talkgroup", recording.Request.Call.TalkgroupID, "radio", recording.Request.Call.RadioID)
		}
	}
}

func (app *uploaderApplication) deferFile(observed *observedFile, err error) {
	message := sanitizeError(err)
	if message != observed.lastError {
		app.logger.Error("recording is not ready for upload", "file", observed.path, "error", message)
		observed.lastError = message
	}
	observed.nextTry = time.Now().Add(time.Minute)
}

func (app *uploaderApplication) uploadWorker(ctx context.Context, number int) {
	defer app.workers.Done()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for {
				item, ok := app.spool.ClaimDue(time.Now().UTC())
				if !ok {
					break
				}
				client := app.clients[item.Request.SenderID]
				if client == nil {
					client = app.client
				}
				if client == nil {
					err := errors.New("no credentials configured for sender " + item.Request.SenderID)
					_ = app.spool.Retry(item, err)
					app.logger.Error("recording upload failed", "worker", number, "item", item.ID, "attempt", item.Attempts+1, "error", err)
					continue
				}
				callID, duplicate, err := client.Upload(ctx, item)
				if err != nil {
					if retryErr := app.spool.Retry(item, err); retryErr != nil {
						app.logger.Error("could not save upload retry", "item", item.ID, "error", retryErr)
					}
					app.logger.Error("recording upload failed", "worker", number, "item", item.ID, "attempt", item.Attempts+1, "error", sanitizeError(err))
					continue
				}
				if err := app.spool.Complete(item); err != nil {
					app.logger.Error("could not complete spool item", "item", item.ID, "error", err)
					continue
				}
				app.logger.Info("recording uploaded", "worker", number, "item", item.ID, "call_id", callID, "duplicate", duplicate)
			}
		}
	}
}

func (app *uploaderApplication) watchForPath(path string) (watchConfig, bool) {
	pathKey := normalizePath(path)
	bestLength := -1
	var best watchConfig
	for _, watch := range app.cfg.WatchDirectories {
		root := strings.TrimRight(normalizePath(watch.Path), string(os.PathSeparator))
		if pathKey == root || strings.HasPrefix(pathKey, root+string(os.PathSeparator)) {
			if len(root) > bestLength {
				best, bestLength = watch, len(root)
			}
		}
	}
	return best, bestLength >= 0
}

func normalizePath(path string) string {
	absolute, err := filepath.Abs(path)
	if err == nil {
		path = absolute
	}
	return strings.ToLower(filepath.Clean(path))
}

func isSupportedRecording(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".mp3")
}
