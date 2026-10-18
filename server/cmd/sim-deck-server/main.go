package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"sim-deck-server/internal/api"
	"sim-deck-server/internal/apns"
	"sim-deck-server/internal/bootstrap"
	"sim-deck-server/internal/push"
	"sim-deck-server/internal/store"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, productionDependencies()); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

type repository interface {
	api.Repository
	bootstrap.OwnerStore
	push.Repository
	InitializeSchema(context.Context) error
	Close()
}

type httpServer interface {
	ListenAndServe() error
	ListenAndServeTLS(string, string) error
	Shutdown(context.Context) error
}

type backgroundWorker interface {
	Run(context.Context) error
}

type dependencies struct {
	getenv         func(string) string
	readFile       func(string) ([]byte, error)
	openRepository func(context.Context, string) (repository, error)
	bootstrapOwner func(context.Context, bootstrap.OwnerStore, bootstrap.Config) (bootstrap.Result, error)
	newHTTPServer  func(string, http.Handler) httpServer
	newPushWorker  func(push.Repository, apns.Config) (backgroundWorker, error)
}

type runtimeConfig struct {
	databaseURL   string
	publicBaseURL string
	httpAddress   string
	turnURL       string
	turnSecret    []byte
	turnTTL       time.Duration
	certificate   string
	privateKey    string
}

func productionDependencies() dependencies {
	return dependencies{
		getenv:   os.Getenv,
		readFile: os.ReadFile,
		openRepository: func(ctx context.Context, databaseURL string) (repository, error) {
			return store.Open(ctx, databaseURL)
		},
		bootstrapOwner: bootstrap.Owner,
		newHTTPServer: func(address string, handler http.Handler) httpServer {
			return &http.Server{
				Addr:              address,
				Handler:           handler,
				ReadHeaderTimeout: 5 * time.Second,
				IdleTimeout:       60 * time.Second,
			}
		},
		newPushWorker: func(repository push.Repository, config apns.Config) (backgroundWorker, error) {
			client, err := apns.New(config)
			if err != nil {
				return nil, err
			}
			return push.NewWorker(repository, client, push.Config{}), nil
		},
	}
}

func run(ctx context.Context, dependencies dependencies) error {
	getenv := dependencies.getenv
	config, err := loadRuntimeConfig(getenv)
	if err != nil {
		return err
	}
	repository, err := dependencies.openRepository(ctx, config.databaseURL)
	if err != nil {
		return err
	}
	defer repository.Close()
	if err := repository.InitializeSchema(ctx); err != nil {
		return err
	}
	bootstrapResult, err := dependencies.bootstrapOwner(ctx, repository, bootstrap.Config{
		PasswordFile:          envOrDefault(getenv, "ADMIN_PASSWORD_FILE", "/run/secrets/sim_deck_admin_password"),
		RecoveryPublicKeyFile: envOrDefault(getenv, "RECOVERY_PUBLIC_KEY_FILE", "/run/secrets/sim_deck_recovery_public_key"),
	})
	if err != nil {
		return fmt.Errorf("bootstrap owner: %w", err)
	}
	if bootstrapResult.GeneratedPassword != "" {
		slog.Warn("generated initial admin password; save it now because it will not be shown again", "username", "admin", "password", bootstrapResult.GeneratedPassword)
	}

	server := dependencies.newHTTPServer(
		config.httpAddress,
		api.Handler(repository, api.Config{PublicBaseURL: config.publicBaseURL, TURNURL: config.turnURL, TURNSecret: config.turnSecret, TURNTTL: config.turnTTL}),
	)
	worker, err := configuredPushWorker(repository, dependencies)
	if err != nil {
		return err
	}
	runContext, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	serverResult := make(chan error, 1)
	go func() {
		if config.certificate != "" {
			serverResult <- server.ListenAndServeTLS(config.certificate, config.privateKey)
			return
		}
		serverResult <- server.ListenAndServe()
	}()
	var workerResult chan error
	if worker != nil {
		workerResult = make(chan error, 1)
		go func() { workerResult <- worker.Run(runContext) }()
	}

	select {
	case <-ctx.Done():
		cancelRun()
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return errors.Join(
			stopHTTPServer(shutdownContext, server, serverResult),
			waitForWorker(shutdownContext, workerResult),
		)
	case err := <-serverResult:
		cancelRun()
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		workerErr := waitForWorker(shutdownContext, workerResult)
		if errors.Is(err, http.ErrServerClosed) {
			return workerErr
		}
		return errors.Join(fmt.Errorf("serve HTTP: %w", err), workerErr)
	case err := <-workerResult:
		cancelRun()
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		workerError := errors.New("push worker stopped unexpectedly")
		if err != nil {
			workerError = fmt.Errorf("run push worker: %w", err)
		}
		return errors.Join(workerError, stopHTTPServer(shutdownContext, server, serverResult))
	}
}

