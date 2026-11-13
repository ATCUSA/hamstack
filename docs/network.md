# Network

    hamstack network                    # also the Network tab (key 0) in the interface

Every port the stack uses (rigctld, each modem's ARQ and UI ports, Direwolf's AGW and KISS, Pat's web UI, gpsd), whether something
is listening, and **who can connect**: this computer only, or any computer on your network. That is read from what is really
listening (`/proc/net/tcp`), not from what the configuration expects. Mercury's web UI and Direwolf's AGW and KISS ports listen
on every interface with no password; they are flagged. It also shows this computer's addresses and the AX.25 kernel state.
hamstack cannot see your firewall rules.
