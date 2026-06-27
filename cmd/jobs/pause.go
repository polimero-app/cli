package jobs

import (
	"context"
	"fmt"
	"time"

	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/output"
	"github.com/spf13/cobra"
)

const commandPause = "jobs pause"

func pauseCommandWithDeps(deps Deps) *cobra.Command {
	var flags struct {
		yes      bool
		timeout  string
		insecure bool
	}

	cmd := &cobra.Command{
		Use:   "pause <printer>",
		Short: "Pause the active print job",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			if len(args) > 1 {
				return writeUsageError(cmd, commandPause, fmt.Sprintf("expected exactly one profile name, got %d", len(args)))
			}
			return runPause(cmd, args[0], flags.yes, flags.timeout, flags.insecure, deps)
		},
	}
	cmd.Flags().BoolVar(&flags.yes, "yes", false, "skip interactive confirmation")
	cmd.Flags().StringVar(&flags.timeout, "timeout", "", "override the profile connection timeout (e.g. 10s)")
	cmd.Flags().BoolVar(&flags.insecure, "insecure", false, "skip TLS fingerprint verification for this invocation")
	return cmd
}

func runPause(cmd *cobra.Command, nameArg string, yes bool, timeoutFlag string, insecureFlag bool, deps Deps) error {
	formatStr, _ := cmd.Root().PersistentFlags().GetString("output")
	format, fmtErr := output.ParseFormat(formatStr)
	if fmtErr != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Error: %s\n", fmtErr)
		return apperr.New(2, "")
	}

	rp, err := resolveProfile(cmd.Context(), nameArg, timeoutFlag, insecureFlag, deps)
	if err != nil {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandPause, err)
	}

	if !rp.driver.Capabilities().JobPause {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandPause,
			apperr.Newf(5, "driver %q does not support job pause", rp.pi.Driver))
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), rp.timeout)
	defer cancel()

	if _, err := checkStatePrecondition(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandPause,
		rp.name, []string{"printing"}, rp, deps, ctx); err != nil {
		return err
	}

	prompt := fmt.Sprintf("Pause the active print on %s? Type 'yes' to continue: ", rp.name)
	if err := checkConfirmation(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandPause, yes, prompt, deps); err != nil {
		return err
	}

	start := time.Now()
	result, err := rp.jobDrv.JobPause(ctx, rp.pi, rp.secrets, deps.Log)
	if err != nil {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandPause, err)
	}
	durationMs := time.Since(start).Milliseconds()

	if err := checkExpectedState(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandPause,
		rp.name, "pause", "paused", result); err != nil {
		return err
	}

	return writeActionSuccess(cmd.OutOrStdout(), format, commandPause, rp.name, rp.pi.Driver,
		"pause", "", nil, result, durationMs)
}
