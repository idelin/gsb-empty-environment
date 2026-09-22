"""Local-only staged build planner.

Two dependency kinds are modelled:

* soft package dependencies (``depends_on``) which may form cycles; such a
  strongly connected component is compiled together as one group;
* hard generator/artifact edges (``produces`` / ``needs_artifacts``) which
  impose strict stage order.  A cycle containing a hard edge is a forbidden
  cross-stage cycle and is rejected with concrete evidence.
"""

from .version import Constraint, Version, VersionError

__all__ = ["Constraint", "Version", "VersionError"]
