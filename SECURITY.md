# Security policy

## Reporting a vulnerability

Please report security problems **privately**: use the repository's **Security → Report a vulnerability** form
(GitHub private vulnerability reporting). Do not open a public issue or pull request for something exploitable.
You will get an acknowledgement and a plan; there is no bug bounty.

## What is in scope

hamstack runs on a person's own computer, installs software, writes systemd user units and, only with a command
shown first and an explicit yes, runs a few `sudo` steps. Things worth reporting:

- a way to make hamstack run a command, write a file or install something the user was not shown first
- shell or path injection in anything built from configuration, profile files, package names or text received from
  the network (the AGW client, gpsd, USB device names, SourceForge or GitHub listings)
- downloads accepted without the check hamstack says it makes (checksums, HTTPS-only redirects)
- anything that stores, logs or sends a Winlink password (hamstack is designed never to read or write one)
- a way for text arriving over the air to reach the terminal as control sequences

## Things that are by design

- **Network services are open on the local network.** Mercury's web UI and Direwolf's AGW and KISS ports listen on all
  interfaces with no authentication, because those programs do. `hamstack network` and `hamstack doctor` flag
  them. Use a firewall on an untrusted network.
- **Source builds run upstream code.** `--from-source` downloads a project's release source over HTTPS and builds it
  with your compiler. For several projects no checksum is published, so there is none to verify; hamstack says so
  before it starts.
- **Packages are installed with your package manager and sudo,** after showing the exact commands and asking.

## Supported versions

Only the latest release is supported.
