package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/kardianos/service"

	"github.com/cerberus8484/opensourcebackup/internal/agent"
	agentclient "github.com/cerberus8484/opensourcebackup/internal/agent/client"
)

const (
	defaultPollInterval  = 30 * time.Second
	defaultTokenFilePath = "data/agent-token"
	serviceName          = "OpenSourceBackupAgent"
	serviceDisplayName   = "OpenSourceBackup Agent"
	serviceDescription   = "Backup agent — polls the OpenSourceBackup Control Plane for jobs and executes Restic backups."
)

// program implements service.Interface so the agent can run as a
// Windows Service, systemd unit, or FreeBSD rc.d service.
type program struct {
	logger *slog.Logger
	cancel context.CancelFunc
}

func (p *program) Start(s service.Service) error {
	go p.run()
	return nil
}

func (p *program) run() {
	logger := newLogger()
	p.logger = logger

	controlPlaneURL := requireEnv(logger, "CONTROL_PLANE_URL")
	resticPassword  := os.Getenv("RESTIC_PASSWORD")
	resticRepo      := os.Getenv("RESTIC_REPO")

	if resticPassword == "" || resticRepo == "" {
		logger.Warn("RESTIC_PASSWORD / RESTIC_REPO not set — backup/restore jobs will fail if triggered")
	}

	restoreRoot := os.Getenv("RESTORE_TEST_ROOT")
	if restoreRoot == "" {
		restoreRoot = "data/restore-tests"
	}

	poll := defaultPollInterval
	if v := os.Getenv("AGENT_POLL_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			poll = d
		}
	}

	token, err := resolveToken(logger, controlPlaneURL)
	if err != nil {
		logger.Error("failed to obtain agent token", "error", err)
		return
	}

	skipTLS := os.Getenv("AGENT_TLS_SKIP_VERIFY") == "true"
	if skipTLS {
		logger.Warn("TLS verification disabled — dev only")
	}

	// The outbox must live in a persistent, writable directory — not the process
	// cwd (systemd often runs the agent from /). Default next to the token file.
	outboxDir := os.Getenv("AGENT_OUTBOX_DIR")
	if outboxDir == "" {
		if tf := os.Getenv("AGENT_TOKEN_FILE"); tf != "" {
			outboxDir = filepath.Join(filepath.Dir(tf), "outbox")
		} else {
			outboxDir = filepath.Join("data", "outbox")
		}
	}

	cp := agentclient.New(controlPlaneURL, token, skipTLS)
	a := agent.New(agent.Config{
		PollInterval:    poll,
		ResticBin:       os.Getenv("RESTIC_BIN"),
		ResticPassword:  resticPassword,
		ResticRepo:      resticRepo,
		RestoreTestRoot: restoreRoot,
		OutboxDir:       outboxDir,
	}, cp, logger)

	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel

	if err := a.Run(ctx); err != nil {
		logger.Error("agent stopped with error", "error", err)
	}
}

func (p *program) Stop(s service.Service) error {
	if p.cancel != nil {
		p.cancel()
	}
	return nil
}

