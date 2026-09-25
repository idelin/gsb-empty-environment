from __future__ import annotations

from dataclasses import dataclass

from predicate_tree import (
    And,
    Block,
    Column,
    ColumnStats,
    ColumnType,
    Comparison,
    ComparisonOp,
    IsNotNull,
    IsNull,
    Not,
    Or,
    Predicate,
    TriBool,
    not3,
    typecheck_predicate,
)


@dataclass(frozen=True)
class SkipDecision:
    skip: bool
    possible_outcomes: frozenset[TriBool]
    reason_code: str | None
    reason: str | None


def analyze_block(predicate: Predicate, block: Block) -> SkipDecision:
    typecheck_predicate(predicate)
    if block.row_count == 0:
        return SkipDecision(True, frozenset(), "EMPTY_BLOCK", "块不包含任何行")
    outcomes = _outcomes(predicate, block)
    if TriBool.TRUE in outcomes:
        return SkipDecision(False, outcomes, None, None)
    return SkipDecision(
        True,
        outcomes,
        "NO_POSSIBLE_TRUE",
        _absence_reason(predicate, block, TriBool.TRUE),
    )


def block_outcomes(predicate: Predicate, block: Block) -> frozenset[TriBool]:
    typecheck_predicate(predicate)
    if block.row_count <= 0:
        return frozenset()
    return _outcomes(predicate, block)


def _outcomes(predicate: Predicate, block: Block) -> frozenset[TriBool]:
    if isinstance(predicate, IsNull):
        return _null_outcomes(_stats(predicate.column, block), positive=True)
    if isinstance(predicate, IsNotNull):
        return _null_outcomes(_stats(predicate.column, block), positive=False)
    if isinstance(predicate, Not):
        return frozenset({not3(value) for value in _outcomes(predicate.operand, block)})
    if isinstance(predicate, And):
        return frozenset(
            _and_value(left, right)
            for left in _outcomes(predicate.left, block)
            for right in _outcomes(predicate.right, block)
        )
    if isinstance(predicate, Or):
        return frozenset(
            _or_value(left, right)
            for left in _outcomes(predicate.left, block)
            for right in _outcomes(predicate.right, block)
        )
    return _comparison_outcomes(predicate, _stats(predicate.column, block))


def _and_value(left: TriBool, right: TriBool) -> TriBool:
    if left is TriBool.FALSE or right is TriBool.FALSE:
        return TriBool.FALSE
    if left is TriBool.UNKNOWN or right is TriBool.UNKNOWN:
        return TriBool.UNKNOWN
    return TriBool.TRUE


def _or_value(left: TriBool, right: TriBool) -> TriBool:
    if left is TriBool.TRUE or right is TriBool.TRUE:
        return TriBool.TRUE
    if left is TriBool.UNKNOWN or right is TriBool.UNKNOWN:
        return TriBool.UNKNOWN
    return TriBool.FALSE


def _stats(column: Column, block: Block) -> ColumnStats:
    if column in block.stats:
        stats = block.stats[column]
    else:
        matches = [
            candidate_stats
            for candidate, candidate_stats in block.stats.items()
            if candidate.name == column.name and candidate.type is column.type
        ]
        if len(matches) != 1:
            raise KeyError(f"missing statistics for column {column.name!r}")
        stats = matches[0]
    _validate_stats(column, block.row_count, stats)
    return stats


def _validate_stats(column: Column, row_count: int, stats: ColumnStats) -> None:
    if row_count < 0:
        raise ValueError("row_count cannot be negative")
    if column.type is not ColumnType.FLOAT and stats.nan_count:
        raise ValueError("nan_count is valid only for float columns")
    if stats.null_count + stats.nan_count + stats.value_count != row_count:
        raise ValueError("null_count + nan_count + value_count must equal row_count")
    if stats.value_count:
        if stats.minimum is None or stats.maximum is None:
            raise ValueError("value_count requires minimum and maximum")
        _validate_bound(column, stats.minimum)
        _validate_bound(column, stats.maximum)
        if stats.minimum > stats.maximum:
            raise ValueError("minimum cannot exceed maximum")
    elif stats.minimum is not None or stats.maximum is not None:
        raise ValueError("minimum and maximum must be absent when value_count is zero")


def _validate_bound(column: Column, value: object) -> None:
    if column.type is ColumnType.INTEGER:
        valid = isinstance(value, int) and not isinstance(value, bool)
    elif column.type is ColumnType.FLOAT:
        valid = isinstance(value, float) and not isinstance(value, bool)
    elif column.type is ColumnType.BOOLEAN:
        valid = isinstance(value, bool)
    else:
        valid = isinstance(value, str)
    if not valid:
        raise ValueError(f"invalid bound for {column.type.value} column")


def _null_outcomes(stats: ColumnStats, positive: bool) -> frozenset[TriBool]:
    outcomes: set[TriBool] = set()
    if stats.null_count:
        outcomes.add(TriBool.TRUE if positive else TriBool.FALSE)
    if stats.nan_count or stats.value_count:
        outcomes.add(TriBool.FALSE if positive else TriBool.TRUE)
    return frozenset(outcomes)


def _comparison_outcomes(predicate: Comparison, stats: ColumnStats) -> frozenset[TriBool]:
    outcomes: set[TriBool] = set()
    if stats.null_count:
        outcomes.add(TriBool.UNKNOWN)

    literal = predicate.literal.value
    literal_is_nan = isinstance(literal, float) and literal != literal
    non_null_count = stats.nan_count + stats.value_count

    if literal_is_nan:
        if non_null_count:
            outcomes.add(
                TriBool.TRUE if predicate.op is ComparisonOp.NE else TriBool.FALSE
            )
        return frozenset(outcomes)

    if stats.nan_count:
        outcomes.add(TriBool.TRUE if predicate.op is ComparisonOp.NE else TriBool.FALSE)

    if stats.value_count:
        if _finite_true_possible(predicate.op, stats, literal):
            outcomes.add(TriBool.TRUE)
        if _finite_false_possible(predicate.op, stats, literal):
            outcomes.add(TriBool.FALSE)
    return frozenset(outcomes)


