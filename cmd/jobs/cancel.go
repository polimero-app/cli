package jobs

import (
	"context"
	"fmt"
	"time"

	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/output"
	"github.com/spf13/cobra"
)

const commandCancel = "jobs cancel"

func cancelCommandWithDeps(deps Deps) *cobra.Command {
	var flags struct {
		yes      bool
		timeout  string
		insecure bool
	}

	cmd := &cobra.Command{
		Use:   "cancel <printer>",
		Short: "Cancel the active or paused print job",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			if len(args) > 1 {
				return writeUsageError(cmd, commandCancel, fmt.Sprintf("expected exactly one profile name, got %d", len(args)))
			}
			return runCancel(cmd, args[0], flags.yes, flags.timeout, flags.insecure, deps)
		},
	}
	cmd.Flags().BoolVar(&flags.yes, "yes", false, "skip interactive confirmation")
	cmd.Flags().StringVar(&flags.timeout, "timeout", "", "override the profile connection timeout (e.g. 10s)")
	cmd.Flags().BoolVar(&flags.insecure, "insecure", false, "skip TLS fingerprint verification for this invocation")
	return cmd
}

func runCancel(cmd *cobra.Command, nameArg string, yes bool, timeoutFlag string, insecureFlag bool, deps Deps) error {
	formatStr, _ := cmd.Root().PersistentFlags().GetString("output")
	format, fmtErr := output.ParseFormat(formatStr)
	if fmtErr != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Error: %s\n", fmtErr)
		return apperr.New(2, "")
	}

	rp, err := resolveProfile(cmd.Context(), nameArg, timeoutFlag, insecureFlag, deps)
	if err != nil {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandCancel, err)
	}

	if !rp.driver.Capabilities().JobCancel {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandCancel,
			apperr.Newf(5, "driver %q does not support job cancel", rp.pi.Driver))
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), rp.timeout)
	defer cancel()

	if _, err := checkStatePrecondition(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandCancel,
		rp.name, []string{"printing", "paused"}, rp, deps, ctx); err != nil {
		return err
	}

	prompt := fmt.Sprintf("Cancel the active print on %s? Type 'yes' to continue: ", rp.name)
	if err := checkConfirmation(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandCancel, yes, prompt, deps); err != nil {
		return err
	}

	start := time.Now()
	result, err := rp.jobDrv.JobCancel(ctx, rp.pi, rp.secrets, deps.Log)
	if err != nil {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandCancel, err)
	}
	durationMs := time.Since(start).Milliseconds()

	if err := checkExpectedState(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandCancel,
		rp.name, "cancel", "idle", result); err != nil {
		return err
	}

	return writeActionSuccess(cmd.OutOrStdout(), format, commandCancel, rp.name, rp.pi.Driver,
		"cancel", "", nil, result, durationMs)
}
