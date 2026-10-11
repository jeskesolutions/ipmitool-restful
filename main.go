package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"ipmitool-microservice/internal/cli"
	"ipmitool-microservice/internal/logging"
)

func main() {
	gin.SetMode(gin.ReleaseMode)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := cli.NewCommand().ExecuteContext(ctx); err != nil {
		logger := logging.New(os.Stderr)
		logger.Error("Application failed", zap.Error(err))
		_ = logger.Sync()
		os.Exit(1)
	}
}
