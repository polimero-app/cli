package files

import (
	"context"
	"io"
	"time"

	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/cmderr"
	"github.com/polimero-app/cli/internal/driver"
	"github.com/polimero-app/cli/internal/output"
	"github.com/polimero-app/cli/internal/profile"
	"github.com/spf13/cobra"
)

// resolvedProfile holds everything needed after profile/secret resolution.
type resolvedProfile struct {
	name    string
	driver  driver.FileDriver
	pi      driver.ProfileInput
	secrets driver.SecretsBundle
	timeout time.Duration
}

// resolveProfile loads and validates the named printer profile, retrieves secrets,
// and locates the file driver. Returns a resolvedProfile ready for driver calls.
func resolveProfile(ctx context.Context, nameArg, timeoutFlag string, insecureFlag bool, deps Deps) (*resolvedProfile, error) {
	r, err := profile.Resolve(ctx, nameArg, timeoutFlag, insecureFlag, deps.KC, deps.GetDriver)
	if err != nil {
		return nil, err
	}
	fileDrv, ok := r.Driver.(driver.FileDriver)
	if !ok {
		return nil, apperr.Newf(5, "driver %q does not support file operations", r.Input.Driver)
	}
	return &resolvedProfile{
		name:    r.Name,
		driver:  fileDrv,
		pi:      r.Input,
		secrets: r.Secrets,
		timeout: r.Timeout,
	}, nil
}

// writeError writes an error response in the appropriate format and returns an ExitError.
func writeError(out, errOut io.Writer, format output.Format, cmdName string, err error) error {
	detail := output.ErrDetail{Code: cmderr.Code(err, false), Message: err.Error()}
	return cmderr.Write(out, errOut, format, cmdName, detail, err)
}

func writeUsageError(cmd *cobra.Command, cmdName, message string) error {
	return cmderr.WriteUsage(cmd, cmdName, message)
}
