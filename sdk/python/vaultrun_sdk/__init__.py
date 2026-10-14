"""VaultRun Python SDK — secure sandbox runtime for AI agents."""

from .client import (
    Client,
    Session,
    Run,
    AsyncRunResult,
    File,
    APIKey,
    CreatedKey,
    AuditLog,
    StreamResult,
    Organization,
    OrgMember,
    Snapshot,
    SharedArtifact,
    Image,
    SessionStats,
    PullStatus,
    VaultRunError,
)

__all__ = [
    "Client",
    "Session",
    "Run",
    "AsyncRunResult",
    "File",
    "APIKey",
    "CreatedKey",
    "AuditLog",
    "StreamResult",
    "Organization",
    "OrgMember",
    "Snapshot",
    "SharedArtifact",
    "Image",
    "SessionStats",
    "PullStatus",
    "VaultRunError",
]

try:
    from importlib.metadata import PackageNotFoundError, version as _version

    try:
        __version__ = _version("vaultrun-sdk")
    except PackageNotFoundError:
        __version__ = "0+unknown"
except ImportError:  # pragma: no cover
    __version__ = "0+unknown"
