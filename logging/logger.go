package logging

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/go-logr/logr"
	zaplib "go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

const (
	LogLevelDebug = "debug"
	LogLevelInfo  = "info"
	LogLevelWarn  = "warn"
	LogLevelError = "error"
)

var (
	LogOpts = zap.Options{
		TimeEncoder: zapcore.TimeEncoderOfLayout(time.RFC3339),
		Development: true,
		EncoderConfigOptions: []zap.EncoderConfigOption{
			func(ec *zapcore.EncoderConfig) {
				ec.LevelKey = "severity"
				ec.MessageKey = "message"
			},
		},
	}
)

func NewLogger(logLevel string, logFormat string) (logr.Logger, error) {

	if !validLogFormat(logFormat) {
		return logr.Logger{}, errors.New("invalid log format specified")
	}

	o := LogOpts
	o.EncoderConfigOptions = make([]zap.EncoderConfigOption, len(LogOpts.EncoderConfigOptions))
	copy(o.EncoderConfigOptions, LogOpts.EncoderConfigOptions)

	if logFormat == "json" {
		o.Development = false
		o.TimeEncoder = nil
	}

	switch logLevel {
	case LogLevelDebug:
		lvl := zaplib.NewAtomicLevelAt(zaplib.DebugLevel) // maps to logr's V(1)
		o.Level = &lvl
	case LogLevelInfo:
		lvl := zaplib.NewAtomicLevelAt(zaplib.InfoLevel)
		o.Level = &lvl
	case LogLevelWarn:
		lvl := zaplib.NewAtomicLevelAt(zaplib.WarnLevel)
		o.Level = &lvl
	case LogLevelError:
		lvl := zaplib.NewAtomicLevelAt(zaplib.ErrorLevel)
		o.Level = &lvl
	default:
		// We use bitsize of 8 as zapcore.Level is a type alias to int8
		levelInt, err := strconv.ParseInt(logLevel, 10, 8)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to parse --log-level=%s: %v", logLevel, err)
			os.Exit(1)
		}
		// For example, --log-level=debug a.k.a --log-level=-1 maps to zaplib.DebugLevel, which is associated to logr's V(1)
		// --log-level=-2 maps the specific custom log level that is associated to logr's V(2).
		level := zapcore.Level(levelInt)
		atomicLevel := zaplib.NewAtomicLevelAt(level)
		o.Level = &atomicLevel
	}
	return zap.New(zap.UseFlagOptions(&o)), nil
}

func validLogFormat(logFormat string) bool {
	validFormat := []string{"text", "json"}
	for _, v := range validFormat {
		if v == logFormat {
			return true
		}
	}
	return false
}