func main() {
	logger := newLogger()

	svcConfig := newServiceConfig()

	prg := &program{}
	svc, err := service.New(prg, svcConfig)
	if err != nil {
		logger.Error("service setup failed", "error", err)
		os.Exit(1)
	}

	// Handle service control commands
	if len(os.Args) > 1 {
		cmd := strings.ToLower(os.Args[1])
		switch cmd {
		case "install":
			if runtime.GOOS == "windows" && svcConfig.UserName == "" {
				fmt.Fprintln(os.Stderr,
					"WARNING: installing as LocalSystem — a network/SMB repository (\\\\server\\share) "+
						"will NOT be reachable. Set OSB_SERVICE_USER and OSB_SERVICE_PASSWORD to run the "+
						"service under a user account.")
			}
			if err := svc.Install(); err != nil {
				fmt.Fprintf(os.Stderr, "Install failed: %v\n", err)
				os.Exit(1)
			}
			// Auto-restart on crash so a transient failure doesn't stop backups.
			configureServiceRecovery()
			fmt.Println("✓ Service installed successfully")
			if svcConfig.UserName != "" {
				fmt.Printf("  Runs as: %s\n", svcConfig.UserName)
			}
			fmt.Printf("  Start with: %s start\n", os.Args[0])
			return
		case "uninstall":
			if err := svc.Uninstall(); err != nil {
				fmt.Fprintf(os.Stderr, "Uninstall failed: %v\n", err)
				os.Exit(1)
			}
			fmt.Println("✓ Service uninstalled")
			return
		case "start":
			if err := svc.Start(); err != nil {
				fmt.Fprintf(os.Stderr, "Start failed: %v\n", err)
				os.Exit(1)
			}
			fmt.Println("✓ Service started")
			return
		case "stop":
			if err := svc.Stop(); err != nil {
				fmt.Fprintf(os.Stderr, "Stop failed: %v\n", err)
				os.Exit(1)
			}
			fmt.Println("✓ Service stopped")
			return
		case "restart":
			if err := svc.Restart(); err != nil {
				fmt.Fprintf(os.Stderr, "Restart failed: %v\n", err)
				os.Exit(1)
			}
			fmt.Println("✓ Service restarted")
			return
		case "status":
			status, err := svc.Status()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Status error: %v\n", err)
				os.Exit(1)
			}
			switch status {
			case service.StatusRunning:
				fmt.Println("● Service is RUNNING")
			case service.StatusStopped:
				fmt.Println("○ Service is STOPPED")
			default:
				fmt.Println("? Service status unknown")
			}
			return
		case "run":
			// fall through to interactive run below
		default:
			fmt.Fprintf(os.Stderr, "Unknown command: %s\n", cmd)
			printUsage(os.Args[0])
			os.Exit(1)
		}
	}

	// No args (or "run"): decide between console and service-managed start.
	//
	// service.Interactive() is false ONLY when the process was launched by a
	// service manager (Windows SCM, systemd, rc.d). In that case we must hand
	// control to svc.Run() so the agent registers with the manager — otherwise
	// the manager's start request times out and the service is killed.
	//
	// When started from a console, a Scheduled Task, or an autostart Run-key,
	// Interactive() is true and we run in the foreground with signal handling.
	if service.Interactive() {
		runInteractive(logger)
		return
	}

	if err := svc.Run(); err != nil {
		logger.Error("service run error", "error", err)
		os.Exit(1)
	}
}

func runInteractive(logger *slog.Logger) {
	controlPlaneURL := requireEnv(logger, "CONTROL_PLANE_URL")
	resticPassword  := os.Getenv("RESTIC_PASSWORD")
	resticRepo      := os.Getenv("RESTIC_REPO")

	if resticPassword == "" || resticRepo == "" {
		logger.Warn("RESTIC_PASSWORD / RESTIC_REPO not set — backup/restore jobs will fail if triggered")
	}

	restoreRoot := os.Getenv("RESTORE_TEST_ROOT")
	if restoreRoot == "" {
		restoreRoot = "data/restore-tests"
	}

	poll := defaultPollInterval
	if v := os.Getenv("AGENT_POLL_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			poll = d
		}
	}

	token, err := resolveToken(logger, controlPlaneURL)
	if err != nil {
		logger.Error("failed to obtain agent token", "error", err)
		os.Exit(1)
	}

	skipTLS := os.Getenv("AGENT_TLS_SKIP_VERIFY") == "true"
	// The outbox must live in a persistent, writable directory — not the process
	// cwd (systemd often runs the agent from /). Default next to the token file.
	outboxDir := os.Getenv("AGENT_OUTBOX_DIR")
	if outboxDir == "" {
		if tf := os.Getenv("AGENT_TOKEN_FILE"); tf != "" {
			outboxDir = filepath.Join(filepath.Dir(tf), "outbox")
		} else {
			outboxDir = filepath.Join("data", "outbox")
		}
	}

	cp := agentclient.New(controlPlaneURL, token, skipTLS)
	a := agent.New(agent.Config{
		PollInterval:    poll,
		ResticBin:       os.Getenv("RESTIC_BIN"),
		ResticPassword:  resticPassword,
		ResticRepo:      resticRepo,
		RestoreTestRoot: restoreRoot,
		OutboxDir:       outboxDir,
	}, cp, logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := a.Run(ctx); err != nil {
		logger.Error("agent stopped with error", "error", err)
		os.Exit(1)
	}
}

// newLogger writes structured logs to AGENT_LOG_FILE when set (so the agent
// remains observable when running as a Windows service, where stdout is
// discarded), otherwise to stdout. The file is opened in append mode.
func newLogger() *slog.Logger {
	if path := os.Getenv("AGENT_LOG_FILE"); path != "" {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err == nil {
			return slog.New(slog.NewJSONHandler(f, nil))
		}
		// Fall through to stdout if the file cannot be opened.
		slog.New(slog.NewJSONHandler(os.Stdout, nil)).
			Warn("could not open AGENT_LOG_FILE, logging to stdout", "path", path, "error", err)
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, nil))
}

