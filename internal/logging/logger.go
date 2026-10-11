package logging

import (
	"io"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func New(output io.Writer) *zap.Logger {
	encoder := zap.NewProductionEncoderConfig()
	encoder.EncodeTime = zapcore.ISO8601TimeEncoder
	return zap.New(zapcore.NewCore(
		zapcore.NewJSONEncoder(encoder),
		zapcore.Lock(zapcore.AddSync(output)),
		zap.InfoLevel,
	))
}
