package observability

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"

	"github.com/cloudwego/hertz/pkg/common/hlog"
	"github.com/cloudwego/kitex/pkg/klog"
)

type logCore struct {
	mu      sync.RWMutex
	output  io.Writer
	level   *slog.LevelVar
	service string
	logger  *slog.Logger
}

func newLogCore(output io.Writer, level string, service string) *logCore {
	var parsed slog.Level
	if err := parsed.UnmarshalText([]byte(level)); err != nil {
		parsed = slog.LevelInfo
	}
	levelVar := &slog.LevelVar{}
	levelVar.Set(parsed)
	core := &logCore{output: output, level: levelVar, service: service}
	core.rebuild()
	return core
}

func (c *logCore) rebuild() {
	c.logger = slog.New(slog.NewJSONHandler(c.output, &slog.HandlerOptions{Level: c.level})).With(
		"service", c.service,
		"component", "cloudwego",
	)
}

func (c *logCore) log(ctx context.Context, level slog.Level, message string) {
	c.mu.RLock()
	logger := c.logger
	c.mu.RUnlock()
	logger.Log(ctx, level, message)
}

func (c *logCore) setOutput(output io.Writer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.output = output
	c.rebuild()
}

func (c *logCore) setLevel(level int) {
	switch {
	case level <= 1:
		c.level.Set(slog.LevelDebug)
	case level <= 3:
		c.level.Set(slog.LevelInfo)
	case level == 4:
		c.level.Set(slog.LevelWarn)
	default:
		c.level.Set(slog.LevelError)
	}
}

type HertzLogger struct{ core *logCore }

func NewHertzLogger(output io.Writer, level, service string) *HertzLogger {
	return &HertzLogger{core: newLogCore(output, level, service)}
}

func (l *HertzLogger) Trace(v ...interface{}) {
	l.core.log(context.Background(), slog.LevelDebug, fmt.Sprint(v...))
}
func (l *HertzLogger) Debug(v ...interface{}) {
	l.core.log(context.Background(), slog.LevelDebug, fmt.Sprint(v...))
}
func (l *HertzLogger) Info(v ...interface{}) {
	l.core.log(context.Background(), slog.LevelInfo, fmt.Sprint(v...))
}
func (l *HertzLogger) Notice(v ...interface{}) {
	l.core.log(context.Background(), slog.LevelInfo, fmt.Sprint(v...))
}
func (l *HertzLogger) Warn(v ...interface{}) {
	l.core.log(context.Background(), slog.LevelWarn, fmt.Sprint(v...))
}
func (l *HertzLogger) Error(v ...interface{}) {
	l.core.log(context.Background(), slog.LevelError, fmt.Sprint(v...))
}
func (l *HertzLogger) Fatal(v ...interface{}) {
	l.core.log(context.Background(), slog.LevelError, fmt.Sprint(v...))
}

func (l *HertzLogger) Tracef(format string, v ...interface{})  { l.Trace(fmt.Sprintf(format, v...)) }
func (l *HertzLogger) Debugf(format string, v ...interface{})  { l.Debug(fmt.Sprintf(format, v...)) }
func (l *HertzLogger) Infof(format string, v ...interface{})   { l.Info(fmt.Sprintf(format, v...)) }
func (l *HertzLogger) Noticef(format string, v ...interface{}) { l.Notice(fmt.Sprintf(format, v...)) }
func (l *HertzLogger) Warnf(format string, v ...interface{})   { l.Warn(fmt.Sprintf(format, v...)) }
func (l *HertzLogger) Errorf(format string, v ...interface{})  { l.Error(fmt.Sprintf(format, v...)) }
func (l *HertzLogger) Fatalf(format string, v ...interface{})  { l.Fatal(fmt.Sprintf(format, v...)) }

func (l *HertzLogger) CtxTracef(ctx context.Context, format string, v ...interface{}) {
	l.core.log(ctx, slog.LevelDebug, fmt.Sprintf(format, v...))
}
func (l *HertzLogger) CtxDebugf(ctx context.Context, format string, v ...interface{}) {
	l.core.log(ctx, slog.LevelDebug, fmt.Sprintf(format, v...))
}
func (l *HertzLogger) CtxInfof(ctx context.Context, format string, v ...interface{}) {
	l.core.log(ctx, slog.LevelInfo, fmt.Sprintf(format, v...))
}
func (l *HertzLogger) CtxNoticef(ctx context.Context, format string, v ...interface{}) {
	l.core.log(ctx, slog.LevelInfo, fmt.Sprintf(format, v...))
}
func (l *HertzLogger) CtxWarnf(ctx context.Context, format string, v ...interface{}) {
	l.core.log(ctx, slog.LevelWarn, fmt.Sprintf(format, v...))
}
func (l *HertzLogger) CtxErrorf(ctx context.Context, format string, v ...interface{}) {
	l.core.log(ctx, slog.LevelError, fmt.Sprintf(format, v...))
}
func (l *HertzLogger) CtxFatalf(ctx context.Context, format string, v ...interface{}) {
	l.core.log(ctx, slog.LevelError, fmt.Sprintf(format, v...))
}

