"""二项序贯概率比检验 (SPRT)：精确递推 + 经验模拟 + 多流共享预算。"""
from .config import SprtConfig, log_ratio
from .evidence import Decision, Evidence
from .runner import SeededBernoulli, ScriptedSampler, run_once, TrialRecord
from .exact import exact_performance, exact_operating_points, ExactReport
from .simulation import simulate, EmpiricalReport
from .multistream import MultiStreamStudy, DeclaredRule, StreamStatus

__all__ = [
    "SprtConfig",
    "log_ratio",
    "Decision",
    "Evidence",
    "SeededBernoulli",
    "ScriptedSampler",
    "run_once",
    "TrialRecord",
    "ExactReport",
    "exact_performance",
    "exact_operating_points",
    "EmpiricalReport",
    "simulate",
    "MultiStreamStudy",
    "DeclaredRule",
    "StreamStatus",
]
