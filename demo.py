from __future__ import annotations

import math
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent / "src"))

from block_pruning import analyze_block
from predicate_tree import Column, ColumnType, Comparison, ComparisonOp, IsNull, Literal, Not
from query import audit_pruning, scan_row_by_row, scan_with_pruning, typed_batch


def root_column(predicate):
    if hasattr(predicate, "column"):
        return predicate.column
    if hasattr(predicate, "operand"):
        return root_column(predicate.operand)
    return root_column(predicate.left)


def show(title, batches, predicate) -> None:
    print(f"\n{title}")
    print("=" * len(title))
    for block_id, batch in enumerate(batches):
        decision = analyze_block(predicate, batch.block)
        values = [row[root_column(predicate).name] for row in batch.rows]
        if decision.skip:
            print(f"块 {block_id} {values!r}: 跳过")
            print(f"  理由: {decision.reason_code} - {decision.reason}")
        else:
            print(f"块 {block_id} {values!r}: 扫描，可能结果 {sorted(v.value for v in decision.possible_outcomes)}")

    reference = scan_row_by_row(batches, predicate)
    pruned = scan_with_pruning(batches, predicate)
    report = audit_pruning(reference, pruned)
    print("逐行解释器选中:", report.reference_selected)
    print("裁剪扫描选中:  ", report.pruned_selected)
    print("审计一致:", report.ok)


def main() -> None:
    integer_x = Column("x", ColumnType.INTEGER)
    float_x = Column("x", ColumnType.FLOAT)

    integer_batches = [
        typed_batch([integer_x], [{"x": None}, {"x": None}]),
        typed_batch([integer_x], [{"x": 1}, {"x": 3}]),
        typed_batch([integer_x], [{"x": 0}, {"x": 2}]),
    ]
    show("整数批次：x > 2", integer_batches, Comparison(integer_x, ComparisonOp.GT, Literal(2)))
    show("整数批次：x IS NULL", integer_batches, IsNull(integer_x))

    nan_batches = [
        typed_batch([float_x], [{"x": 1.0}, {"x": math.nan}]),
        typed_batch([float_x], [{"x": 3.0}, {"x": math.nan}]),
    ]
    greater = Comparison(float_x, ComparisonOp.GT, Literal(2.0, ColumnType.FLOAT))
    show("浮点批次（含 NaN）：x > 2", nan_batches, greater)
    show("浮点批次（含 NaN）：NOT (x > 2)", nan_batches, Not(greater))


if __name__ == "__main__":
    main()