func (l *HertzLogger) SetLevel(level hlog.Level)  { l.core.setLevel(int(level)) }
func (l *HertzLogger) SetOutput(output io.Writer) { l.core.setOutput(output) }

type KitexLogger struct{ core *logCore }

func NewKitexLogger(output io.Writer, level, service string) *KitexLogger {
	return &KitexLogger{core: newLogCore(output, level, service)}
}

func (l *KitexLogger) Trace(v ...interface{}) {
	l.core.log(context.Background(), slog.LevelDebug, fmt.Sprint(v...))
}
func (l *KitexLogger) Debug(v ...interface{}) {
	l.core.log(context.Background(), slog.LevelDebug, fmt.Sprint(v...))
}
func (l *KitexLogger) Info(v ...interface{}) {
	l.core.log(context.Background(), slog.LevelInfo, fmt.Sprint(v...))
}
func (l *KitexLogger) Notice(v ...interface{}) {
	l.core.log(context.Background(), slog.LevelInfo, fmt.Sprint(v...))
}
func (l *KitexLogger) Warn(v ...interface{}) {
	l.core.log(context.Background(), slog.LevelWarn, fmt.Sprint(v...))
}
func (l *KitexLogger) Error(v ...interface{}) {
	l.core.log(context.Background(), slog.LevelError, fmt.Sprint(v...))
}
func (l *KitexLogger) Fatal(v ...interface{}) {
	l.core.log(context.Background(), slog.LevelError, fmt.Sprint(v...))
}

func (l *KitexLogger) Tracef(format string, v ...interface{})  { l.Trace(fmt.Sprintf(format, v...)) }
func (l *KitexLogger) Debugf(format string, v ...interface{})  { l.Debug(fmt.Sprintf(format, v...)) }
func (l *KitexLogger) Infof(format string, v ...interface{})   { l.Info(fmt.Sprintf(format, v...)) }
func (l *KitexLogger) Noticef(format string, v ...interface{}) { l.Notice(fmt.Sprintf(format, v...)) }
func (l *KitexLogger) Warnf(format string, v ...interface{})   { l.Warn(fmt.Sprintf(format, v...)) }
func (l *KitexLogger) Errorf(format string, v ...interface{})  { l.Error(fmt.Sprintf(format, v...)) }
func (l *KitexLogger) Fatalf(format string, v ...interface{})  { l.Fatal(fmt.Sprintf(format, v...)) }

func (l *KitexLogger) CtxTracef(ctx context.Context, format string, v ...interface{}) {
	l.core.log(ctx, slog.LevelDebug, fmt.Sprintf(format, v...))
}
func (l *KitexLogger) CtxDebugf(ctx context.Context, format string, v ...interface{}) {
	l.core.log(ctx, slog.LevelDebug, fmt.Sprintf(format, v...))
}
func (l *KitexLogger) CtxInfof(ctx context.Context, format string, v ...interface{}) {
	l.core.log(ctx, slog.LevelInfo, fmt.Sprintf(format, v...))
}
func (l *KitexLogger) CtxNoticef(ctx context.Context, format string, v ...interface{}) {
	l.core.log(ctx, slog.LevelInfo, fmt.Sprintf(format, v...))
}
func (l *KitexLogger) CtxWarnf(ctx context.Context, format string, v ...interface{}) {
	l.core.log(ctx, slog.LevelWarn, fmt.Sprintf(format, v...))
}
func (l *KitexLogger) CtxErrorf(ctx context.Context, format string, v ...interface{}) {
	l.core.log(ctx, slog.LevelError, fmt.Sprintf(format, v...))
}
func (l *KitexLogger) CtxFatalf(ctx context.Context, format string, v ...interface{}) {
	l.core.log(ctx, slog.LevelError, fmt.Sprintf(format, v...))
}

func (l *KitexLogger) SetLevel(level klog.Level)  { l.core.setLevel(int(level)) }
func (l *KitexLogger) SetOutput(output io.Writer) { l.core.setOutput(output) }

func InstallCloudWeGoLoggers(output io.Writer, level, service string) {
	hlog.SetLogger(NewHertzLogger(output, level, service))
	klog.SetLogger(NewKitexLogger(output, level, service))
}

var _ hlog.FullLogger = (*HertzLogger)(nil)
var _ klog.FullLogger = (*KitexLogger)(nil)
