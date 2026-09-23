"""差分 harness: 用独立 oracle 的最终全量状态验证处理器。

四种结果严格分开:
- ok              处理器最终状态与 oracle 一致
- mismatch        状态不一致(差分失败)
- invalid_stream  流本身无效(StreamValidationError), 不进入处理器比对
- processor_error 流有效但处理器抛出 ProcessorError
"""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Callable, Dict, List, Optional, Tuple

from .events import ProcessorError, StreamValidationError
from .oracle import compute_final_state
from .processor import IncrementalProcessor


@dataclass
class RunResult:
    status: str
    expected: Optional[Dict] = None
    actual: Optional[Dict] = None
    diffs: List[Tuple] = field(default_factory=list)
    error: Optional[Exception] = None

    @property
    def ok(self) -> bool:
        return self.status == "ok"


def diff_states(expected: Dict, actual: Dict) -> List[Tuple]:
    diffs = []
    for table in sorted(set(expected) | set(actual)):
        e_rows, a_rows = expected.get(table, {}), actual.get(table, {})
        for pk in sorted(set(e_rows) | set(a_rows)):
            e_val, a_val = e_rows.get(pk), a_rows.get(pk)
            if e_val != a_val:
                diffs.append((table, pk, e_val, a_val))
    return diffs


def run_case(
    stream,
    tables,
    processor_factory: Callable = IncrementalProcessor,
) -> RunResult:
    try:
        expected = compute_final_state(stream, tables)
    except StreamValidationError as exc:
        return RunResult("invalid_stream", error=exc)

    processor = processor_factory(tables)
    try:
        processor.run(stream)
    except ProcessorError as exc:
        return RunResult("processor_error", expected=expected, error=exc)

    actual = processor.state()
    diffs = diff_states(expected, actual)
    if diffs:
        return RunResult("mismatch", expected=expected, actual=actual, diffs=diffs)
    return RunResult("ok", expected=expected, actual=actual)
