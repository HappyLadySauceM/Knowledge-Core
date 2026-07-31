package log

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"github.com/cloudwego/hertz/pkg/common/hlog"
	"github.com/cloudwego/kitex/pkg/klog"
)

// InstallCloudWeGo routes Hertz and Kitex framework logs through the service's
// structured logger. The application retains ownership of the output writer;
// framework SetOutput calls are intentionally ignored.
func InstallCloudWeGo(logger *slog.Logger, level *slog.LevelVar) {
	if logger == nil {
		logger = slog.Default()
	}
	if level == nil {
		level = new(slog.LevelVar)
		level.Set(slog.LevelInfo)
	}
	hlog.SetLogger(&HertzLogger{frameworkLogger: &frameworkLogger{
		logger: logger.With(slog.String("component", "hertz")),
		level:  level,
	}})
	klog.SetLogger(&KitexLogger{frameworkLogger: &frameworkLogger{
		logger: logger.With(slog.String("component", "kitex")),
		level:  level,
	}})
}

type frameworkLogger struct {
	logger *slog.Logger
	level  *slog.LevelVar
}

func (l *frameworkLogger) log(ctx context.Context, level slog.Level, message string) {
	if ctx == nil {
		ctx = context.Background()
	}
	l.logger.Log(ctx, level, message)
}

func (l *frameworkLogger) Trace(v ...any) {
	l.log(context.Background(), slog.LevelDebug, fmt.Sprint(v...))
}
func (l *frameworkLogger) Debug(v ...any) {
	l.log(context.Background(), slog.LevelDebug, fmt.Sprint(v...))
}
func (l *frameworkLogger) Info(v ...any) {
	l.log(context.Background(), slog.LevelInfo, fmt.Sprint(v...))
}
func (l *frameworkLogger) Notice(v ...any) {
	l.log(context.Background(), slog.LevelInfo, fmt.Sprint(v...))
}
func (l *frameworkLogger) Warn(v ...any) {
	l.log(context.Background(), slog.LevelWarn, fmt.Sprint(v...))
}
func (l *frameworkLogger) Error(v ...any) {
	l.log(context.Background(), slog.LevelError, fmt.Sprint(v...))
}
func (l *frameworkLogger) Fatal(v ...any) {
	l.log(context.Background(), slog.LevelError, fmt.Sprint(v...))
}

func (l *frameworkLogger) Tracef(format string, values ...any) {
	l.Trace(fmt.Sprintf(format, values...))
}
func (l *frameworkLogger) Debugf(format string, values ...any) {
	l.Debug(fmt.Sprintf(format, values...))
}
func (l *frameworkLogger) Infof(format string, values ...any) { l.Info(fmt.Sprintf(format, values...)) }
func (l *frameworkLogger) Noticef(format string, values ...any) {
	l.Notice(fmt.Sprintf(format, values...))
}
func (l *frameworkLogger) Warnf(format string, values ...any) { l.Warn(fmt.Sprintf(format, values...)) }
func (l *frameworkLogger) Errorf(format string, values ...any) {
	l.Error(fmt.Sprintf(format, values...))
}
func (l *frameworkLogger) Fatalf(format string, values ...any) {
	l.Fatal(fmt.Sprintf(format, values...))
}

func (l *frameworkLogger) CtxTracef(ctx context.Context, format string, values ...any) {
	l.log(ctx, slog.LevelDebug, fmt.Sprintf(format, values...))
}
func (l *frameworkLogger) CtxDebugf(ctx context.Context, format string, values ...any) {
	l.log(ctx, slog.LevelDebug, fmt.Sprintf(format, values...))
}
func (l *frameworkLogger) CtxInfof(ctx context.Context, format string, values ...any) {
	l.log(ctx, slog.LevelInfo, fmt.Sprintf(format, values...))
}
func (l *frameworkLogger) CtxNoticef(ctx context.Context, format string, values ...any) {
	l.log(ctx, slog.LevelInfo, fmt.Sprintf(format, values...))
}
func (l *frameworkLogger) CtxWarnf(ctx context.Context, format string, values ...any) {
	l.log(ctx, slog.LevelWarn, fmt.Sprintf(format, values...))
}
func (l *frameworkLogger) CtxErrorf(ctx context.Context, format string, values ...any) {
	l.log(ctx, slog.LevelError, fmt.Sprintf(format, values...))
}
func (l *frameworkLogger) CtxFatalf(ctx context.Context, format string, values ...any) {
	l.log(ctx, slog.LevelError, fmt.Sprintf(format, values...))
}

type HertzLogger struct{ *frameworkLogger }

func (l *HertzLogger) SetLevel(level hlog.Level) { l.level.Set(frameworkLevel(int(level))) }
func (l *HertzLogger) SetOutput(io.Writer)       {}

type KitexLogger struct{ *frameworkLogger }

func (l *KitexLogger) SetLevel(level klog.Level) { l.level.Set(frameworkLevel(int(level))) }
func (l *KitexLogger) SetOutput(io.Writer)       {}

func frameworkLevel(level int) slog.Level {
	switch {
	case level <= 1:
		return slog.LevelDebug
	case level <= 3:
		return slog.LevelInfo
	case level == 4:
		return slog.LevelWarn
	default:
		return slog.LevelError
	}
}

var (
	_ hlog.FullLogger = (*HertzLogger)(nil)
	_ klog.FullLogger = (*KitexLogger)(nil)
)
