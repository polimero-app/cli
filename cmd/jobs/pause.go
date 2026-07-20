package jobs

import (
	"context"
	"fmt"
	"time"

	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/cmdrun"
	"github.com/polimero-app/cli/internal/output"
	"github.com/polimero-app/cli/internal/protocoltrace"
	"github.com/spf13/cobra"
)

const commandPause = "jobs pause"

func pauseCommandWithDeps(deps Deps) *cobra.Command {
	var flags struct {
		yes           bool
		timeout       string
		insecure      bool
		protocolTrace string
	}

	cmd := &cobra.Command{
		Use:   "pause <printer>",
		Short: "Pause the active print job",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return writeUsageError(cmd, commandPause, "profile name is required")
			}
			if len(args) > 1 {
				return writeUsageError(cmd, commandPause, fmt.Sprintf("expected exactly one profile name, got %d", len(args)))
			}
			return runPause(cmd, args[0], flags.yes, flags.timeout, flags.insecure, flags.protocolTrace, deps)
		},
	}
	cmd.Flags().BoolVar(&flags.yes, "yes", false, "skip interactive confirmation")
	cmd.Flags().StringVar(&flags.timeout, "timeout", "", "override the profile connection timeout (e.g. 10s)")
	cmd.Flags().BoolVar(&flags.insecure, "insecure", false, "skip TLS fingerprint verification for this invocation")
	cmd.Flags().StringVar(&flags.protocolTrace, "protocol-trace", "", "write protocol diagnostics to this file (JSON Lines)")
	return cmd
}

func runPause(cmd *cobra.Command, nameArg string, yes bool, timeoutFlag string, insecureFlag bool, protocolTrace string, deps Deps) (retErr error) {
	formatStr, _ := cmd.Root().PersistentFlags().GetString("output")
	format, fmtErr := output.ParseFormat(formatStr)
	if fmtErr != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Error: %s\n", fmtErr)
		return apperr.New(2, "")
	}

	traceCtx, traceCleanup, traceErr := protocoltrace.Setup(cmd.Context(), protocolTrace)
	if traceErr != nil {
		return cmdrun.WriteError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandPause, traceErr)
	}
	defer protocoltrace.Finish(traceCleanup, cmd.ErrOrStderr(), &retErr)

	rp, jobDrv, err := resolveProfile(traceCtx, nameArg, timeoutFlag, insecureFlag, deps)
	if err != nil {
		return cmdrun.WriteError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandPause, err)
	}

	if !rp.Driver.Capabilities().JobPause {
		return cmdrun.WriteError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandPause,
			apperr.Newf(5, "driver %q does not support job pause", rp.Input.Driver))
	}

	ctx, cancel := context.WithTimeout(traceCtx, rp.Timeout)
	defer cancel()

	if _, err := cmdrun.CheckStatePrecondition(ctx, cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandPause,
		[]string{"printing"}, rp, deps.Log); err != nil {
		return err
	}

	prompt := fmt.Sprintf("Pause the active print on %s? Type 'yes' to continue: ", rp.Name)
	if err := cmdrun.CheckConfirmation(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandPause, yes, prompt, deps.Prompter); err != nil {
		return err
	}

	start := time.Now()
	result, err := jobDrv.JobPause(ctx, rp.Input, rp.Secrets, deps.Log)
	if err != nil {
		return cmdrun.WriteError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandPause, err)
	}
	durationMs := time.Since(start).Milliseconds()

	if err := checkExpectedState(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandPause,
		rp.Name, "pause", "paused", result); err != nil {
		return err
	}

	var tracePath *string
	if protocolTrace != "" {
		tracePath = &protocolTrace
	}
	return writeActionSuccess(cmd.OutOrStdout(), format, commandPause, rp.Name, rp.Input.Driver,
		"pause", "", nil, result, durationMs, tracePath)
}
