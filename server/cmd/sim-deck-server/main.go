package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"sim-deck-server/internal/api"
	"sim-deck-server/internal/bootstrap"
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
	Migrate(context.Context) error
	Close()
}

type httpServer interface {
	ListenAndServe() error
	ListenAndServeTLS(string, string) error
	Shutdown(context.Context) error
}

type dependencies struct {
	getenv         func(string) string
	openRepository func(context.Context, string) (repository, error)
	bootstrapOwner func(context.Context, bootstrap.OwnerStore, bootstrap.Config) (bootstrap.Result, error)
	newHTTPServer  func(string, http.Handler) httpServer
}

func productionDependencies() dependencies {
	return dependencies{
		getenv: os.Getenv,
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
	}
}

func run(ctx context.Context, dependencies dependencies) error {
	getenv := dependencies.getenv

	databaseURL := getenv("DATABASE_URL")
	if databaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	repository, err := dependencies.openRepository(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer repository.Close()
	if err := repository.Migrate(ctx); err != nil {
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

	udpPort, err := api.ParsePort(getenv("MEDIA_UDP_PORT"), 49152)
	if err != nil {
		return err
	}
	tcpPort, err := api.ParsePort(getenv("MEDIA_TCP_PORT"), 49153)
	if err != nil {
		return err
	}

	server := dependencies.newHTTPServer(
		envOrDefault(getenv, "HTTP_ADDR", ":8080"),
		api.Handler(repository, api.Config{PublicBaseURL: getenv("PUBLIC_BASE_URL"), MediaUDPPort: udpPort, MediaTCPPort: tcpPort}),
	)

	result := make(chan error, 1)
	go func() {
		certificate := getenv("TLS_CERT_FILE")
		privateKey := getenv("TLS_KEY_FILE")
		if certificate != "" || privateKey != "" {
			if certificate == "" || privateKey == "" {
				result <- errors.New("TLS_CERT_FILE and TLS_KEY_FILE must be configured together")
				return
			}
			result <- server.ListenAndServeTLS(certificate, privateKey)
			return
		}
		result <- server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdownContext)
	case err := <-result:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve HTTP: %w", err)
	}
}

func envOrDefault(getenv func(string) string, name string, fallback string) string {
	if value := getenv(name); value != "" {
		return value
	}
	return fallback
}
