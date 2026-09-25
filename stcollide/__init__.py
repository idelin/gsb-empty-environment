"""stcollide：分段匀速轴对齐盒的精确时空碰撞检测。"""

from .core import F, vec
from .world import CollisionInterval, CollisionRecord, SimResult, World

__all__ = ["F", "vec", "World", "CollisionInterval", "CollisionRecord", "SimResult"]
