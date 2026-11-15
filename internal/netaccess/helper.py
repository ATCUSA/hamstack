#!/usr/bin/python3
"""Root-owned, narrow nftables helper. Never executes user-provided commands.

Each invoking sudo user's policy controls only that user's hamstack_<uid> table.
The watcher reads root-owned snapshots, never a user's changing config or code.
"""
import fcntl
import ipaddress
import json
import os
import pathlib
import subprocess
import sys
import tempfile
import time

ROOT = pathlib.Path('/var/lib/hamstack-network-access')

def validate(value):
    if not isinstance(value, list) or len(value) > 256:
        raise ValueError('policy must be a list of at most 256 ports')
    result = {}
    for row in value:
        if not isinstance(row, dict) or set(row) != {'port', 'mode'}:
            raise ValueError('each policy needs only port and mode')
        port, mode = row['port'], row['mode']
        if type(port) is not int or not 1 <= port <= 65535:
            raise ValueError('invalid TCP port')
        if mode not in ('local', 'network', 'all'):
            raise ValueError('invalid access mode')
        if port in result and result[port] != mode:
            raise ValueError('conflicting policies for one port')
        result[port] = mode
    return [{'port': p, 'mode': result[p]} for p in sorted(result)]

def tool(name):
    # Fixed system directories only; never inherit a caller-controlled PATH.
    for directory in ('/usr/sbin', '/usr/bin', '/sbin', '/bin'):
        path = directory + '/' + name
        if os.path.isfile(path) and os.access(path, os.X_OK):
            return path
    raise ValueError(name + ' is not installed in a system directory')

def addresses():
    data = json.loads(subprocess.check_output([tool('ip'), '-j', 'address', 'show'], timeout=10))
    result = []
    for interface in data:
        if interface.get('ifname') == 'lo' or interface.get('operstate') not in ('UP', 'UNKNOWN'):
            continue
        for addr in interface.get('addr_info', []):
            if addr.get('scope') != 'global':
                continue
            ip = ipaddress.ip_address(addr['local'])
            network = ipaddress.ip_network(str(ip) + '/' + str(addr['prefixlen']), strict=False)
            result.append((str(ip), str(network), ip.version))
    return sorted(set(result))

def rules(uid, policy, lan, exists):
    table = 'hamstack_' + str(int(uid))
    lines = ['delete table inet ' + table] if exists else []
    lines += ['table inet ' + table + ' {', 'chain input {', 'type filter hook input priority -5; policy accept;']
    for row in validate(policy):
        port, mode = row['port'], row['mode']
        if mode == 'all':
            continue
        lines.append('tcp dport %d iifname "lo" accept' % port)
        if mode == 'network':
            for host, network, version in lan:
                host = str(ipaddress.ip_address(host))
                network = str(ipaddress.ip_network(network, strict=False))
                family = 'ip' if version == 4 else 'ip6'
                lines.append('tcp dport %d %s daddr %s %s saddr %s accept' % (port, family, host, family, network))
        lines.append('tcp dport %d drop' % port)
    return '\n'.join(lines + ['}', '}', ''])

def apply(uid, policy, lan):
    table = 'hamstack_' + str(int(uid))
    exists = subprocess.run([tool('nft'), 'list', 'table', 'inet', table], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=10).returncode == 0
    body = rules(uid, policy, lan, exists)
    # nft's batch is transactional: an invalid batch leaves the previous policy active.
    subprocess.run([tool('nft'), '-f', '-'], input=body, text=True, check=True, timeout=15)

def main():
    if os.geteuid() != 0:
        raise ValueError('requires root via the installed sudo rule')
    ROOT.mkdir(mode=0o700, parents=True, exist_ok=True)
    with open(ROOT / 'lock', 'a') as lock:
        if sys.argv[1:] == ['update']:
            uid = int(os.environ.get('SUDO_UID', os.getuid()))
            if uid < 0:
                raise ValueError('invalid uid')
            policy = validate(json.loads(sys.stdin.read(65537)))
            allowed = json.loads(pathlib.Path('/etc/hamstack-network-access', str(uid) + '.json').read_text())
            if any(row['port'] not in allowed for row in policy):
                raise ValueError('port not approved; review and reinstall the firewall helper to approve new endpoints')
            fcntl.flock(lock, fcntl.LOCK_EX)
            apply(uid, policy, addresses())
            fd, path = tempfile.mkstemp(dir=ROOT, prefix='.policy-')
            try:
                with os.fdopen(fd, 'w') as out:
                    json.dump(policy, out)
                os.replace(path, ROOT / (str(uid) + '.json'))
            finally:
                if os.path.exists(path):
                    os.unlink(path)
            print('Firewall policy applied; local clients remain allowed. LAN prefixes are refreshed every five seconds.')
        elif sys.argv[1:] == ['watch']:
            previous = None
            while True:
                try:
                    lan = addresses()
                    policies = [(p, p.read_text()) for p in sorted(ROOT.glob('*.json')) if p.stem.isdigit()]
                    fingerprint = repr((lan, policies))
                    if fingerprint != previous:
                        fcntl.flock(lock, fcntl.LOCK_EX)
                        try:
                            for path, _ in policies:
                                # Read again under the lock so updates cannot be overwritten by a stale snapshot.
                                apply(int(path.stem), validate(json.loads(path.read_text())), lan)
                        finally:
                            fcntl.flock(lock, fcntl.LOCK_UN)
                        previous = fingerprint
                except Exception as error:
                    print('network policy refresh failed: ' + str(error), file=sys.stderr, flush=True)
                time.sleep(5)
        else:
            raise ValueError('expected update or watch')

if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)