def _finite_true_possible(
    op: ComparisonOp, stats: ColumnStats, literal: object
) -> bool:
    if op is ComparisonOp.EQ:
        return stats.minimum <= literal <= stats.maximum
    if op is ComparisonOp.NE:
        return not _all_finite_equal(stats, literal)
    if op is ComparisonOp.LT:
        return stats.minimum < literal
    if op is ComparisonOp.LE:
        return stats.minimum <= literal
    if op is ComparisonOp.GT:
        return stats.maximum > literal
    return stats.maximum >= literal


def _finite_false_possible(
    op: ComparisonOp, stats: ColumnStats, literal: object
) -> bool:
    if op is ComparisonOp.EQ:
        return not _all_finite_equal(stats, literal)
    if op is ComparisonOp.NE:
        return stats.minimum <= literal <= stats.maximum
    if op is ComparisonOp.LT:
        return stats.maximum >= literal
    if op is ComparisonOp.LE:
        return stats.maximum > literal
    if op is ComparisonOp.GT:
        return stats.minimum <= literal
    return stats.minimum < literal


def _all_finite_equal(stats: ColumnStats, literal: object) -> bool:
    return stats.minimum == literal and stats.maximum == literal


def _absence_reason(
    predicate: Predicate, block: Block, target: TriBool
) -> str:
    if isinstance(predicate, Comparison):
        return _comparison_absence_reason(predicate, _stats(predicate.column, block), target)
    if isinstance(predicate, IsNull):
        return _null_absence_reason(predicate.column, _stats(predicate.column, block), True, target)
    if isinstance(predicate, IsNotNull):
        return _null_absence_reason(predicate.column, _stats(predicate.column, block), False, target)
    if isinstance(predicate, Not):
        inner_target = _not_inverse(target)
        if target is TriBool.UNKNOWN:
            return "NOT 不改变 unknown；子表达式也不可能为 unknown"
        return (
            f"NOT 将 {inner_target.value} 映射为 {target.value}；"
            + _absence_reason(predicate.operand, block, inner_target)
        )
    if isinstance(predicate, And):
        left = _outcomes(predicate.left, block)
        right = _outcomes(predicate.right, block)
        if TriBool.TRUE not in left:
            return "AND 左子树不可能为 true：" + _absence_reason(predicate.left, block, TriBool.TRUE)
        if TriBool.TRUE not in right:
            return "AND 右子树不可能为 true：" + _absence_reason(predicate.right, block, TriBool.TRUE)
        return "AND 不存在产生目标结果的组合"
    left = _outcomes(predicate.left, block)
    right = _outcomes(predicate.right, block)
    if TriBool.TRUE not in left and TriBool.TRUE not in right:
        return "OR 的左右子树均不可能为 true"
    return "OR 不可能产生目标结果"


def _not_inverse(value: TriBool) -> TriBool:
    if value is TriBool.TRUE:
        return TriBool.FALSE
    if value is TriBool.FALSE:
        return TriBool.TRUE
    return TriBool.UNKNOWN


def _null_absence_reason(
    column: Column, stats: ColumnStats, positive: bool, target: TriBool
) -> str:
    non_null_count = stats.nan_count + stats.value_count
    wanted_null_branch = target is (TriBool.TRUE if positive else TriBool.FALSE)
    if wanted_null_branch:
        return f"{column.name} 的 null_count={stats.null_count}，块内不存在 NULL"
    if non_null_count == 0:
        return f"{column.name} 全为 NULL；非 NULL 分支不存在（NaN 也会计为非 NULL）"
    return f"{column.name} 的非 NULL 值数量为 {non_null_count}，NaN 与 NULL 分开计数"


def _comparison_absence_reason(
    predicate: Comparison, stats: ColumnStats, target: TriBool
) -> str:
    column_name = predicate.column.name
    if target is not TriBool.TRUE:
        return f"{column_name} 的统计信息证明不可能得到 {target.value}"
    if stats.value_count == 0 and stats.nan_count == 0:
        return f"{column_name} 全为 NULL；比较谓词只能得到 unknown"

    literal = predicate.literal.value
    if isinstance(literal, float) and literal != literal:
        if predicate.op is ComparisonOp.NE:
            return "表达式不可能为 true 的假设与 NaN 的 <> 语义矛盾"
        return "所有非 NULL 值与 NaN 做该比较均为 false；NULL 仅产生 unknown"

    op = predicate.op
    if op is ComparisonOp.EQ:
        detail = f"字面量 {literal!r} 位于有限值范围 [{stats.minimum!r}, {stats.maximum!r}] 之外"
    elif op is ComparisonOp.NE:
        detail = f"所有有限值都等于 {literal!r}，且块内没有 NaN"
    elif op is ComparisonOp.LT:
        detail = f"最小值 {stats.minimum!r} >= {literal!r}"
    elif op is ComparisonOp.LE:
        detail = f"最小值 {stats.minimum!r} > {literal!r}"
    elif op is ComparisonOp.GT:
        detail = f"最大值 {stats.maximum!r} <= {literal!r}"
    else:
        detail = f"最大值 {stats.maximum!r} < {literal!r}"
    suffix = []
    if stats.null_count:
        suffix.append("NULL 只产生 unknown")
    if stats.nan_count:
        suffix.append("NaN 对该比较只产生 false")
    return detail + ("；" + "，".join(suffix) if suffix else "")
