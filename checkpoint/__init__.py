"""Checkpointing / rematerialisation toolkit (pure stdlib)."""
from .engine import (AuditMismatch, Engine, MemoryTracker, RecomputeError,
                     RunResult, Stats)
from .graph import Node, descendant_counts, topo_order
from .planner import (checkpoint_capacity, dag_lower_bound,
                      plan_chain_bruteforce, plan_chain_optimal,
                      plan_dag_greedy, segment_cost)
from .rng import Rng
from .simulator import SimViolation, simulate
from .tensor import Tensor, VersionError

__all__ = [
    "AuditMismatch", "Engine", "MemoryTracker", "Node", "RecomputeError",
    "Rng", "RunResult", "SimViolation", "Stats", "Tensor", "VersionError",
    "checkpoint_capacity", "dag_lower_bound", "descendant_counts",
    "plan_chain_bruteforce", "plan_chain_optimal", "plan_dag_greedy",
    "segment_cost", "simulate", "topo_order",
]
