"""shardstitch：分片局部结论 + 边界假设的拼接验证器。"""
from .model import Assumption, Fact, FactRef, Proof, Rule, Shard, Step
from .replay import ReplayError, replay
from .stitch import Rejection, Stitcher, StitchReport

__all__ = [
    "Assumption",
    "Fact",
    "FactRef",
    "Proof",
    "Rule",
    "Shard",
    "Step",
    "ReplayError",
    "replay",
    "Rejection",
    "Stitcher",
    "StitchReport",
]