func loadRuntimeConfig(getenv func(string) string) (runtimeConfig, error) {
	databaseURL := strings.TrimSpace(getenv("DATABASE_URL"))
	if databaseURL == "" {
		return runtimeConfig{}, errors.New("DATABASE_URL is required")
	}
	publicBaseURL, err := api.ParsePublicBaseURL(getenv("PUBLIC_BASE_URL"))
	if err != nil {
		return runtimeConfig{}, err
	}
	certificate := strings.TrimSpace(getenv("TLS_CERT_FILE"))
	privateKey := strings.TrimSpace(getenv("TLS_KEY_FILE"))
	if (certificate == "") != (privateKey == "") {
		return runtimeConfig{}, errors.New("serve HTTP: TLS_CERT_FILE and TLS_KEY_FILE must be configured together")
	}
	turnURL := strings.TrimSpace(getenv("TURN_URL"))
	turnSecretFile := strings.TrimSpace(getenv("TURN_SHARED_SECRET_FILE"))
	var turnSecret []byte
	if turnURL != "" || turnSecretFile != "" {
		if turnURL == "" || turnSecretFile == "" {
			return runtimeConfig{}, errors.New("TURN_URL and TURN_SHARED_SECRET_FILE must be configured together")
		}
		turnSecret, err = os.ReadFile(turnSecretFile)
		if err != nil {
			return runtimeConfig{}, fmt.Errorf("read TURN shared secret: %w", err)
		}
		turnSecret = []byte(strings.TrimSpace(string(turnSecret)))
		if len(turnSecret) < 16 {
			return runtimeConfig{}, errors.New("TURN shared secret must contain at least 16 bytes")
		}
	}
	return runtimeConfig{
		databaseURL:   databaseURL,
		publicBaseURL: publicBaseURL,
		httpAddress:   envOrDefault(getenv, "HTTP_ADDR", ":8080"),
		turnURL:       turnURL,
		turnSecret:    turnSecret,
		turnTTL:       10 * time.Minute,
		certificate:   certificate,
		privateKey:    privateKey,
	}, nil
}

func stopHTTPServer(ctx context.Context, server httpServer, result <-chan error) error {
	shutdownErr := server.Shutdown(ctx)
	var serveErr error
	select {
	case serveErr = <-result:
		if errors.Is(serveErr, http.ErrServerClosed) {
			serveErr = nil
		} else if serveErr != nil {
			serveErr = fmt.Errorf("serve HTTP: %w", serveErr)
		}
	case <-ctx.Done():
		serveErr = fmt.Errorf("wait for HTTP server: %w", ctx.Err())
	}
	return errors.Join(shutdownErr, serveErr)
}

func waitForWorker(ctx context.Context, result <-chan error) error {
	if result == nil {
		return nil
	}
	select {
	case err := <-result:
		if err != nil {
			return fmt.Errorf("stop push worker: %w", err)
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("wait for push worker: %w", ctx.Err())
	}
}

func configuredPushWorker(repository push.Repository, dependencies dependencies) (backgroundWorker, error) {
	values := map[string]string{
		"APNS_KEY_ID":           strings.TrimSpace(dependencies.getenv("APNS_KEY_ID")),
		"APNS_TEAM_ID":          strings.TrimSpace(dependencies.getenv("APNS_TEAM_ID")),
		"APNS_TOPIC":            strings.TrimSpace(dependencies.getenv("APNS_TOPIC")),
		"APNS_PRIVATE_KEY_FILE": strings.TrimSpace(dependencies.getenv("APNS_PRIVATE_KEY_FILE")),
	}
	configured := false
	for _, value := range values {
		configured = configured || value != ""
	}
	if !configured {
		return nil, nil
	}
	for name, value := range values {
		if value == "" {
			return nil, fmt.Errorf("%s is required when APNs is configured", name)
		}
	}
	privateKey, err := dependencies.readFile(values["APNS_PRIVATE_KEY_FILE"])
	if err != nil {
		return nil, fmt.Errorf("read APNs private key: %w", err)
	}
	worker, err := dependencies.newPushWorker(repository, apns.Config{
		KeyID:         values["APNS_KEY_ID"],
		TeamID:        values["APNS_TEAM_ID"],
		Topic:         values["APNS_TOPIC"],
		PrivateKeyPEM: privateKey,
	})
	if err != nil {
		return nil, fmt.Errorf("configure APNs worker: %w", err)
	}
	return worker, nil
}

func envOrDefault(getenv func(string) string, name string, fallback string) string {
	if value := getenv(name); value != "" {
		return value
	}
	return fallback
}
