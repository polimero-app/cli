package printer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"regexp"
	"strings"
	"time"

	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/config"
	"github.com/polimero-app/cli/internal/driver"
	"github.com/polimero-app/cli/internal/drivers"
	"github.com/polimero-app/cli/internal/keychain"
	"github.com/polimero-app/cli/internal/output"
	"github.com/polimero-app/cli/internal/tty"
	"github.com/spf13/cobra"
)

var profileNameRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)
var dnsLabelRE = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)

// AddDeps holds injectable dependencies for the printer add command.
// Tests supply mocks; the real command wires real implementations.
type AddDeps struct {
	KC        keychain.Keychain
	Prompter  tty.Prompter
	GetDriver func(name string) (driver.Driver, bool)
}

func addCommand() *cobra.Command {
	return AddCommandWithDeps(AddDeps{
		KC:        keychain.NewReal(),
		Prompter:  tty.NewReal(),
		GetDriver: drivers.Get,
	})
}

// AddCommandWithDeps constructs the "add" cobra command with injected dependencies.
func AddCommandWithDeps(deps AddDeps) *cobra.Command {
	var flags struct {
		driverName     string
		host           string
		serial         string
		timeout        string
		insecure       bool
		accessCodeFile string
	}

	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Add a printer profile",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			if len(args) > 1 {
				return writeAddUsageError(cmd, fmt.Sprintf("expected exactly one profile name, got %d", len(args)))
			}
			return runAdd(cmd, args[0], flags.driverName, flags.host, flags.serial,
				flags.timeout, flags.insecure, flags.accessCodeFile, deps)
		},
	}

	cmd.Flags().StringVar(&flags.driverName, "driver", "", "driver name (e.g. bambu-lan)")
	cmd.Flags().StringVar(&flags.host, "host", "", "printer IP or hostname")
	cmd.Flags().StringVar(&flags.serial, "serial", "", "printer serial number (required for bambu-lan)")
	cmd.Flags().StringVar(&flags.timeout, "timeout", "10s", "connection timeout")
	cmd.Flags().BoolVar(&flags.insecure, "insecure", false, "skip TLS verification and MQTT auth check")
	cmd.Flags().StringVar(&flags.accessCodeFile, "access-code-file", "", "file containing the access code")

	return cmd
}

func writeAddUsageError(cmd *cobra.Command, message string) error {
	formatStr, _ := cmd.Root().PersistentFlags().GetString("output")
	format, fmtErr := output.ParseFormat(formatStr)
	if fmtErr != nil {
		return apperr.New(2, fmtErr.Error())
	}
	return writeAddError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, apperr.New(2, message))
}

func runAdd(cmd *cobra.Command, nameArg, driverName, host, serial, timeoutStr string, insecure bool, accessCodeFile string, deps AddDeps) error {
	formatStr, _ := cmd.Root().PersistentFlags().GetString("output")
	format, fmtErr := output.ParseFormat(formatStr)
	if fmtErr != nil {
		return apperr.New(2, fmtErr.Error())
	}

	err := doAdd(cmd, nameArg, driverName, host, serial, timeoutStr, insecure, accessCodeFile, format, deps)
	if err == nil {
		return nil
	}
	return writeAddError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, err)
}

