package vaultrun

import rego.v1

# Production starting point. It is not AllowAll: a sandbox may not run the
# commands that change namespaces or mount filesystems, and it may not write
# outside its workspace and /tmp. Copy this file and tighten it for the
# commands your agents actually need, then point OPA_POLICY_FILE at the copy.
#
# make prod-env mounts this file at /etc/vaultrun/policy.rego in the API.

blocked_commands := {
	"nsenter", "unshare", "chroot", "mount", "umount", "pivot_root",
}

default allow_command := false

allow_command if {
	not input.command in blocked_commands
}

deny_reason := r if {
	input.command in blocked_commands
	r := concat("", ["command blocked by policy: ", input.command])
}

default allow_file := false

allow_file if {
	not input.write
}

allow_file if {
	input.write
	startswith(input.path, "/workspace/")
}

allow_file if {
	input.write
	startswith(input.path, "/tmp/")
}
