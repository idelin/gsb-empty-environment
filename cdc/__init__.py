"""CDC 处理器差分测试框架: 确定性流生成 + 独立 oracle + 自动缩减。"""
from .events import (
    Abort,
    Begin,
    Change,
    Commit,
    Crash,
    Event,
    ProcessorError,
    StreamValidationError,
    freeze_row,
    thaw_row,
)
from .fixtures import TABLES
from .generator import GeneratedStream, generate
from .harness import RunResult, diff_states, run_case
from .oracle import compute_final_state
from .processor import ArrivalOrderProcessor, IncrementalProcessor
from .shrinker import shrink
from .validate import validate_stream

__all__ = [
    "Abort",
    "ArrivalOrderProcessor",
    "Begin",
    "Change",
    "Commit",
    "Crash",
    "Event",
    "GeneratedStream",
    "IncrementalProcessor",
    "ProcessorError",
    "RunResult",
    "StreamValidationError",
    "TABLES",
    "compute_final_state",
    "diff_states",
    "freeze_row",
    "generate",
    "run_case",
    "shrink",
    "thaw_row",
    "validate_stream",
]
