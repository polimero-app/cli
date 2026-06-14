package printer

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/config"
	"github.com/polimero-app/cli/internal/keychain"
	"github.com/polimero-app/cli/internal/output"
	"github.com/polimero-app/cli/internal/tty"
	"github.com/spf13/cobra"
)

// RemoveDeps holds injectable dependencies for the printer remove command.
type RemoveDeps struct {
	KC       keychain.Keychain
	Prompter tty.Prompter
}

func removeCommand() *cobra.Command {
	return RemoveCommandWithDeps(RemoveDeps{
		KC:       keychain.NewReal(),
		Prompter: tty.NewReal(),
	})
}

// RemoveCommandWithDeps constructs the "remove" cobra command with injected dependencies.
func RemoveCommandWithDeps(deps RemoveDeps) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a printer profile",
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			return runRemove(cmd, args[0], yes, deps)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip interactive confirmation")
	return cmd
}

type removeWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func runRemove(cmd *cobra.Command, nameArg string, yes bool, deps RemoveDeps) error {
	formatStr, _ := cmd.Root().PersistentFlags().GetString("output")
	format, fmtErr := output.ParseFormat(formatStr)
	if fmtErr != nil {
		return apperr.New(2, fmtErr.Error())
	}

	err := doRemove(cmd, nameArg, yes, format, deps)
	if err == nil {
		return nil
	}
	return writeRemoveError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, err)
}

func doRemove(cmd *cobra.Command, nameArg string, yes bool, format output.Format, deps RemoveDeps) error {
	name := strings.ToLower(nameArg)
	if err := validateProfileName(name); err != nil {
		return err
	}

	dir, err := config.ConfigDir()
	if err != nil {
		return apperr.Newf(1, "cannot resolve config directory: %s", err)
	}
	cfg, err := config.Open(dir)
	if err != nil {
		return apperr.Newf(2, "cannot load config: %s", err)
	}
	p, ok := cfg.GetProfile(name)
	if !ok {
		return apperr.Newf(2, "printer profile %q not found", name)
	}

	// Confirmation
	if !yes {
		if !deps.Prompter.IsTerminal() {
			return apperr.New(2, "non-interactive mode requires --yes")
		}
		answer, err := deps.Prompter.ReadLine(
			fmt.Sprintf("Remove printer profile %s and its stored secrets? Type 'yes' to continue: ", name),
		)
		if err != nil {
			return apperr.Newf(1, "cannot read confirmation: %s", err)
		}
		if answer != "yes" {
			return apperr.New(2, "confirmation declined; profile not removed")
		}
	}

	var warnings []removeWarning
	accessCodeRemoved := false
	tlsFingerprintRemoved := false

	// Delete access code (missing = warning, not fatal)
	kcAcct := fmt.Sprintf("%s:%s:access-code", p.Driver, name)
	if err := deps.KC.Delete("polimero", kcAcct); err != nil {
		if errors.Is(err, keychain.ErrNotFound) {
			warnings = append(warnings, removeWarning{
				Code:    "access_code_not_found",
				Message: "profile was removed, but no stored access code was found",
			})
		} else {
			return apperr.Newf(1, "cannot delete access code from keychain: %s", err)
		}
	} else {
		accessCodeRemoved = true
	}

	// Delete TLS fingerprint (missing on insecure profile = expected, no warning)
	kcFpAcct := fmt.Sprintf("%s:%s:tls-fingerprint", p.Driver, name)
	if err := deps.KC.Delete("polimero", kcFpAcct); err != nil {
		if errors.Is(err, keychain.ErrNotFound) {
			if !p.Insecure {
				warnings = append(warnings, removeWarning{
					Code:    "tls_fingerprint_not_found",
					Message: "profile was removed, but no stored TLS fingerprint was found",
				})
			}
		} else {
			return apperr.Newf(1, "cannot delete TLS fingerprint from keychain: %s", err)
		}
	} else {
		tlsFingerprintRemoved = true
	}

	if _, err := cfg.RemoveProfile(name); err != nil {
		return apperr.Newf(1, "cannot remove profile: %s", err)
	}
	if err := config.Save(dir, cfg); err != nil {
		return apperr.Newf(1, "cannot save config; keychain entries may have already been deleted: %s", err)
	}

	return writeRemoveSuccess(cmd.OutOrStdout(), format, name, accessCodeRemoved, tlsFingerprintRemoved, warnings)
}

func writeRemoveSuccess(w io.Writer, format output.Format, name string, accessCodeRemoved, tlsFingerprintRemoved bool, warnings []removeWarning) error {
	if format == output.FormatJSON {
		warningsOut := make([]any, len(warnings))
		for i, ww := range warnings {
			warningsOut[i] = map[string]any{"code": ww.Code, "message": ww.Message}
		}
		return output.WriteEnvelope(w, output.Envelope{
			OK: true,
			Data: map[string]any{
				"removed": map[string]any{
					"name":                  name,
					"accessCodeRemoved":     accessCodeRemoved,
					"tlsFingerprintRemoved": tlsFingerprintRemoved,
				},
				"warnings": warningsOut,
			},
			Error: nil,
			Meta:  output.Meta{Command: "printer remove"},
		})
	}
	_, err := fmt.Fprintf(w, "Printer profile removed: %s\n", name)
	return err
}

func writeRemoveError(out, errOut io.Writer, format output.Format, err error) error {
	var exitErr *apperr.ExitError
	code := 1
	if errors.As(err, &exitErr) {
		code = exitErr.Code
	}
	if format == output.FormatJSON {
		_ = output.WriteEnvelope(out, output.Envelope{
			OK:    false,
			Data:  nil,
			Error: &output.ErrDetail{Code: "error", Message: err.Error()},
			Meta:  output.Meta{Command: "printer remove"},
		})
	} else {
		_, _ = fmt.Fprintf(errOut, "Error: %s\n", err)
	}
	return apperr.New(code, "")
}
