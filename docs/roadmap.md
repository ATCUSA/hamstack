# Roadmap

## Available now

The CLI and terminal interface share the configuration and backend operations. The optional HTTPS dashboard provides service and station controls, a program catalog, network and maintenance views, and browser access to supported desktop apps. Its optional console exposes the existing CLI and TUI for advanced interactive operations. Updates are checked at startup and periodically while the TUI or dashboard is running.

## Next steps

- **More direct browser controls:** replace remaining console-only guided workflows with accessible native forms and progress reporting, including operations that need interactive privilege approval.
- **Certificates:** offer configuration of ACME-issued certificates alongside the existing generated and user-supplied certificates.
- **Remote desktop compatibility:** test X11 and supported Wayland compositors on more distributions and improve diagnostics when capture or input protocols are missing.
- **Hardware and radio coverage:** expand real-radio, sound-interface, GPS and on-air reports; broaden coverage beyond simulated and container scenarios.
- **Network policy validation:** exercise firewall installation, address changes, site switches and recovery under real systemd across supported distributions.

These are directions for future work, not promises that a particular device or deployment has been verified. See [Testing](testing.md) and [Hardware testing](hardware-testing.md).
