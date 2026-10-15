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
	if err := run(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	repository, err := store.Open(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer repository.Close()
	if err := repository.Migrate(ctx); err != nil {
		return err
	}
	bootstrapResult, err := bootstrap.Owner(ctx, repository, bootstrap.Config{
		PasswordFile:          envOrDefault("ADMIN_PASSWORD_FILE", "/run/secrets/sim_deck_admin_password"),
		RecoveryPublicKeyFile: envOrDefault("RECOVERY_PUBLIC_KEY_FILE", "/run/secrets/sim_deck_recovery_public_key"),
	})
	if err != nil {
		return fmt.Errorf("bootstrap owner: %w", err)
	}
	if bootstrapResult.GeneratedPassword != "" {
		slog.Warn("generated initial admin password; save it now because it will not be shown again", "username", "admin", "password", bootstrapResult.GeneratedPassword)
	}

	udpPort, err := api.ParsePort(os.Getenv("MEDIA_UDP_PORT"), 49152)
	if err != nil {
		return err
	}
	tcpPort, err := api.ParsePort(os.Getenv("MEDIA_TCP_PORT"), 49153)
	if err != nil {
		return err
	}

	server := &http.Server{
		Addr:              envOrDefault("HTTP_ADDR", ":8080"),
		Handler:           api.Handler(repository, api.Config{PublicBaseURL: os.Getenv("PUBLIC_BASE_URL"), MediaUDPPort: udpPort, MediaTCPPort: tcpPort}),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	result := make(chan error, 1)
	go func() {
		certificate := os.Getenv("TLS_CERT_FILE")
		privateKey := os.Getenv("TLS_KEY_FILE")
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

func envOrDefault(name string, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
