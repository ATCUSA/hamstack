package ax25

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestKernel(t *testing.T) {
	root := t.TempDir()
	write(t, root, "proc/sys/kernel/osrelease", "7.2.15-1\n")
	if k, rel := Kernel(root); k != KernelMissing || rel != "7.2.15-1" {
		t.Errorf("no module: %v %q", k, rel)
	}
	write(t, root, "usr/lib/modules/7.2.15-1/kernel/net/ax25/ax25.ko.zst", "")
	if k, _ := Kernel(root); k != KernelAvailable {
		t.Errorf("module present: %v", k)
	}
	write(t, root, "sys/module/ax25/version", "")
	if k, _ := Kernel(root); k != KernelLoaded {
		t.Errorf("loaded: %v", k)
	}
	// A module of another kernel version is of no use.
	other := t.TempDir()
	write(t, other, "proc/sys/kernel/osrelease", "7.2.15-1")
	write(t, other, "usr/lib/modules/6.12.0/kernel/net/ax25/ax25.ko", "")
	if k, _ := Kernel(other); k != KernelMissing {
		t.Errorf("another kernel's module: %v", k)
	}
}

const sample = `# /etc/ax25/axports
#
#1	OH2BNS-1	1200	255	2	144.675 MHz (1200  bps)
`

func TestUpsert(t *testing.T) {
	p := DefaultPort("N0CALL-1")
	out, changed := Upsert(sample, p)
	if !changed || !strings.HasPrefix(out, sample) || !strings.HasSuffix(out, "hamstack\tN0CALL-1\t1200\t255\t2\thamstack (Direwolf)\n") {
		t.Errorf("appended wrongly:\n%s", out)
	}
	if got := ParsePorts(out); len(got) != 1 || got[0].Name != "hamstack" || got[0].Call != "N0CALL-1" || got[0].Speed != 1200 || got[0].Desc != "hamstack (Direwolf)" {
		t.Errorf("ParsePorts = %+v", got)
	}
	if again, changed := Upsert(out, p); changed || again != out {
		t.Error("the same port again must change nothing")
	}
	p.Call = "N0CALL-9"
	out2, changed := Upsert(out, p)
	if !changed || strings.Count(out2, "hamstack\t") != 1 || !strings.Contains(out2, "N0CALL-9") || strings.Contains(out2, "N0CALL-1\t") || !strings.HasPrefix(out2, sample) {
		t.Errorf("replaced wrongly:\n%s", out2)
	}
	if out3, _ := Upsert("", p); out3 != p.Line()+"\n" {
		t.Errorf("empty file: %q", out3)
	}
	// Another port with its own name is left alone.
	other := "vhf\tK0TEST\t1200\t255\t2\tx\n"
	out4, _ := Upsert(other, p)
	if !strings.HasPrefix(out4, other) || len(ParsePorts(out4)) != 2 {
		t.Errorf("clobbered another port:\n%s", out4)
	}
}

func TestInterfaces(t *testing.T) {
	root := t.TempDir()
	write(t, root, "sys/class/net/eth0/type", "1\n")
	write(t, root, "sys/class/net/ax0/type", "3\n")
	write(t, root, "sys/class/net/lo/type", "772\n")
	if got := Interfaces(root); len(got) != 1 || got[0].Name != "ax0" {
		t.Errorf("got %+v", got)
	}
}

func TestValidPortName(t *testing.T) {
	for s, want := range map[string]bool{"hamstack": true, "vhf": true, "": false, "toolongname": false, "a b": false, "a;b": false} {
		if ValidPortName(s) != want {
			t.Errorf("%q: want %v", s, want)
		}
	}
}
