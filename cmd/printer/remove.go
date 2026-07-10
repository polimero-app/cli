package printer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/config"
	"github.com/polimero-app/cli/internal/drivers"
	"github.com/polimero-app/cli/internal/keychain"
	"github.com/polimero-app/cli/internal/output"
	"github.com/polimero-app/cli/internal/tty"
	"github.com/spf13/cobra"
)

// RemoveDeps holds injectable dependencies for the printer remove command.
type RemoveDeps struct {
	KC         keychain.Keychain
	Prompter   tty.Prompter
	SaveConfig func(string, *config.Config) error
}

func removeCommand() *cobra.Command {
	return RemoveCommandWithDeps(RemoveDeps{
		KC:         keychain.NewReal(),
		Prompter:   tty.NewReal(),
		SaveConfig: config.Save,
	})
}

// RemoveCommandWithDeps constructs the "remove" cobra command with injected dependencies.
func RemoveCommandWithDeps(deps RemoveDeps) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a printer profile",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return writeRemoveUsageError(cmd, "profile name is required")
			}
			if len(args) > 1 {
				return writeRemoveUsageError(cmd, fmt.Sprintf("expected exactly one profile name, got %d", len(args)))
			}
			return runRemove(cmd, args[0], yes, deps)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip interactive confirmation")
	return cmd
}

func writeRemoveUsageError(cmd *cobra.Command, message string) error {
	formatStr, _ := cmd.Root().PersistentFlags().GetString("output")
	format, fmtErr := output.ParseFormat(formatStr)
	if fmtErr != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Error: %s\n", fmtErr)
		return apperr.New(2, "")
	}
	return writeRemoveError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, apperr.New(2, message))
}

type removeWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type storedSecret struct {
	account string
	value   string
	present bool
	deleted bool
}

func runRemove(cmd *cobra.Command, nameArg string, yes bool, deps RemoveDeps) error {
	formatStr, _ := cmd.Root().PersistentFlags().GetString("output")
	format, fmtErr := output.ParseFormat(formatStr)
	if fmtErr != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Error: %s\n", fmtErr)
		return apperr.New(2, "")
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
	kcCtx, kcCancel := secretStoreContext(cmd.Context())
	defer kcCancel()

	accessCode := storedSecret{account: fmt.Sprintf("%s:%s:access-code", p.Driver, name)}
	tlsFingerprint := storedSecret{account: fmt.Sprintf("%s:%s:tls-fingerprint", p.Driver, name)}
	driverUsesTLSPinning := false
	if drv, ok := drivers.Get(p.Driver); ok {
		driverUsesTLSPinning = drv.Capabilities().TLSRefresh
	}

	if err := loadStoredSecret(kcCtx, deps.KC, &accessCode); err != nil {
		return apperr.Wrap(3, "cannot read stored access code from keychain", err)
	}
	if !accessCode.present {
		warnings = append(warnings, removeWarning{
			Code:    "access_code_not_found",
			Message: "profile was removed, but no stored access code was found",
		})
	}

	if err := loadStoredSecret(kcCtx, deps.KC, &tlsFingerprint); err != nil {
		return apperr.Wrap(3, "cannot read stored TLS fingerprint from keychain", err)
	}
	if !tlsFingerprint.present && !p.Insecure && driverUsesTLSPinning {
		warnings = append(warnings, removeWarning{
			Code:    "tls_fingerprint_not_found",
			Message: "profile was removed, but no stored TLS fingerprint was found",
		})
	}

	secrets := []*storedSecret{&accessCode, &tlsFingerprint}
	for _, secret := range secrets {
		if !secret.present {
			continue
		}
		if err := deps.KC.Delete(kcCtx, "polimero", secret.account); err != nil {
			if errors.Is(err, keychain.ErrNotFound) {
				secret.present = false
				continue
			}
			if restoreErr := restoreStoredSecrets(cmd.Context(), deps.KC, secrets); restoreErr != nil {
				return apperr.Wrap(3, "cannot delete stored secrets and could not restore prior keychain state", err)
			}
			return apperr.Wrap(3, "cannot delete stored secrets from keychain", err)
		}
		secret.deleted = true
	}

	if _, err := cfg.RemoveProfile(name); err != nil {
		if restoreErr := restoreStoredSecrets(cmd.Context(), deps.KC, secrets); restoreErr != nil {
			return apperr.Wrap(1, "cannot remove profile and could not restore stored secrets", err)
		}
		return apperr.Newf(1, "cannot remove profile: %s", err)
	}
	saveConfig := deps.SaveConfig
	if saveConfig == nil {
		saveConfig = config.Save
	}
	if err := saveConfig(dir, cfg); err != nil {
		if restoreErr := restoreStoredSecrets(cmd.Context(), deps.KC, secrets); restoreErr != nil {
			return apperr.Wrap(1, "cannot save config and could not restore stored secrets", err)
		}
		return apperr.Newf(1, "cannot save config: %s", err)
	}

	return writeRemoveSuccess(cmd.OutOrStdout(), format, name, accessCode.deleted, tlsFingerprint.deleted, warnings)
}

func loadStoredSecret(ctx context.Context, kc keychain.Keychain, secret *storedSecret) error {
	value, err := kc.Get(ctx, "polimero", secret.account)
	if errors.Is(err, keychain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	secret.value = value
	secret.present = true
	return nil
}

func restoreStoredSecrets(parent context.Context, kc keychain.Keychain, secrets []*storedSecret) error {
	ctx, cancel := secretStoreContext(context.WithoutCancel(parent))
	defer cancel()

	var firstErr error
	for _, secret := range secrets {
		if !secret.deleted {
			continue
		}
		if err := kc.Set(ctx, "polimero", secret.account, secret.value); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
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
			Error: &output.ErrDetail{Code: removeErrorCode(err), Message: removeErrorMessage(err)},
			Meta:  output.Meta{Command: "printer remove"},
		})
	} else {
		_, _ = fmt.Fprintf(errOut, "Error: %s\n", removeErrorMessage(err))
	}
	return apperr.New(code, "")
}

func removeErrorCode(err error) string {
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) {
		return "error"
	}
	switch exitErr.Code {
	case 2:
		return "config_error"
	case 3:
		return "secret_not_found"
	default:
		return "error"
	}
}

func removeErrorMessage(err error) string {
	if removeErrorCode(err) == "secret_not_found" {
		return "keychain operation failed"
	}
	return err.Error()
}
