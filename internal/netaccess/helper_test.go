package netaccess

import (
	"os"
	"os/exec"
	"testing"
)

func TestFirewallPolicyValidation(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	cmd := exec.Command(python, "helper_test.py")
	cmd.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("firewall policy tests: %v\n%s", err, out)
	}
}
