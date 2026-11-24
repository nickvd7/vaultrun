"""Probe one sandbox. The first line is "contained" or "open:<what>".

A second line prints the injected E2E_TOKEN so the caller can check that the
API redacts it. Reaching metadata, the host, or the Docker socket is a failure.
"""

import os
import urllib.request

bad = []

if os.path.exists("/var/run/docker.sock"):
    bad.append("docker.sock")
if os.path.exists("/var/lib/vaultrun"):
    bad.append("host-workspaces")
if os.path.exists("/proc/1/root/var/lib/vaultrun"):
    bad.append("host-via-proc")

try:
    with open("/etc/escape-probe", "w") as handle:
        handle.write("x")
    bad.append("write-etc")
except OSError:
    pass


def gateway():
    try:
        lines = open("/proc/net/route")
    except OSError:
        return ""
    for line in lines:
        parts = line.split()
        if len(parts) < 3 or parts[1] != "00000000":
            continue
        hexip = parts[2]
        if len(hexip) != 8 or hexip == "00000000":
            continue
        return ".".join(str(int(hexip[i : i + 2], 16)) for i in (6, 4, 2, 0))
    return ""


gw = gateway()
if gw:
    try:
        urllib.request.urlopen("http://%s:8080/health" % gw, timeout=3)
        bad.append("host-api")
    except Exception:
        pass

try:
    urllib.request.urlopen("http://169.254.169.254/latest/meta-data/", timeout=3)
    bad.append("metadata")
except Exception:
    pass

print("contained" if not bad else "open:" + ",".join(bad))
print("token=" + os.environ.get("E2E_TOKEN", "missing"))
