package vaultrun

import rego.v1

# Example restrictive policy.
# Copy and adjust to match your threat model, then set:
#   OPA_POLICY_FILE=policy/restrictive.rego

# Commands that may never run, regardless of other rules.
blocked_commands := {
    "bash", "sh", "zsh", "fish", "dash",   # shell interpreters
    "curl", "wget", "nc", "ncat", "socat",  # network tools
    "sudo", "su",                            # privilege escalation
}

default allow_command := false

allow_command if {
    not input.command in blocked_commands
}

deny_reason := r if {
    input.command in blocked_commands
    r := concat("", ["command blocked by policy: ", input.command])
}

# Allow reads anywhere; restrict writes to /workspace
default allow_file := false

allow_file if {
    not input.write
}

allow_file if {
    input.write
    startswith(input.path, "/workspace/")
}
