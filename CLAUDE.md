@AGENTS.md

# Claude Code notes

- Run `task check` before saying a change is done; run the relevant `scripts/incus-test.sh --only ...` scenarios for
  anything that touches systemd, packages or the network, and report the actual results (including failures).
- Do not run `install`, `setup`, `units`, `start`, `ax25` or `gps use` on the machine you are on: use the incus containers.
- Keep personal data out of everything you write (see the hard rules in AGENTS.md).
