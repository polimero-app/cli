//go:build unix

package printer_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/keychain"
	"github.com/polimero-app/cli/internal/tty"
)

func TestAdd_AccessCodeFile_SymlinkRejected(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "code.txt")
	if err := os.WriteFile(target, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	kc := keychain.NewMock()
	p := &tty.Mock{Terminal: false}
	_, err := runAddCmd(t, dir, defaultAddDeps(kc, p),
		"myprinter", "--driver", "bambu-lan", "--host", "192.0.2.10", "--serial", "SN001",
		"--access-code-file", link, "--insecure")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Errorf("expected exit 2 for symlink access-code file, got %v", err)
	}
}
