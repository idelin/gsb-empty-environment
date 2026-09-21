"""bitemp：双时态版本索引（业务时间 × 录入时间）。"""

from .core import INF, BitemporalStore, Version, resolve_winner
from .reference import ReferenceStore

__all__ = ["INF", "BitemporalStore", "ReferenceStore", "Version", "resolve_winner"]