func doAdd(cmd *cobra.Command, nameArg, driverName, host, serial, timeoutStr string, insecure bool, accessCodeFile string, format output.Format, deps AddDeps) error {
	name := strings.ToLower(nameArg)

	// 1. Validate
	if driverName == "" {
		return apperr.New(2, "--driver is required")
	}
	if host == "" {
		return apperr.New(2, "--host is required")
	}
	if err := validateHost(host); err != nil {
		return err
	}
	if err := validateProfileName(name); err != nil {
		return err
	}
	drv, ok := deps.GetDriver(driverName)
	if !ok {
		return apperr.Newf(2, "unknown driver %q; valid drivers: %s", driverName, strings.Join(drivers.Names(), ", "))
	}
	if driverName == "bambu-lan" {
		if err := validateSerial(serial); err != nil {
			return err
		}
	}
	timeout, err := time.ParseDuration(timeoutStr)
	if err != nil {
		return apperr.Newf(2, "invalid --timeout %q: %s", timeoutStr, err)
	}
	if timeout <= 0 {
		return apperr.Newf(2, "--timeout must be greater than zero")
	}

	dir, err := config.ConfigDir()
	if err != nil {
		return apperr.Newf(1, "cannot resolve config directory: %s", err)
	}
	cfg, err := config.Open(dir)
	if err != nil {
		return apperr.Newf(2, "cannot load config: %s", err)
	}
	if _, exists := cfg.GetProfile(name); exists {
		return apperr.Newf(2, "printer profile %q already exists", name)
	}

	// 2. Collect access code
	var accessCode string
	if accessCodeFile != "" {
		accessCode, err = readAccessCodeFile(accessCodeFile)
		if err != nil {
			return err
		}
	} else if deps.Prompter.IsTerminal() {
		accessCode, err = deps.Prompter.ReadHidden(fmt.Sprintf("Enter Bambu LAN access code for %s: ", name))
		if err != nil {
			return apperr.Newf(1, "cannot read access code: %s", err)
		}
	} else {
		return apperr.New(2, "non-interactive mode requires --access-code-file")
	}
	if accessCode == "" {
		return apperr.New(2, "access code must not be empty")
	}

	kcAcct := fmt.Sprintf("%s:%s:access-code", driverName, name)
	kcFpAcct := fmt.Sprintf("%s:%s:tls-fingerprint", driverName, name)

	verboseFlag, _ := cmd.Root().PersistentFlags().GetBool("verbose")
	verbose := verboseFlag && format == output.FormatHuman
	w := cmd.OutOrStdout()

	var fingerprint string
	opCtx, opCancel := context.WithTimeout(cmd.Context(), timeout)
	defer opCancel()
	if !insecure {
		// 3. Connectivity check (TLS + MQTT CONNECT + CONNACK)
		output.Verbose(w, verbose, fmt.Sprintf("Connecting to %s:8883...", host))
		fingerprint, err = drv.ConnectCheck(opCtx, host, serial, accessCode, false, timeout)
		if err != nil {
			return err // already an *apperr.ExitError with code 3 or 4
		}
		if !driver.ValidTLSFingerprint(fingerprint) {
			return apperr.New(4, "driver returned invalid TLS fingerprint")
		}
		output.Verbose(w, verbose, fmt.Sprintf("Connection verified. TLS fingerprint: %s", fingerprint))

		// 4. Store access code
		output.Verbose(w, verbose, "Storing credentials in keychain...")
		if err := deps.KC.Set(opCtx, "polimero", kcAcct, accessCode); err != nil {
			return apperr.Wrap(3, "cannot store access code in keychain", err)
		}

		// 5. Store TLS fingerprint; rollback access code on failure
		if err := deps.KC.Set(opCtx, "polimero", kcFpAcct, fingerprint); err != nil {
			cleanupCtx, cleanupCancel := secretStoreContext(cmd.Context())
			_ = deps.KC.Delete(cleanupCtx, "polimero", kcAcct)
			cleanupCancel()
			return apperr.Wrap(3, "cannot store TLS fingerprint in keychain", err)
		}
	} else {
		// Insecure: store access code (no connectivity check, no fingerprint)
		output.Verbose(w, verbose, "Storing access code in keychain...")
		if err := deps.KC.Set(opCtx, "polimero", kcAcct, accessCode); err != nil {
			return apperr.Wrap(3, "cannot store access code in keychain", err)
		}
	}

	output.Verbose(w, verbose, fmt.Sprintf("Saving profile %q...", name))

	// 6. Write profile; rollback keychain entries on failure
	now := time.Now().UTC()
	p := config.Profile{
		Driver:   driverName,
		Host:     host,
		Serial:   serial,
		Timeout:  timeoutStr,
		Insecure: insecure,
		Created:  now,
		Updated:  now,
	}
	if err := cfg.AddProfile(name, p); err != nil {
		cleanupCtx, cleanupCancel := secretStoreContext(cmd.Context())
		_ = deps.KC.Delete(cleanupCtx, "polimero", kcAcct)
		if !insecure {
			_ = deps.KC.Delete(cleanupCtx, "polimero", kcFpAcct)
		}
		cleanupCancel()
		return apperr.Newf(1, "cannot add profile: %s", err)
	}
	if err := config.Save(dir, cfg); err != nil {
		cleanupCtx, cleanupCancel := secretStoreContext(cmd.Context())
		_ = deps.KC.Delete(cleanupCtx, "polimero", kcAcct)
		if !insecure {
			_ = deps.KC.Delete(cleanupCtx, "polimero", kcFpAcct)
		}
		cleanupCancel()
		return apperr.Newf(1, "cannot save config: %s", err)
	}

	// 7. Output success
	return writeAddSuccess(cmd.OutOrStdout(), format, name, p, fingerprint)
}

func writeAddSuccess(w io.Writer, format output.Format, name string, p config.Profile, fingerprint string) error {
	if format == output.FormatJSON {
		var fp any
		if fingerprint != "" {
			fp = fingerprint
		}
		return output.WriteEnvelope(w, output.Envelope{
			OK: true,
			Data: map[string]any{
				"profile": map[string]any{
					"name":           name,
					"driver":         p.Driver,
					"host":           p.Host,
					"serial":         p.Serial,
					"timeout":        p.Timeout,
					"insecure":       p.Insecure,
					"tlsFingerprint": fp,
				},
			},
			Error: nil,
			Meta:  output.Meta{Command: "printer add"},
		})
	}
	lines := []string{
		fmt.Sprintf("Printer profile added: %s", name),
		fmt.Sprintf("Driver: %s", p.Driver),
		fmt.Sprintf("Host: %s", p.Host),
		fmt.Sprintf("Serial: %s", p.Serial),
	}
	if p.Insecure {
		lines = append(lines, "Warning: TLS verification is disabled for this profile.")
	} else {
		lines = append(lines, fmt.Sprintf("TLS: %s", fingerprint))
	}
	for _, l := range lines {
		if _, err := fmt.Fprintln(w, l); err != nil {
			return err
		}
	}
	return nil
}

