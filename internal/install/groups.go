package install

import "fmt"

// GroupStep adds username to a group (for example uucp or dialout, which own USB serial ports).
func GroupStep(e Env, group, username, why string) (Step, error) {
	cmd, err := e.priv("usermod", "-aG", group, username)
	if err != nil {
		return Step{}, err
	}
	return Step{Desc: fmt.Sprintf("Add you to the %q group (%s)", group, why), Cmd: cmd}, nil
}
