package profile

import (
	"regexp"

	"github.com/polimero-app/cli/internal/apperr"
)

var nameRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// ValidateName checks that a profile name conforms to naming rules.
func ValidateName(name string) error {
	if name == "" {
		return apperr.New(2, "profile name is required")
	}
	if len(name) > 64 {
		return apperr.Newf(2, "profile name too long (max 64 chars): %q", name)
	}
	if !nameRE.MatchString(name) {
		return apperr.Newf(2, "invalid profile name %q: use only ASCII letters, digits, '.', '_', '-', starting with a letter or digit", name)
	}
	return nil
}
