# Network access

`hamstack network` lists the stack's ports, observed listeners, addresses and AX.25 state. The Network tab is key `0` in the TUI. Its listener observations come from `/proc/net/tcp` and describe where a process is bound; they do not prove that another firewall permits or blocks a connection.

```sh
hamstack network access
hamstack network access web network --restart
hamstack network access pat local --restart
hamstack network access data2g@hf/web network --restart
hamstack network firewall install --dry-run
hamstack network firewall install
hamstack network access mercury@hf/8300 local --firewall --restart
```

The TUI uses `v` on an endpoint to review its access and `F` to review firewall setup. The browser Network section has corresponding controls. The choices are `local` (this computer), `network` (a detected local-network address), and `all` (all IPv4 interfaces). Native listeners in network mode follow address changes by restarting their child process; if there is no LAN address, they fall back to loopback. Preferences are recorded for the current site when one is active, or as a machine default otherwise. A change offers a service restart; active radio sessions may be interrupted.

For an endpoint without a supported native bind setting, the optional nftables helper can enforce a choice on its TCP port. Review its installation plan before accepting the root-owned helper, address watcher, approved-port list and narrow sudo permission. Additional ports require reviewing the helper installation again. Restrictions apply to every listener on that TCP port; loopback stays available to local clients. `network` permits peers on detected directly connected IPv4/IPv6 prefixes, refreshed every five seconds. `all` removes hamstack's extra filter, but the application must also listen beyond loopback. Other firewall rules remain in place and can still block access.

The dashboard and Pat have their own access choices. Mercury's UI and Direwolf's AGW/KISS interfaces may be reachable without their own password if exposed. The dashboard uses HTTPS and a separate login; its VNC relay connects to VNC servers on loopback. See [Browser dashboard and remote access](remote-access.md).
