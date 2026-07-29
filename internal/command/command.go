package command

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
)

type Runner func(context.Context) error

func New(service string, runner Runner) *cobra.Command {
	cmd := &cobra.Command{
		Use:           service,
		Short:         "Run the " + service + " service",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if service == "" {
				return errors.New("create service command: service is required")
			}
			if runner == nil {
				return errors.New("create service command: runner is required")
			}
			return runner(cmd.Context())
		},
	}
	cmd.CompletionOptions.DisableDefaultCmd = true
	return cmd
}

func Execute(service string, runner Runner) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return New(service, runner).ExecuteContext(ctx)
}
