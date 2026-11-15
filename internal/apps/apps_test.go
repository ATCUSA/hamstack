package apps

import "testing"

func TestDisplaysAndPortsAreDistinct(t *testing.T) {
	seen := map[int]string{}
	for _, a := range All {
		if !ValidName(a.Name) {
			t.Errorf("%q is not a valid name", a.Name)
		}
		if o, dup := seen[a.Port()]; dup {
			t.Errorf("%s and %s share port %d", a.Name, o, a.Port())
		}
		seen[a.Port()] = a.Name
		if a.Port() == DesktopPort || a.Port() == 5900 {
			t.Errorf("%s collides with a desktop VNC port", a.Name)
		}
	}
	w, _ := Find("wsjtx")
	if w.Display() != FirstDisplay || w.Port() != 5900+FirstDisplay || w.Binary() != "wsjtx" || !w.Radio {
		t.Errorf("wsjtx: %d %d %s", w.Display(), w.Port(), w.Binary())
	}
	for _, bad := range []string{"", "../x", "a b", "x;rm", "WSJTX", "a.service"} {
		if ValidName(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}
