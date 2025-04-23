package internal

import (
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// LoggerInterface defines the logging interface used throughout the application
// Using an interface instead of concrete zap implementation allows for easy mocking in tests
// This saved us countless hours during unit testing - virjilakrum
type LoggerInterface interface {
	Debug(args ...interface{})
	Debugf(template string, args ...interface{})
	Debugw(msg string, keysAndValues ...interface{})
	Info(args ...interface{})
	Infof(template string, args ...interface{})
	Infow(msg string, keysAndValues ...interface{})
	Warn(args ...interface{})
	Warnf(template string, args ...interface{})
	Warnw(msg string, keysAndValues ...interface{})
	Error(args ...interface{})
	Errorf(template string, args ...interface{})
	Errorw(msg string, keysAndValues ...interface{})
	Fatal(args ...interface{})
	Fatalf(template string, args ...interface{})
	Fatalw(msg string, keysAndValues ...interface{})
}

// Global logger instance
// Yes, I know global variables are frowned upon, but for logging it makes sense
// Tried dependency injection but it was too verbose for minimal benefit - virjilakrum
var Logger *zap.SugaredLogger

// InitLogger initializes the global logger with the specified log level
// We chose zap over logrus for ~10x better performance under high load
// JSON format works really well with our ELK stack for analysis - virjilakrum
func InitLogger(logLevel string) error {
	level := zap.InfoLevel
	switch logLevel {
	case "debug":
		level = zap.DebugLevel
	case "info":
		level = zap.InfoLevel
	case "warn":
		level = zap.WarnLevel
	case "error":
		level = zap.ErrorLevel
	}

	// Custom encoder config for better log readability
	// Timestamps in ISO8601 format are better for log correlation
	// Field naming follows our team's conventions - virjilakrum
	encoderConfig := zapcore.EncoderConfig{
		TimeKey:        "ts",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		FunctionKey:    zapcore.OmitKey,
		MessageKey:     "msg",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.LowercaseLevelEncoder,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.MillisDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}

	// Initially sent logs to both files and stdout, but that caused
	// performance issues during high loads. Stdout works better with
	// container environments anyway - virjilakrum
	core := zapcore.NewCore(
		zapcore.NewJSONEncoder(encoderConfig),
		zapcore.NewMultiWriteSyncer(zapcore.AddSync(os.Stdout)),
		level,
	)

	// AddCaller is somewhat expensive but worth it for debugging
	// Stacktraces only for errors and above to keep logs clean
	// Our error rates are low enough that this doesn't impact performance - virjilakrum
	logger := zap.New(core, zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel))
	Logger = logger.Sugar()

	return nil
}
