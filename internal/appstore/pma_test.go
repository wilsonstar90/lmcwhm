package appstore

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The sign-on script must parse; with a PHP binary at hand it is linted.
func TestPMASignonScript(t *testing.T) {
	for _, want := range []string{"lmcwhm_user", "PMA_single_signon_user", "logout"} {
		if !strings.Contains(pmaSignonPHP, want) {
			t.Fatalf("sign-on script lacks %q", want)
		}
	}
	if !strings.Contains(pmaPHPIni, "session.gc_maxlifetime = 28800") {
		t.Fatalf("ini: %s", pmaPHPIni)
	}
	php, err := exec.LookPath("php")
	if err != nil {
		t.Skip("no php")
	}
	p := filepath.Join(t.TempDir(), "signon.php")
	os.WriteFile(p, []byte(pmaSignonPHP), 0o644)
	if out, err := exec.Command(php, "-l", p).CombinedOutput(); err != nil {
		t.Fatalf("php -l: %v\n%s", err, out)
	}
}