// newServiceConfig builds the service definition. On Windows it runs the service
// under a user account (from OSB_SERVICE_USER / OSB_SERVICE_PASSWORD, defaulting
// to the current user) because a LocalSystem service has no network credential
// and cannot reach an SMB/UNC repository. The password is read from the
// environment — set securely by the installer — never passed on the command line.
// The account also needs the "Log on as a service" right (the installer grants it).
func newServiceConfig() *service.Config {
	cfg := &service.Config{
		Name:        serviceName,
		DisplayName: serviceDisplayName,
		Description: serviceDescription,
		EnvVars:     buildEnvVars(),
		Option:      service.KeyValue{},
	}
	if runtime.GOOS == "windows" {
		user := os.Getenv("OSB_SERVICE_USER")
		if user == "" {
			if u := os.Getenv("USERNAME"); u != "" {
				if d := os.Getenv("USERDOMAIN"); d != "" {
					user = d + `\` + u
				} else {
					user = u
				}
			}
		}
		if pw := os.Getenv("OSB_SERVICE_PASSWORD"); user != "" && pw != "" {
			cfg.UserName = user
			cfg.Option["Password"] = pw
		}
	}
	return cfg
}

// configureServiceRecovery tells the Windows SCM to restart the service on crash
// (3 attempts: after 5s, 10s, 30s; failure counter reset after 24h). Best-effort.
func configureServiceRecovery() {
	if runtime.GOOS != "windows" {
		return
	}
	_ = exec.Command("sc.exe", "failure", serviceName,
		"reset=", "86400",
		"actions=", "restart/5000/restart/10000/restart/30000").Run()
}

// buildEnvVars collects current env vars that should be passed to the service.
func buildEnvVars() map[string]string {
	vars := map[string]string{}
	keys := []string{
		"CONTROL_PLANE_URL", "RESTIC_PASSWORD", "RESTIC_REPO",
		"RESTIC_BIN", "AGENT_TOKEN_FILE", "AGENT_POLL_INTERVAL",
		"RESTORE_TEST_ROOT", "AGENT_TLS_SKIP_VERIFY", "AGENT_LOG_FILE",
	}
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			vars[k] = v
		}
	}
	return vars
}

func resolveToken(logger *slog.Logger, controlPlaneURL string) (string, error) {
	if t := os.Getenv("AGENT_TOKEN"); t != "" {
		logger.Info("using agent token from AGENT_TOKEN env var")
		return t, nil
	}
	tokenFile := os.Getenv("AGENT_TOKEN_FILE")
	if tokenFile == "" {
		tokenFile = defaultTokenFilePath
	}
	if t, err := agent.LoadToken(tokenFile); err != nil {
		return "", fmt.Errorf("load token file %s: %w", tokenFile, err)
	} else if t != "" {
		logger.Info("using agent token from file", "path", tokenFile)
		return t, nil
	}
	enrollmentToken := os.Getenv("ENROLLMENT_TOKEN")
	if enrollmentToken == "" {
		return "", fmt.Errorf(
			"no agent token available — set AGENT_TOKEN, create %s, or set ENROLLMENT_TOKEN",
			tokenFile,
		)
	}
	logger.Info("no agent token found — enrolling with enrollment token")
	enrollClient := agentclient.New(controlPlaneURL, "")
	agentToken, err := enrollClient.Enroll(context.Background(), enrollmentToken)
	if err != nil {
		return "", fmt.Errorf("enrollment failed: %w", err)
	}
	if err := agent.SaveToken(tokenFile, agentToken); err != nil {
		logger.Warn("could not save agent token to file", "path", tokenFile, "error", err)
	} else {
		logger.Info("agent token saved", "path", tokenFile)
	}
	return agentToken, nil
}

func requireEnv(logger *slog.Logger, key string) string {
	v := os.Getenv(key)
	if v == "" {
		logger.Error("required environment variable not set", "key", key)
		os.Exit(1)
	}
	return v
}

func printUsage(bin string) {
	name := filepath.Base(bin)
	fmt.Printf(`
OpenSourceBackup Agent — Usage:

  %s                  Run interactively (foreground)
  %s run              Run interactively (foreground)
  %s install          Install as system service
  %s uninstall        Remove system service
  %s start            Start the service
  %s stop             Stop the service
  %s restart          Restart the service
  %s status           Show service status

Environment variables (required):
  CONTROL_PLANE_URL   URL of the Control Plane (e.g. http://<control-plane-host>:8080)
  RESTIC_PASSWORD     Encryption password for backups
  RESTIC_REPO         Backup destination (e.g. /mnt/nas/backups or Z:\Backups)

Optional:
  ENROLLMENT_TOKEN    One-time token to enroll (first run only)
  AGENT_TOKEN_FILE    Path to saved token (default: data/agent-token)
  AGENT_POLL_INTERVAL Poll interval (default: 30s)
  RESTORE_TEST_ROOT   Sandbox directory for restore tests
  AGENT_TLS_SKIP_VERIFY=true  Skip TLS verification (dev only)
`, name, name, name, name, name, name, name, name)
}
