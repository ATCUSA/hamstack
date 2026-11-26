package radiosetup

import (
	"slices"
	"testing"
)

func TestEveryProgramGetsOneProfileOfItsOperation(t *testing.T) {
	models := map[int]bool{}
	for _, p := range All() {
		models[p.HamlibModel] = true
	}
	if len(models) == 0 {
		t.Fatal("no bundled profiles")
	}
	for m := range models {
		for _, u := range Uses {
			p, ok := Recommended(m, u.Name)
			if !ok {
				continue // not every radio has every kind of profile (the IC-7300MK2 has no Winlink ones)
			}
			if p.HamlibModel != m || p.Operation != u.Operation {
				t.Errorf("model %d, %s: got %s (%s, model %d)", m, u.Name, p.ID, p.Operation, p.HamlibModel)
			}
			if slices.Contains(p.Tags, "remote") {
				for _, q := range ForModel(m) {
					if q.Operation == u.Operation && !slices.Contains(q.Tags, "remote") {
						t.Errorf("model %d, %s: got the remote %s although %s is there", m, u.Name, p.ID, q.ID)
					}
				}
			}
		}
	}
}

func TestRecommendedProfiles(t *testing.T) {
	for _, tc := range []struct {
		model int
		use   string
		want  string
	}{
		{1042, "ardopcf", "yaesu-ftdx10/winlink-ardop"},
		{1042, "xara", "yaesu-ftdx10/winlink-vara"},
		{1042, "data2g", "yaesu-ftdx10/winlink-vara"},
		{1042, "wsjtx", "yaesu-ftdx10/ft8"},
		{1042, "js8call", "yaesu-ftdx10/ft8"},
		{1042, "fldigi", "yaesu-ftdx10/psk31"},
		{3094, "wsjtx", "icom-ic7300mk2/ft8-usb"}, // over the USB cable, not the LAN one listed first
		{3094, "ardopcf", ""},
		{1042, "direwolf", ""},
	} {
		p, ok := Recommended(tc.model, tc.use)
		if got := map[bool]string{true: p.ID}[ok]; got != tc.want {
			t.Errorf("%d %s: got %q, want %q", tc.model, tc.use, got, tc.want)
		}
	}
	vara, _ := ByID("yaesu-ftdx10/winlink-vara")
	if got := RecommendedFor(vara); !slices.Equal(got, []string{"xara", "Mercury", "Data2G"}) {
		t.Errorf("the VARA profile is recommended for %q", got)
	}
	if got := RecommendedFor(Profile{ID: "yaesu-ftdx10/ssb", HamlibModel: 1042, Operation: "ssb"}); got != nil {
		t.Errorf("no program runs SSB phone: %q", got)
	}
}
