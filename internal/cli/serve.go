package cli

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"go.uber.org/zap/zapio"

	"ipmitool-microservice/internal/api"
	"ipmitool-microservice/internal/config"
	"ipmitool-microservice/internal/ipmi"
	"ipmitool-microservice/internal/logging"
)

func newServeCommand(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Locate or install ipmitool and start the API",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			logger := logging.New(cmd.ErrOrStderr())
			defer logger.Sync()
			cfg, err := config.Load(*configPath)
			if err != nil {
				return err
			}
			tlsConfig, err := loadTLS(cfg.Server.TLS)
			if err != nil {
				return err
			}
			installerOutput := &zapio.Writer{Log: logger, Level: zap.InfoLevel}
			defer installerOutput.Close()
			binary, err := ipmi.EnsureBinary(cmd.Context(), cfg.IPMITool.Binary, installerOutput)
			if err != nil {
				return err
			}
			client, err := ipmi.New(cmd.Context(), binary, cfg.IPMITool.Timeout, cfg.IPMITool.MaxOutputBytes)
			if err != nil {
				return err
			}
			server := &http.Server{
				Addr:              cfg.Server.Address,
				Handler:           api.New(cfg, client, logger),
				TLSConfig:         tlsConfig,
				ErrorLog:          zap.NewStdLog(logger),
				ReadHeaderTimeout: 5 * time.Second,
				ReadTimeout:       15 * time.Second,
				WriteTimeout:      cfg.IPMITool.Timeout + 20*time.Second,
				IdleTimeout:       time.Minute,
				BaseContext:       func(net.Listener) context.Context { return cmd.Context() },
			}
			return serve(cmd.Context(), server, client.Version(), logger)
		},
	}
}

func loadTLS(settings config.TLS) (*tls.Config, error) {
	if !settings.Enabled {
		return nil, nil
	}
	certificate, err := tls.LoadX509KeyPair(settings.CertFile, settings.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("load TLS certificate and private key: %w", err)
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{certificate},
	}, nil
}

func serve(ctx context.Context, server *http.Server, version string, logger *zap.Logger) error {
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return fmt.Errorf("start API: %w", err)
	}
	logger.Info("API started", zap.String("address", listener.Addr().String()),
		zap.Bool("tls", server.TLSConfig != nil), zap.String("ipmitool_version", version))

	done := make(chan error, 1)
	go func() {
		if server.TLSConfig != nil {
			done <- server.ServeTLS(listener, "", "")
		} else {
			done <- server.Serve(listener)
		}
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			_ = server.Close()
			return fmt.Errorf("stop API: %w", err)
		}
		logger.Info("API stopped")
		return nil
	}
}
