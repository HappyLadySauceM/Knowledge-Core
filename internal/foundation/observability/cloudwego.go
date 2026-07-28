package observability

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"github.com/cloudwego/hertz/pkg/common/hlog"
	"github.com/cloudwego/kitex/pkg/klog"
)

type frameworkLogger struct {
	logger *slog.Logger
	level  *slog.LevelVar
}

func (l *frameworkLogger) log(ctx context.Context, level slog.Level, message string) {
	l.logger.Log(ctx, level, message)
}

func (l *frameworkLogger) Trace(v ...interface{}) {
	l.log(context.Background(), slog.LevelDebug, fmt.Sprint(v...))
}
func (l *frameworkLogger) Debug(v ...interface{}) {
	l.log(context.Background(), slog.LevelDebug, fmt.Sprint(v...))
}
func (l *frameworkLogger) Info(v ...interface{}) {
	l.log(context.Background(), slog.LevelInfo, fmt.Sprint(v...))
}
func (l *frameworkLogger) Notice(v ...interface{}) {
	l.log(context.Background(), slog.LevelInfo, fmt.Sprint(v...))
}
func (l *frameworkLogger) Warn(v ...interface{}) {
	l.log(context.Background(), slog.LevelWarn, fmt.Sprint(v...))
}
func (l *frameworkLogger) Error(v ...interface{}) {
	l.log(context.Background(), slog.LevelError, fmt.Sprint(v...))
}
func (l *frameworkLogger) Fatal(v ...interface{}) {
	l.log(context.Background(), slog.LevelError, fmt.Sprint(v...))
}

func (l *frameworkLogger) Tracef(format string, v ...interface{}) { l.Trace(fmt.Sprintf(format, v...)) }
func (l *frameworkLogger) Debugf(format string, v ...interface{}) { l.Debug(fmt.Sprintf(format, v...)) }
func (l *frameworkLogger) Infof(format string, v ...interface{})  { l.Info(fmt.Sprintf(format, v...)) }
func (l *frameworkLogger) Noticef(format string, v ...interface{}) {
	l.Notice(fmt.Sprintf(format, v...))
}
func (l *frameworkLogger) Warnf(format string, v ...interface{})  { l.Warn(fmt.Sprintf(format, v...)) }
func (l *frameworkLogger) Errorf(format string, v ...interface{}) { l.Error(fmt.Sprintf(format, v...)) }
func (l *frameworkLogger) Fatalf(format string, v ...interface{}) { l.Fatal(fmt.Sprintf(format, v...)) }

func (l *frameworkLogger) CtxTracef(ctx context.Context, format string, v ...interface{}) {
	l.log(ctx, slog.LevelDebug, fmt.Sprintf(format, v...))
}
func (l *frameworkLogger) CtxDebugf(ctx context.Context, format string, v ...interface{}) {
	l.log(ctx, slog.LevelDebug, fmt.Sprintf(format, v...))
}
func (l *frameworkLogger) CtxInfof(ctx context.Context, format string, v ...interface{}) {
	l.log(ctx, slog.LevelInfo, fmt.Sprintf(format, v...))
}
func (l *frameworkLogger) CtxNoticef(ctx context.Context, format string, v ...interface{}) {
	l.log(ctx, slog.LevelInfo, fmt.Sprintf(format, v...))
}
func (l *frameworkLogger) CtxWarnf(ctx context.Context, format string, v ...interface{}) {
	l.log(ctx, slog.LevelWarn, fmt.Sprintf(format, v...))
}
func (l *frameworkLogger) CtxErrorf(ctx context.Context, format string, v ...interface{}) {
	l.log(ctx, slog.LevelError, fmt.Sprintf(format, v...))
}
func (l *frameworkLogger) CtxFatalf(ctx context.Context, format string, v ...interface{}) {
	l.log(ctx, slog.LevelError, fmt.Sprintf(format, v...))
}

type HertzLogger struct{ *frameworkLogger }

func (l *HertzLogger) SetLevel(level hlog.Level) { l.level.Set(mapFrameworkLevel(int(level))) }
func (l *HertzLogger) SetOutput(io.Writer)       {}

type KitexLogger struct{ *frameworkLogger }

func (l *KitexLogger) SetLevel(level klog.Level) { l.level.Set(mapFrameworkLevel(int(level))) }
func (l *KitexLogger) SetOutput(io.Writer)       {}

func installCloudWeGoLoggers(runtime *Runtime) {
	hlog.SetLogger(&HertzLogger{frameworkLogger: &frameworkLogger{
		logger: runtime.Logger().With("component", "hertz"),
		level:  runtime.level,
	}})
	klog.SetLogger(&KitexLogger{frameworkLogger: &frameworkLogger{
		logger: runtime.Logger().With("component", "kitex"),
		level:  runtime.level,
	}})
}

func mapFrameworkLevel(level int) slog.Level {
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

var _ hlog.FullLogger = (*HertzLogger)(nil)
var _ klog.FullLogger = (*KitexLogger)(nil)
