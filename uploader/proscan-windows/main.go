package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/kardianos/service"
)

var (
	version   = "dev"
	commit    = "unknown"
	buildTime = "unknown"
)

const windowsServiceName = "CallLoggerProScanUploader"

type serviceProgram struct {
	cfg           config
	logger        *slog.Logger
	exitOnFailure bool
	cancel        context.CancelFunc
	done          chan error
	mu            sync.Mutex
}

func (program *serviceProgram) Start(_ service.Service) error {
	program.mu.Lock()
	defer program.mu.Unlock()
	if program.cancel != nil {
		return errors.New("uploader is already running")
	}
	app, err := newUploaderApplication(program.cfg, program.logger)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	program.cancel = cancel
	program.done = make(chan error, 1)
	go func() {
		runErr := app.Run(ctx)
		program.done <- runErr
		if runErr != nil && program.exitOnFailure && ctx.Err() == nil {
			program.logger.Error("service stopped unexpectedly", "error", runErr)
			os.Exit(1)
		}
	}()
	return nil
}

func (program *serviceProgram) Stop(_ service.Service) error {
	program.mu.Lock()
	cancel, done := program.cancel, program.done
	program.cancel = nil
	program.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		select {
		case err := <-done:
			return err
		case <-time.After(30 * time.Second):
			return errors.New("uploader did not stop within 30 seconds")
		}
	}
	return nil
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "run":
		err = runCommand(os.Args[2:])
	case "check":
		err = checkCommand(os.Args[2:])
	case "inspect":
		err = inspectCommand(os.Args[2:])
	case "service":
		err = serviceCommand(os.Args[2:])
	case "version", "--version", "-version":
		fmt.Printf("proscan-uploader %s commit=%s built=%s go=%s\n", version, commit, buildTime, runtime.Version())
		return
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "proscan-uploader:", sanitizeError(err))
		os.Exit(1)
	}
}

func runCommand(args []string) error {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	configPath := flags.String("config", platformDefaultConfigPath(), "path to YAML configuration")
	serviceMode := flags.Bool("service", false, "run under the Windows service manager")
	if err := flags.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	logger, closeLog, err := configuredLogger(cfg)
	if err != nil {
		return err
	}
	defer closeLog()
	program := &serviceProgram{cfg: cfg, logger: logger, exitOnFailure: *serviceMode}
	serviceConfig := serviceDefinition(cfg.configPath)
	serviceInstance, err := service.New(program, serviceConfig)
	if err != nil {
		return err
	}
	if *serviceMode {
		return serviceInstance.Run()
	}
	app, err := newUploaderApplication(cfg, logger)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return app.Run(ctx)
}

func checkCommand(args []string) error {
	flags := flag.NewFlagSet("check", flag.ContinueOnError)
	configPath := flags.String("config", platformDefaultConfigPath(), "path to YAML configuration")
	if err := flags.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	location, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return err
	}
	for _, watch := range cfg.WatchDirectories {
		info, statErr := os.Stat(watch.Path)
		if statErr != nil || !info.IsDir() {
			return fmt.Errorf("watch directory %q is unavailable", watch.Path)
		}
		newest, findErr := newestRecording(watch.Path)
		if findErr != nil {
			return findErr
		}
		if newest != "" {
			raw, readErr := os.ReadFile(newest)
			if readErr != nil {
				return readErr
			}
			if _, parseErr := parseProScanRecording(raw, newest, watch, cfg.Logger.SenderID, location); parseErr != nil {
				return fmt.Errorf("parse newest recording in %q: %w", watch.Path, parseErr)
			}
		}
		fmt.Printf("OK watch=%q system=%q newest=%q\n", watch.Path, watch.SystemID, filepath.Base(newest))
	}
	client, err := newLoggerClient(cfg)
	if err != nil {
		return err
	}
	request, _ := http.NewRequest(http.MethodGet, client.baseURL+"/healthz", nil)
	response, err := client.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("logger health check: %w", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64*1024))
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("logger health check returned HTTP %d", response.StatusCode)
	}
	if _, err := cfg.apiKey(); err != nil {
		return err
	}
	fmt.Printf("OK logger=%s sender=%s health=200 credential=available\n", client.baseURL, cfg.Logger.SenderID)
	return nil
}

