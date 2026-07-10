package jobs

import (
	"context"
	"fmt"
	"time"

	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/cmderr"
	"github.com/polimero-app/cli/internal/devicepath"
	"github.com/polimero-app/cli/internal/driver"
	"github.com/polimero-app/cli/internal/output"
	"github.com/polimero-app/cli/internal/protocoltrace"
	"github.com/spf13/cobra"
)

const commandStart = "jobs start"

func startCommandWithDeps(deps Deps) *cobra.Command {
	var flags struct {
		plate         int
		skipLeveling  bool
		yes           bool
		timeout       string
		insecure      bool
		protocolTrace string
	}

	cmd := &cobra.Command{
		Use:   "start <printer> <device-path>",
		Short: "Start a print job from a file on printer storage",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return writeUsageError(cmd, commandStart, "profile name is required")
			}
			if len(args) < 2 {
				return writeUsageError(cmd, commandStart, "device-path is required")
			}
			if len(args) > 2 {
				return writeUsageError(cmd, commandStart, fmt.Sprintf("expected printer and device-path, got %d arguments", len(args)))
			}
			hasPlate := cmd.Flags().Changed("plate")
			var plate *int
			if hasPlate {
				v := flags.plate
				plate = &v
			}
			return runStart(cmd, args[0], args[1], plate, flags.skipLeveling,
				flags.yes, flags.timeout, flags.insecure, flags.protocolTrace, deps)
		},
	}
	cmd.Flags().IntVar(&flags.plate, "plate", 0, "plate/sub-file index within a multi-plate file")
	cmd.Flags().BoolVar(&flags.skipLeveling, "skip-leveling", false, "skip automatic bed leveling")
	cmd.Flags().BoolVar(&flags.yes, "yes", false, "skip interactive confirmation")
	cmd.Flags().StringVar(&flags.timeout, "timeout", "", "override the profile connection timeout (e.g. 10s)")
	cmd.Flags().BoolVar(&flags.insecure, "insecure", false, "skip TLS fingerprint verification for this invocation")
	cmd.Flags().StringVar(&flags.protocolTrace, "protocol-trace", "", "write protocol diagnostics to this file (JSON Lines)")
	return cmd
}

func writeUsageError(cmd *cobra.Command, cmdName, message string) error {
	return cmderr.WriteUsage(cmd, cmdName, message)
}

func runStart(cmd *cobra.Command, nameArg, devicePath string, plate *int, skipLeveling bool,
	yes bool, timeoutFlag string, insecureFlag bool, protocolTrace string, deps Deps,
) (retErr error) {
	formatStr, _ := cmd.Root().PersistentFlags().GetString("output")
	format, fmtErr := output.ParseFormat(formatStr)
	if fmtErr != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Error: %s\n", fmtErr)
		return apperr.New(2, "")
	}

	dp, err := devicepath.Parse(devicePath)
	if err != nil {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandStart, err)
	}
	if dp.BaseName() == "" {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandStart,
			apperr.New(2, "device path must name a file"))
	}
	devicePath = dp.String()

	traceCtx, traceCleanup, traceErr := protocoltrace.Setup(cmd.Context(), protocolTrace)
	if traceErr != nil {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandStart, traceErr)
	}
	defer protocoltrace.Finish(traceCleanup, cmd.ErrOrStderr(), &retErr)

	rp, err := resolveProfile(traceCtx, nameArg, timeoutFlag, insecureFlag, deps)
	if err != nil {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandStart, err)
	}

	if !rp.driver.Capabilities().JobStart {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandStart,
			apperr.Newf(5, "driver %q does not support job start", rp.pi.Driver))
	}

	ctx, cancel := context.WithTimeout(traceCtx, rp.timeout)
	defer cancel()

	if _, err := checkStatePrecondition(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandStart,
		rp.name, []string{"idle"}, rp, deps, ctx); err != nil {
		return err
	}

	prompt := fmt.Sprintf("Start %s on %s? Type 'yes' to continue: ", devicePath, rp.name)
	if err := checkConfirmation(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandStart, yes, prompt, deps); err != nil {
		return err
	}

	opts := driver.JobStartOptions{
		Plate:        plate,
		SkipLeveling: skipLeveling,
	}

	start := time.Now()
	result, err := rp.jobDrv.JobStart(ctx, rp.pi, rp.secrets, deps.Log, devicePath, opts)
	if err != nil {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandStart, err)
	}
	durationMs := time.Since(start).Milliseconds()

	if err := checkExpectedState(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandStart,
		rp.name, "start", "printing", result); err != nil {
		return err
	}

	var tracePath *string
	if protocolTrace != "" {
		tracePath = &protocolTrace
	}
	return writeActionSuccess(cmd.OutOrStdout(), format, commandStart, rp.name, rp.pi.Driver,
		"start", devicePath, plate, result, durationMs, tracePath)
}
