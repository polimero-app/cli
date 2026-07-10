package camera

import (
	"context"
	"io"
	"time"

	"github.com/polimero-app/cli/internal/cmderr"
	"github.com/polimero-app/cli/internal/driver"
	"github.com/polimero-app/cli/internal/output"
	"github.com/polimero-app/cli/internal/profile"
	"github.com/spf13/cobra"
)

type resolvedProfile struct {
	name    string
	driver  driver.Driver
	input   driver.ProfileInput
	secrets driver.SecretsBundle
	timeout time.Duration
}

func resolveProfile(ctx context.Context, nameArg, timeoutFlag string, insecureFlag bool, deps Deps) (*resolvedProfile, error) {
	r, err := profile.Resolve(ctx, nameArg, timeoutFlag, insecureFlag, deps.KC, deps.GetDriver)
	if err != nil {
		return nil, err
	}
	return &resolvedProfile{
		name:    r.Name,
		driver:  r.Driver,
		input:   r.Input,
		secrets: r.Secrets,
		timeout: r.Timeout,
	}, nil
}

func writeUsageError(cmd *cobra.Command, cmdName, message string) error {
	return cmderr.WriteUsage(cmd, cmdName, message)
}

func writeError(out, errOut io.Writer, format output.Format, cmdName string, err error) error {
	detail := output.ErrDetail{Code: cmderr.Code(err, false), Message: err.Error()}
	return cmderr.Write(out, errOut, format, cmdName, detail, err)
}
