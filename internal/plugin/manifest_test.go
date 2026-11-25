package plugin

import (
	"os"
	"strings"
	"testing"
)

func load(t *testing.T, path string) *Manifest {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	m, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestExampleManifestIsValid(t *testing.T) {
	if p := load(t, "../../docs/plugins/examples/rigctlweb.json").Validate(); len(p) > 0 {
		t.Fatalf("example must be valid: %v", p)
	}
}

func TestProblemsSayWhatToChange(t *testing.T) {
	m := load(t, "../../docs/plugins/examples/rigctlweb.json")
	m.ID = "Pat"
	m.Install.Checksums = ""
	m.Run.BasePath = "none"
	m.Run.StopWith = "SIGKILL"
	m.UI.Auth = "own"
	m.Needs.Rig = "serial"
	all := strings.Join(m.Validate(), "\n")
	for _, want := range []string{"id:", "install.checksums", "base_path", "stop_with", "ui.auth", "serial port"} {
		if !strings.Contains(all, want) {
			t.Errorf("no problem mentioning %q in:\n%s", want, all)
		}
	}
}

func TestUnknownFieldsAreRefused(t *testing.T) {
	if _, err := Parse([]byte(`{"schema":1,"idd":"x"}`)); err == nil {
		t.Fatal("a misspelled key must be an error")
	}
}

func TestSecretsNeverFixedInEnvOrDefaulted(t *testing.T) {
	m := load(t, "../../docs/plugins/examples/rigctlweb.json")
	m.Run.Env = map[string]string{"API_TOKEN": "abc"}
	m.Config = []Config{{Key: "pw", Label: "Password", Type: "text", Env: "PW", Secret: true, Default: "x"}}
	all := strings.Join(m.Validate(), "\n")
	if !strings.Contains(all, "API_TOKEN") || !strings.Contains(all, "secret cannot have a default") {
		t.Fatalf("%s", all)
	}
}

func TestPluginMustBeToldWhereToListen(t *testing.T) {
	m := load(t, "../../docs/plugins/examples/rigctlweb.json")
	m.Run.Command = []string{"{bin}", "-base-path", "{base_path}"}
	if all := strings.Join(m.Validate(), "\n"); !strings.Contains(all, "{listen}") {
		t.Fatal(all)
	}
}

func TestRigctldPlaceholderNeedsRig(t *testing.T) {
	m := load(t, "../../docs/plugins/examples/rigctlweb.json")
	m.Needs.Rig = "none"
	m.Needs.Transmits = false
	if all := strings.Join(m.Validate(), "\n"); !strings.Contains(all, "{rigctld}") {
		t.Fatal(all)
	}
}

func TestBasePathEnvNeedsNoMapping(t *testing.T) {
	m := load(t, "../../docs/plugins/examples/rigctlweb.json")
	m.Run.Command = []string{"{bin}", "-listen", "{listen}"}
	m.Run.BasePath = "env"
	if p := m.Validate(); len(p) > 0 {
		t.Fatalf("env mode reads HAMSTACK_BASE_PATH: %v", p)
	}
	m.Run.BasePath = "arg"
	if all := strings.Join(m.Validate(), "\n"); !strings.Contains(all, "uses {base_path}") {
		t.Fatalf("arg mode must pass it: %s", all)
	}
}

func TestMicrophoneNeedsAudioAndIsTheOnlyPermission(t *testing.T) {
	m := load(t, "../../docs/plugins/examples/rigctlweb.json")
	m.UI.Permissions = []string{"microphone"}
	if all := strings.Join(m.Validate(), "\n"); !strings.Contains(all, "needs.audio") {
		t.Fatal(all)
	}
	m.Needs.Audio = true
	if p := m.Validate(); len(p) > 0 {
		t.Fatal(p)
	}
	m.UI.Permissions = []string{"camera"}
	if all := strings.Join(m.Validate(), "\n"); !strings.Contains(all, "not supported") {
		t.Fatal(all)
	}
}
