"""Capability-to-implementation resolver.

Selects exactly one providing package for every requested capability while
honoring semantic versions, mutual-exclusion (conflict) declarations, target
constraints, and alternative (replaced_by) chains.
"""
from .errors import (
    BuildRequestError,
    CapabilityResolverError,
    CatalogError,
    ResolutionError,
)
from .loader import load_build_request, load_catalog
from .models import Catalog, Package, Requirement, Target
from .resolver import DECISION_POLICY, Resolution, Resolver
from .version import Constraint, Version, VersionError

__all__ = [
    "BuildRequestError",
    "CapabilityResolverError",
    "Catalog",
    "CatalogError",
    "DECISION_POLICY",
    "Constraint",
    "Package",
    "Requirement",
    "Resolution",
    "ResolutionError",
    "Resolver",
    "Target",
    "Version",
    "VersionError",
    "load_build_request",
    "load_catalog",
]
