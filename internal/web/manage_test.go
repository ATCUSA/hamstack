package web

import "testing"

// Data2G's page can change its settings and key the radio: setting its password, or turning the modem pages on, needs
// the dashboard password again, like ardopcf's screen.
func TestModemPageChangesAreOwnerOnly(t *testing.T) {
	for _, r := range []ManagementRequest{{Action: "data2g.password", ID: "hf"}, {Action: "setting.set", ID: "ui"}, {Action: "rig.set", Value: "xara-registered", Extra: "on"}} {
		if !ownerOnly(r) {
			t.Errorf("%+v must need the dashboard password", r)
		}
	}
	if ownerOnly(ManagementRequest{Action: "setting.set", ID: "grid"}) {
		t.Error("an ordinary setting does not")
	}
}
