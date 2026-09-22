"""Feature/dependency closure resolver over local JSON inputs."""

from .errors import (
    ConflictError,
    ExprError,
    FeatureResolverError,
    ModelError,
    ResolutionError,
    VersionError,
)
from .model import Dependency, Package, Registry, RootRequest
from .resolver import Diagnostic, Resolution, ResolvedPackage, Resolver
from .version import Requirement, Version

__all__ = [
    "ConflictError",
    "Dependency",
    "Diagnostic",
    "ExprError",
    "FeatureResolverError",
    "ModelError",
    "Package",
    "Registry",
    "Requirement",
    "Resolution",
    "ResolutionError",
    "ResolvedPackage",
    "Resolver",
    "RootRequest",
    "Version",
    "VersionError",
]