func writeAddError(out, errOut io.Writer, format output.Format, err error) error {
	var exitErr *apperr.ExitError
	code := 1
	if errors.As(err, &exitErr) {
		code = exitErr.Code
	}
	if format == output.FormatJSON {
		_ = output.WriteEnvelope(out, output.Envelope{
			OK:    false,
			Data:  nil,
			Error: &output.ErrDetail{Code: addErrorCode(err), Message: addErrorMessage(err)},
			Meta:  output.Meta{Command: "printer add"},
		})
	} else {
		_, _ = fmt.Fprintf(errOut, "Error: %s\n", addErrorMessage(err))
	}
	return apperr.New(code, "")
}

func addErrorMessage(err error) string {
	switch addErrorCode(err) {
	case "auth_error":
		return "MQTT authentication rejected"
	case "network_error":
		if strings.Contains(err.Error(), "connection cancelled") {
			return "connection cancelled"
		}
		return "connection failed"
	case "keychain_error":
		return "keychain operation failed"
	default:
		return err.Error()
	}
}

func addErrorCode(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "already exists"):
		return "profile_exists"
	case strings.Contains(msg, "unknown driver"):
		return "unknown_driver"
	case strings.Contains(msg, "MQTT authentication"):
		return "auth_error"
	case strings.Contains(msg, "connection failed"), strings.Contains(msg, "connection cancelled"):
		return "network_error"
	case strings.Contains(msg, "keychain"):
		return "keychain_error"
	default:
		return "error"
	}
}

func validateProfileName(name string) error {
	if name == "" {
		return apperr.New(2, "profile name is required")
	}
	if len(name) > 64 {
		return apperr.Newf(2, "profile name too long (max 64 chars): %q", name)
	}
	if !profileNameRE.MatchString(name) {
		return apperr.Newf(2, "invalid profile name %q: use only ASCII letters, digits, '.', '_', '-', starting with a letter or digit", name)
	}
	return nil
}

func validateHost(host string) error {
	if strings.TrimSpace(host) != host || host == "" {
		return apperr.Newf(2, "invalid --host %q: must be an IP address or DNS hostname", host)
	}
	if strings.ContainsAny(host, " \t\r\n") {
		return apperr.Newf(2, "invalid --host %q: must not contain whitespace", host)
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return nil
	}
	host = strings.TrimSuffix(host, ".")
	if len(host) > 253 {
		return apperr.Newf(2, "invalid --host %q: hostname too long", host)
	}
	if looksLikeIPv4Literal(host) {
		return apperr.Newf(2, "invalid --host %q: must be a valid IP address or DNS hostname", host)
	}
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if !dnsLabelRE.MatchString(label) {
			return apperr.Newf(2, "invalid --host %q: must be an IP address or DNS hostname", host)
		}
	}
	return nil
}

func looksLikeIPv4Literal(host string) bool {
	labels := strings.Split(host, ".")
	if len(labels) != 4 {
		return false
	}
	for _, label := range labels {
		if label == "" {
			return false
		}
		for _, c := range label {
			if c < '0' || c > '9' {
				return false
			}
		}
	}
	return true
}

func validateSerial(serial string) error {
	if serial == "" {
		return apperr.New(2, "--serial is required for bambu-lan driver")
	}
	if len(serial) > 64 {
		return apperr.Newf(2, "--serial too long (max 64 chars)")
	}
	for _, c := range serial {
		if c < 0x21 || c > 0x7E {
			return apperr.Newf(2, "--serial contains invalid character (must be printable ASCII with no whitespace)")
		}
	}
	return nil
}

func readAccessCodeFile(path string) (string, error) {
	f, err := openAccessCodeFile(path)
	if err != nil {
		return "", apperr.Newf(2, "--access-code-file: %s", err)
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return "", apperr.Newf(2, "--access-code-file: %s", err)
	}
	if !info.Mode().IsRegular() {
		return "", apperr.Newf(2, "--access-code-file %q is not a regular file", path)
	}
	if info.Size() > 4096 {
		return "", apperr.Newf(2, "--access-code-file %q exceeds 4 KiB limit", path)
	}
	if info.Mode().Perm()&0077 != 0 {
		return "", apperr.Newf(2, "--access-code-file %q has insecure permissions: group or other access detected", path)
	}
	data, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil {
		return "", apperr.Newf(2, "--access-code-file: %s", err)
	}
	if len(data) > 4096 {
		return "", apperr.Newf(2, "--access-code-file %q exceeds 4 KiB limit", path)
	}
	return trimTrailingNewline(string(data)), nil
}

// trimTrailingNewline removes exactly one trailing \r\n or \n.
// Other leading or trailing whitespace is preserved per spec.
func trimTrailingNewline(s string) string {
	if strings.HasSuffix(s, "\r\n") {
		return s[:len(s)-2]
	}
	if strings.HasSuffix(s, "\n") {
		return s[:len(s)-1]
	}
	return s
}
