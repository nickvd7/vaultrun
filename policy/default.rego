package vaultrun

import rego.v1

# Default policy: allow everything.
# Set OPA_POLICY_FILE to a custom Rego file to enforce restrictions.

default allow_command := true
default allow_file := true
