"""Deprecated alias for :mod:`vaultrun_sdk`.

``pip install vaultrun-sdk`` provides the ``vaultrun_sdk`` package. This module
only exists so code written against the old import name keeps working.
"""

import sys
import warnings

import vaultrun_sdk as _impl
from vaultrun_sdk import *  # noqa: F401,F403
from vaultrun_sdk import __all__, __version__  # noqa: F401

warnings.warn(
    "'sandbox_sdk' is deprecated; use 'import vaultrun_sdk' (package vaultrun-sdk).",
    DeprecationWarning,
    stacklevel=2,
)

sys.modules[__name__ + ".client"] = _impl.client
