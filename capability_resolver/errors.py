"""Exception hierarchy for the capability resolver."""
from __future__ import annotations


class CapabilityResolverError(Exception):
    """Base class for all resolver errors."""


class CatalogError(CapabilityResolverError):
    """The package catalog JSON is missing required data or is inconsistent."""


class BuildRequestError(CapabilityResolverError):
    """The build request JSON is invalid."""


class ResolutionError(CapabilityResolverError):
    """No single-provider selection can satisfy the request."""
