package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"k8s.io/component-base/cli/flag"
	"k8s.io/component-base/logs"
	"k8s.io/klog/v2"

	"github.com/HappyLadySauce/Knowledge-Core/cmd/app/options"
	"github.com/HappyLadySauce/Knowledge-Core/cmd/app/router"
	assetsroute "github.com/HappyLadySauce/Knowledge-Core/cmd/app/routes/assets"
	authroute "github.com/HappyLadySauce/Knowledge-Core/cmd/app/routes/auth"
	documentroute "github.com/HappyLadySauce/Knowledge-Core/cmd/app/routes/document"
	taxonomyroute "github.com/HappyLadySauce/Knowledge-Core/cmd/app/routes/taxonomy"
	userroute "github.com/HappyLadySauce/Knowledge-Core/cmd/app/routes/user"
	"github.com/HappyLadySauce/Knowledge-Core/cmd/app/svc"
	"github.com/HappyLadySauce/Knowledge-Core/internal/config"
)

func NewAPICommand(ctx context.Context, basename string) *cobra.Command {
	opts := options.NewOptions(basename)
	cmd := &cobra.Command{
		Use:   basename,
		Short: basename + " is a web server",
		Long:  basename + " is a web server",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Bind command-line flags to Viper (CLI values override the config file).
			// 将命令行标志绑定到 Viper（命令行参数覆盖配置文件）。
			if err := viper.BindPFlags(cmd.Flags()); err != nil {
				return err
			}

			if err := viper.Unmarshal(opts); err != nil {
				return err
			}

			// Initialize logging after flags are parsed and configuration is loaded.
			// 在解析完标志并加载配置后初始化日志。
			logs.InitLogs()
			defer logs.FlushLogs()

			// Validate options after flags and configuration are fully populated.
			// 在标志与配置全部就绪后校验选项。
			if err := opts.Validate(); err != nil {
				return err
			}
			return run(ctx, opts)
		},
	}

	nfs := opts.AddFlags(cmd.Flags())
	flag.SetUsageAndHelpFunc(cmd, *nfs, 80)

	return cmd
}

func run(ctx context.Context, opts *options.Options) error {
	cfg := &config.Config{
		InsecureServing: opts.InsecureServing,
		Database:        opts.Database,
		Redis:           opts.Redis,
		JWT:             opts.JWT,
		WebSocket:       opts.WebSocket,
		Uploads:         opts.Uploads,
	}
	config.Init(cfg)

	if err := router.ConfigureTrustedProxies(opts.InsecureServing.TrustedProxies); err != nil {
		return fmt.Errorf("configure trusted proxies: %w", err)
	}

	sc, err := svc.NewServiceContext(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := sc.Close(); closeErr != nil {
			klog.ErrorS(closeErr, "failed to close service context")
		}
	}()

	// Bootstrap the initial admin user when none exists.
	// 引导创建初始 admin 用户（若不存在）。
	if err := sc.Auth.EnsureAdmin(ctx); err != nil {
		return err
	}

	// Initialize HTTP route handlers after the service context is ready.
	// 在服务上下文就绪后初始化 HTTP 路由处理器。
	if err := routesInit(ctx, sc); err != nil {
		return err
	}

	return serve(ctx, opts)
}

func serve(ctx context.Context, opts *options.Options) error {
	insecureAddress := fmt.Sprintf("%s:%d", opts.InsecureServing.BindAddress, opts.InsecureServing.BindPort)
	klog.V(2).InfoS("Listening and serving on", "address", insecureAddress)
	server := &http.Server{
		Addr:              insecureAddress,
		Handler:           router.Router(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown http server: %w", err)
		}
		if err := <-errCh; err != nil {
			return err
		}
		return nil
	}
}

// Initialize HTTP route handlers after the service context is ready.
// 在服务上下文就绪后初始化 HTTP 路由处理器。
func routesInit(ctx context.Context, sc *svc.ServiceContext) error {
	authroute.Init(ctx, sc)
	if err := assetsroute.Init(ctx, sc); err != nil {
		return err
	}
	userroute.Init(ctx, sc)
	taxonomyroute.Init(ctx, sc)
	if err := documentroute.Init(ctx, sc); err != nil {
		return err
	}
	return nil
}