func inspectCommand(args []string) error {
	flags := flag.NewFlagSet("inspect", flag.ContinueOnError)
	directory := flags.String("directory", "", "directory containing ProScan MP3 files")
	systemID := flags.String("system-id", "SAMPLE", "temporary logger system ID")
	receiverID := flags.String("receiver-id", "", "optional receiver ID override")
	timezone := flags.String("timezone", "America/Toronto", "recording timezone")
	jsonOutput := flags.Bool("json", false, "emit one JSON object per recording")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *directory == "" {
		return errors.New("inspect requires --directory")
	}
	location, err := time.LoadLocation(*timezone)
	if err != nil {
		return err
	}
	watch := watchConfig{Path: *directory, SystemID: *systemID, SystemName: *systemID, ReceiverID: *receiverID, ConventionalIDPrefix: "CONV"}
	count := 0
	err = filepath.WalkDir(*directory, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !isSupportedRecording(path) {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		recording, parseErr := parseProScanRecording(raw, path, watch, "inspection", location)
		if parseErr != nil {
			return fmt.Errorf("%s: %w", filepath.Base(path), parseErr)
		}
		count++
		if *jsonOutput {
			line, _ := json.Marshal(recording.Request.Call)
			fmt.Println(string(line))
		} else {
			fmt.Printf("OK file=%q start=%s duration_ms=%d scanner=%q site=%q talkgroup=%q name=%q radio=%q frequency=%q\n", filepath.Base(path), recording.Request.Call.StartTime, recording.Request.Call.DurationMS, recording.Request.Call.ReceiverID, recording.Request.Call.SiteName, recording.Request.Call.TalkgroupID, recording.Request.Call.TalkgroupName, recording.Request.Call.RadioID, recording.Request.Call.Frequency)
		}
		return nil
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "inspected %d ProScan recordings successfully\n", count)
	if count == 0 {
		return errors.New("no MP3 recordings found")
	}
	return nil
}

func serviceCommand(args []string) error {
	flags := flag.NewFlagSet("service", flag.ContinueOnError)
	configPath := flags.String("config", platformDefaultConfigPath(), "path to YAML configuration")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return errors.New("usage: proscan-uploader service --config FILE install|uninstall|start|stop|restart|status")
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	logger, closeLog, err := configuredLogger(cfg)
	if err != nil {
		return err
	}
	defer closeLog()
	instance, err := service.New(&serviceProgram{cfg: cfg, logger: logger}, serviceDefinition(cfg.configPath))
	if err != nil {
		return err
	}
	action := strings.ToLower(flags.Arg(0))
	if action == "status" {
		status, statusErr := instance.Status()
		if statusErr != nil {
			return statusErr
		}
		label := "unknown"
		switch status {
		case service.StatusRunning:
			label = "running"
		case service.StatusStopped:
			label = "stopped"
		}
		fmt.Println(label)
		return nil
	}
	if action == "install" && runtime.GOOS != "windows" {
		return errors.New("service installation is supported only on Windows")
	}
	if err := service.Control(instance, action); err != nil {
		return err
	}
	fmt.Printf("service %s: %s\n", windowsServiceName, action)
	return nil
}

func serviceDefinition(configPath string) *service.Config {
	return &service.Config{
		Name:        windowsServiceName,
		DisplayName: "Call Logger ProScan Uploader",
		Description: "Watches ProScan recording directories and uploads completed calls to Call Logger.",
		Arguments:   []string{"run", "--config", configPath, "--service"},
		Option:      service.KeyValue{"StartType": "automatic", "OnFailure": "restart"},
	}
}

func configuredLogger(cfg config) (*slog.Logger, func(), error) {
	writers := []io.Writer{os.Stdout}
	closeLog := func() {}
	if cfg.LogFile != "" {
		if err := os.MkdirAll(filepath.Dir(cfg.LogFile), 0o700); err != nil {
			return nil, closeLog, err
		}
		if info, err := os.Stat(cfg.LogFile); err == nil && info.Size() > 20*1024*1024 {
			rotated := cfg.LogFile + ".1"
			_ = os.Remove(rotated)
			if err := os.Rename(cfg.LogFile, rotated); err != nil {
				return nil, closeLog, fmt.Errorf("rotate log: %w", err)
			}
		}
		file, err := os.OpenFile(cfg.LogFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, closeLog, err
		}
		writers = append(writers, file)
		closeLog = func() { _ = file.Close() }
	}
	return slog.New(slog.NewTextHandler(io.MultiWriter(writers...), &slog.HandlerOptions{Level: slog.LevelInfo})), closeLog, nil
}

func newestRecording(root string) (string, error) {
	newest := ""
	var newestTime time.Time
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !isSupportedRecording(path) {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		if newest == "" || info.ModTime().After(newestTime) {
			newest, newestTime = path, info.ModTime()
		}
		return nil
	})
	return newest, err
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage: proscan-uploader COMMAND [options]

commands:
  run       watch directories and upload completed recordings
  check     validate configuration, directories, samples, and logger health
  inspect   parse a directory without uploading
  service   install/control the Windows service
  version   print build information`)
}
