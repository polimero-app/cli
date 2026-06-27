package jobs

import (
	"context"
	"fmt"
	"time"

	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/output"
	"github.com/spf13/cobra"
)

const commandResume = "jobs resume"

func resumeCommandWithDeps(deps Deps) *cobra.Command {
	var flags struct {
		yes      bool
		timeout  string
		insecure bool
	}

	cmd := &cobra.Command{
		Use:   "resume <printer>",
		Short: "Resume a paused print job",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			if len(args) > 1 {
				return writeUsageError(cmd, commandResume, fmt.Sprintf("expected exactly one profile name, got %d", len(args)))
			}
			return runResume(cmd, args[0], flags.yes, flags.timeout, flags.insecure, deps)
		},
	}
	cmd.Flags().BoolVar(&flags.yes, "yes", false, "skip interactive confirmation")
	cmd.Flags().StringVar(&flags.timeout, "timeout", "", "override the profile connection timeout (e.g. 10s)")
	cmd.Flags().BoolVar(&flags.insecure, "insecure", false, "skip TLS fingerprint verification for this invocation")
	return cmd
}

func runResume(cmd *cobra.Command, nameArg string, yes bool, timeoutFlag string, insecureFlag bool, deps Deps) error {
	formatStr, _ := cmd.Root().PersistentFlags().GetString("output")
	format, fmtErr := output.ParseFormat(formatStr)
	if fmtErr != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Error: %s\n", fmtErr)
		return apperr.New(2, "")
	}

	rp, err := resolveProfile(cmd.Context(), nameArg, timeoutFlag, insecureFlag, deps)
	if err != nil {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandResume, err)
	}

	if !rp.driver.Capabilities().JobResume {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandResume,
			apperr.Newf(5, "driver %q does not support job resume", rp.pi.Driver))
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), rp.timeout)
	defer cancel()

	if _, err := checkStatePrecondition(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandResume,
		rp.name, []string{"paused"}, rp, deps, ctx); err != nil {
		return err
	}

	prompt := fmt.Sprintf("Resume the paused print on %s? Type 'yes' to continue: ", rp.name)
	if err := checkConfirmation(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandResume, yes, prompt, deps); err != nil {
		return err
	}

	start := time.Now()
	result, err := rp.jobDrv.JobResume(ctx, rp.pi, rp.secrets, deps.Log)
	if err != nil {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandResume, err)
	}
	durationMs := time.Since(start).Milliseconds()

	if err := checkExpectedState(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandResume,
		rp.name, "resume", "printing", result); err != nil {
		return err
	}

	return writeActionSuccess(cmd.OutOrStdout(), format, commandResume, rp.name, rp.pi.Driver,
		"resume", "", nil, result, durationMs)
}
