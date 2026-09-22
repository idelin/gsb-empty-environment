"""Error hierarchy for the feature resolver."""


class FeatureResolverError(Exception):
    """Base class for all errors raised by this package."""


class VersionError(FeatureResolverError, ValueError):
    """Invalid version or version requirement."""


class ExprError(FeatureResolverError, ValueError):
    """Invalid feature boolean expression."""


class ModelError(FeatureResolverError, ValueError):
    """Invalid registry or request document."""


class ResolutionError(FeatureResolverError):
    """The closure cannot be computed (unsatisfiable input)."""


class ConflictError(ResolutionError):
    """An explicitly disabled feature is transitively required."""
