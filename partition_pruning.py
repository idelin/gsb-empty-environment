from __future__ import annotations

from dataclasses import dataclass
from enum import Enum
from itertools import product
from typing import Any, Iterable, Mapping, Sequence

NULL = None
T, F, U = "true", "false", "unknown"


class FragmentType(Enum):
    RANGE = "range"
    ENUM = "enum"
    NULL = "null"


@dataclass(frozen=True)
class RangeFragment:
    name: str
    lower: int | None
    upper: int | None
    lower_inclusive: bool = True
    upper_inclusive: bool = True
    is_null: bool = False
    type: FragmentType = FragmentType.RANGE

    def __post_init__(self) -> None:
        lo = self.lower
        hi = self.upper
        if lo is not None and not self.lower_inclusive:
            lo += 1
        if hi is not None and not self.upper_inclusive:
            hi -= 1
        object.__setattr__(self, "lower", lo)
        object.__setattr__(self, "upper", hi)
        object.__setattr__(self, "lower_inclusive", True)
        object.__setattr__(self, "upper_inclusive", True)
        if lo is not None and hi is not None and lo > hi:
            raise ValueError(f"range fragment {self.name!r} is empty")

    def contains(self, value: int) -> bool:
        if value is NULL:
            return False
        return (self.lower is None or value >= self.lower) and (
            self.upper is None or value <= self.upper
        )


@dataclass(frozen=True)
class EnumFragment:
    name: str
    values: frozenset[Any]
    is_null: bool = False
    type: FragmentType = FragmentType.ENUM

    def contains(self, value: Any) -> bool:
        return value is not NULL and value in self.values


@dataclass(frozen=True)
class NullFragment:
    name: str = "null"
    type: FragmentType = FragmentType.NULL

    @property
    def is_null(self) -> bool:
        return True

    def contains(self, value: Any) -> bool:
        return value is NULL


@dataclass(frozen=True)
class ColumnSpec:
    name: str
    kind: str


@dataclass(frozen=True)
class Partition:
    id: str
    parts: tuple[RangeFragment | EnumFragment | NullFragment, ...]
    columns: tuple[ColumnSpec, ...]

    def contains(self, row: Mapping[str, Any]) -> bool:
        return all(f.contains(row[key.name]) for f, key in zip(self.parts, self.columns))


@dataclass(frozen=True)
class PartitionScheme:
    columns: tuple[ColumnSpec, ...]
    fragments_by_column: Mapping[str, tuple[Any, ...]]
    partitions: tuple[Partition, ...]

    @classmethod
    def build(
        cls,
        columns: Sequence[ColumnSpec | tuple[str, str]],
        fragments_by_column: Mapping[
            str, Sequence[RangeFragment | EnumFragment | NullFragment]
        ],
    ) -> "PartitionScheme":
        keys = tuple(col if isinstance(col, ColumnSpec) else ColumnSpec(*col) for col in columns)
        normalized: dict[str, tuple[Any, ...]] = {}
        for key in keys:
            frags = tuple(fragments_by_column[key.name])
            if key.kind == "integer":
                validate_ranges(key.name, frags)
            elif key.kind == "enum":
                validate_enums(key.name, frags)
            else:
                raise ValueError("column kind must be 'integer' or 'enum'")
            normalized[key.name] = frags

        partitions = []
        for combo in product(*(normalized[key.name] for key in keys)):
            partitions.append(
                Partition("|".join(f.name for f in combo), tuple(combo), keys)
            )
        return cls(keys, normalized, tuple(partitions))

    def route(self, row: Mapping[str, Any]) -> Partition:
        for partition in self.partitions:
            if partition.contains(row):
                return partition
        raise ValueError("row does not belong to any partition")


def validate_ranges(column: str, fragments: Sequence[Any]) -> None:
    ranges = [f for f in fragments if not f.is_null]
    nulls = [f for f in fragments if f.is_null]
    if len(nulls) > 1:
        raise ValueError(f"column {column!r} has multiple NULL fragments")
    if not ranges:
        raise ValueError(f"column {column!r} needs at least one non-NULL range")
    ordered = sorted(ranges, key=lambda f: (1 if f.lower is not None else 0, f.lower))
    cursor: int | None = None
    first = True
    for frag in ordered:
        if first:
            if frag.lower is not None:
                raise ValueError(f"column {column!r} range fragments must start at -infinity")
            first = False
        else:
            if frag.lower != cursor:
                raise ValueError(
                    f"column {column!r} range fragments have a gap or overlap near {frag.name!r}"
                )
        if frag.upper is None:
            cursor = None
        else:
            cursor = frag.upper + 1
    if cursor is not None:
        raise ValueError(f"column {column!r} range fragments must end at +infinity")


def validate_enums(column: str, fragments: Sequence[Any]) -> None:
    nulls = [f for f in fragments if f.is_null]
    if len(nulls) > 1:
        raise ValueError(f"column {column!r} has multiple NULL fragments")
    seen: set[Any] = set()
    for frag in fragments:
        if frag.is_null:
            continue
        values = set(frag.values)
        if not values:
            raise ValueError(f"enum fragment {frag.name!r} is empty")
        if seen & values:
            raise ValueError(f"enum fragments for {column!r} overlap")
        seen.update(values)


@dataclass(frozen=True)
class Col:
    name: str


@dataclass(frozen=True)
class Param:
    name: str


@dataclass(frozen=True)
class Lit:
    value: Any


@dataclass(frozen=True)
class Cmp:
    op: str
    left: Col | Param | Lit
    right: Col | Param | Lit


@dataclass(frozen=True)
class InList:
    value: Col | Param | Lit
    items: tuple[Col | Param | Lit, ...]
    negated: bool = False


@dataclass(frozen=True)
class IsNull:
    value: Col | Param | Lit
    negated: bool = False


@dataclass(frozen=True)
class And:
    children: tuple[Any, ...]


@dataclass(frozen=True)
class Or:
    children: tuple[Any, ...]


@dataclass(frozen=True)
class Not:
    child: Any


def and_(*children: Any) -> And:
    return And(tuple(children))


def or_(*children: Any) -> Or:
    return Or(tuple(children))


def not_(child: Any) -> Not:
    return Not(child)


def in_(value: Any, items: Iterable[Any]) -> InList:
    return InList(value, tuple(items), False)


def not_in(value: Any, items: Iterable[Any]) -> InList:
    return InList(value, tuple(items), True)


@dataclass(frozen=True)
class IntervalDomain:
    lower: int | None
    upper: int | None
    nullable: bool
    kind: str = "integer"

    def values(self) -> set[Any]:
        if self.lower is None or self.upper is None:
            return set()
        return set(range(self.lower, self.upper + 1))


@dataclass(frozen=True)
class SetDomain:
    values_: frozenset[Any]
    nullable: bool
    kind: str = "enum"

    def values(self) -> set[Any]:
        return set(self.values_)


@dataclass(frozen=True)
class NullDomain:
    nullable: bool = True
    kind: str = "null"

    def values(self) -> set[Any]:
        return {NULL}


@dataclass(frozen=True)
class UnknownDomain:
    nullable: bool = True
    kind: str = "unknown"

    def values(self) -> set[Any]:
        return set()


Domain = IntervalDomain | SetDomain | NullDomain | UnknownDomain


def negate_state(state: frozenset[str]) -> frozenset[str]:
    result: set[str] = set()
    if T in state:
        result.add(F)
    if F in state:
        result.add(T)
    if U in state:
        result.add(U)
    return frozenset(result)


def and_state(left: frozenset[str], right: frozenset[str]) -> frozenset[str]:
    return frozenset({sql_and(a, b) for a in left for b in right})


def or_state(left: frozenset[str], right: frozenset[str]) -> frozenset[str]:
    return frozenset({sql_or(a, b) for a in left for b in right})


def sql_and(left: str, right: str) -> str:
    if left == F or right == F:
        return F
    if left == T and right == T:
        return T
    return U


def sql_or(left: str, right: str) -> str:
    if left == T or right == T:
        return T
    if left == F and right == F:
        return F
    return U


def expr_nodes(expr: Any) -> Iterable[Any]:
    if isinstance(expr, (And, Or)):
        for child in expr.children:
            yield from expr_nodes(child)
    elif isinstance(expr, Not):
        yield from expr_nodes(expr.child)
    else:
        yield expr


def has_unbound(expr: Any, params: Mapping[str, Any]) -> bool:
    if isinstance(expr, Param):
        return expr.name not in params
    if isinstance(expr, Cmp):
        return has_unbound(expr.left, params) or has_unbound(expr.right, params)
    if isinstance(expr, (InList, IsNull)):
        items = getattr(expr, "items", ())
        return has_unbound(expr.value, params) or any(
            has_unbound(item, params) for item in items
        )
    if isinstance(expr, (And, Or)):
        return any(has_unbound(child, params) for child in expr.children)
    if isinstance(expr, Not):
        return has_unbound(expr.child, params)
    return False


def concrete_value(
    expr: Col | Param | Lit,
    row: Mapping[str, Any],
    params: Mapping[str, Any],
) -> Any:
    if isinstance(expr, Col):
        return row[expr.name]
    if isinstance(expr, Param):
        if expr.name not in params:
            raise KeyError(f"parameter {expr.name!r} is not bound")
        return params[expr.name]
    return expr.value


def concrete_compare(op: str, left: Any, right: Any) -> str:
    if left is NULL or right is NULL:
        return U
    try:
        result = {
            "=": left == right,
            "<>": left != right,
            "!=": left != right,
            "<": left < right,
            "<=": left <= right,
            ">": left > right,
            ">=": left >= right,
        }[op]
    except TypeError:
        return U
    return T if result else F


def concrete_eval(
    predicate: Any,
    row: Mapping[str, Any],
    params: Mapping[str, Any] | None = None,
) -> str:
    params = params or {}
    if has_unbound(predicate, params):
        return U
    if isinstance(predicate, Cmp):
        return concrete_compare(
            predicate.op,
            concrete_value(predicate.left, row, params),
            concrete_value(predicate.right, row, params),
        )
    if isinstance(predicate, InList):
        value = concrete_value(predicate.value, row, params)
        if value is NULL:
            return U
        values = [concrete_value(item, row, params) for item in predicate.items]
        if any(item is NULL for item in values):
            if value in values:
                return T
            return U
        result = value in values
        if predicate.negated:
            result = not result
        return T if result else F
    if isinstance(predicate, IsNull):
        value = concrete_value(predicate.value, row, params)
        result = value is NULL
        if predicate.negated:
            result = not result
        return T if result else F
    if isinstance(predicate, And):
        state = T
        for child in predicate.children:
            state = sql_and(state, concrete_eval(child, row, params))
        return state
    if isinstance(predicate, Or):
        state = F
        for child in predicate.children:
            state = sql_or(state, concrete_eval(child, row, params))
        return state
    if isinstance(predicate, Not):
        inverted = negate_state(frozenset({concrete_eval(predicate.child, row, params)}))
        return next(iter(inverted))
    raise TypeError(f"unsupported predicate: {type(predicate).__name__}")


def expression_domain(
    expr: Col | Param | Lit,
    partition: Partition,
    params: Mapping[str, Any],
    expected_kind: str | None = None,
) -> Domain:
    if isinstance(expr, Param):
        if expr.name not in params:
            return UnknownDomain(True)
        return scalar_domain(params[expr.name], expected_kind)
    if isinstance(expr, Lit):
        return scalar_domain(expr.value, expected_kind)
    column = next((item for item in partition.columns if item.name == expr.name), None)
    if column is None:
        return UnknownDomain(True)
    fragment = partition.parts[partition.columns.index(column)]
    if fragment.is_null:
        return NullDomain()
    if column.kind == "integer":
        return IntervalDomain(fragment.lower, fragment.upper, False)
    return SetDomain(fragment.values, False)


def scalar_domain(value: Any, kind: str | None = None) -> Domain:
    if value is NULL:
        return NullDomain()
    if kind == "enum":
        return SetDomain(frozenset({value}), False, "enum")
    return literal_domain(value)


def column_kind(expr: Any, partition: Partition) -> str | None:
    if isinstance(expr, Col):
        column = next((item for item in partition.columns if item.name == expr.name), None)
        return None if column is None else column.kind
    return None


def literal_domain(value: Any) -> Domain:
    if value is NULL:
        return NullDomain()
    if isinstance(value, bool) or isinstance(value, int):
        return IntervalDomain(value, value, False)
    return SetDomain(frozenset({value}), False, type(value).__name__)


def abstract_compare(op: str, left: Domain, right: Domain) -> frozenset[str]:
    if left.kind == "null" or right.kind == "null":
        return frozenset({U})
    if left.nullable or right.nullable:
        return compare_nonnull(op, left, right) | {U}
    return compare_nonnull(op, left, right)


def compare_nonnull(op: str, left: Domain, right: Domain) -> frozenset[str]:
    if left.kind == "unknown" or right.kind == "unknown":
        return frozenset({T, F})
    if left.kind == "integer" and right.kind == "integer":
        assert isinstance(left, IntervalDomain) and isinstance(right, IntervalDomain)
        return compare_intervals(op, left, right)
    if left.kind == right.kind and isinstance(left, SetDomain) and isinstance(right, SetDomain):
        return compare_sets(op, left, right)
    return frozenset({U})


def compare_intervals(op: str, left: IntervalDomain, right: IntervalDomain) -> frozenset[str]:
    a, b, c, d = left.lower, left.upper, right.lower, right.upper
    if op == "!=":
        op = "<>"
    if op == "=":
        overlap = not (
            (b is not None and c is not None and b < c)
            or (d is not None and a is not None and d < a)
        )
        all_equal = (
            a is not None and b is not None and c is not None and d is not None
            and a == b == c == d
        )
        return frozenset(({T} if overlap else set()) | ({F} if not all_equal else set()))
    if op == "<>":
        overlap = not (
            (b is not None and c is not None and b < c)
            or (d is not None and a is not None and d < a)
        )
        all_equal = (
            a is not None and b is not None and c is not None and d is not None
            and a == b == c == d
        )
        return frozenset(({T} if not all_equal else set()) | ({F} if overlap else set()))
    if op == "<":
        can_true = a is None or d is None or a < d
        can_false = a is not None and d is not None and a >= d
    elif op == "<=":
        can_true = a is None or d is None or a <= d
        can_false = a is not None and d is not None and a > d
    elif op == ">":
        can_true = b is None or c is None or b > c
        can_false = b is not None and c is not None and b <= c
    elif op == ">=":
        can_true = b is None or c is None or b >= c
        can_false = b is not None and c is not None and b < c
    else:
        raise ValueError(f"unsupported operator {op!r}")
    return frozenset(({T} if can_true else set()) | ({F} if can_false else set()))


def compare_sets(op: str, left: SetDomain, right: SetDomain) -> frozenset[str]:
    if op == "!=":
        op = "<>"
    common = bool(left.values_ & right.values_)
    same_singleton = len(left.values_) == 1 and left.values_ == right.values_
    if op == "=":
        return frozenset(({T} if common else set()) | ({F} if not same_singleton else set()))
    if op == "<>":
        return frozenset(({T} if not same_singleton else set()) | ({F} if common else set()))
    return frozenset({T, F})


def abstract_in(value: Domain, items: tuple[Domain, ...], negated: bool) -> frozenset[str]:
    compatible = tuple(
        item
        for item in items
        if item.kind == "null"
        or item.kind == "unknown"
        or item.kind == value.kind
    )
    if value.kind not in {"null", "unknown"} and len(compatible) != len(items):
        return frozenset({U}) if negated else frozenset({F, U})
    nonnull_items = tuple(
        item for item in compatible if item.kind != "null" and not item.nullable
    )
    can_true = _some_value_matched(value, nonnull_items) if value.kind != "null" else False
    can_unknown = (
        value.kind == "null"
        or value.nullable
        or value.kind == "unknown"
        or any(item.kind == "null" or item.nullable or item.kind == "unknown" for item in items)
    )
    can_false = False
    if value.kind not in {"null", "unknown"} and not (
        value.nullable and not _has_nonnull_value(value)
    ):
        can_false = not _all_values_matched(value, nonnull_items)
    elif value.kind == "unknown":
        can_false = True
    state = set()
    if can_true:
        state.add(T)
    if can_false:
        state.add(F)
    if can_unknown:
        state.add(U)
    if negated:
        state = negate_state(frozenset(state))
    return frozenset(state)


def _has_nonnull_value(domain: Domain) -> bool:
    if isinstance(domain, NullDomain):
        return False
    if isinstance(domain, SetDomain):
        return bool(domain.values_)
    if isinstance(domain, IntervalDomain):
        return True
    return True


def _all_values_matched(value: Domain, items: tuple[Domain, ...]) -> bool:
    if isinstance(value, SetDomain):
        candidates = set(value.values_)
    elif isinstance(value, IntervalDomain):
        if value.lower is None or value.upper is None:
            return False
        candidates = set(range(value.lower, value.upper + 1))
    else:
        return False
    covered: set[Any] = set()
    for item in items:
        if isinstance(item, SetDomain) and item.kind == value.kind:
            covered.update(item.values_ & candidates)
        elif isinstance(item, IntervalDomain) and item.kind == value.kind:
            if item.lower is not None and item.upper is not None:
                covered.update(candidates & set(range(item.lower, item.upper + 1)))
    return bool(candidates) and covered == candidates


def _some_value_matched(value: Domain, items: tuple[Domain, ...]) -> bool:
    if isinstance(value, SetDomain):
        return any(
            isinstance(item, SetDomain)
            and item.kind == value.kind
            and bool(value.values_ & item.values_)
            for item in items
        )
    if isinstance(value, IntervalDomain):
        for item in items:
            if not isinstance(item, IntervalDomain) or item.kind != value.kind:
                continue
            if value.lower is None or item.upper is None or value.lower <= item.upper:
                if value.upper is None or item.lower is None or value.upper >= item.lower:
                    return True
        return False
    return True


@dataclass(frozen=True)
class ProofNode:
    expression: str
    state: frozenset[str]
    reason: str
    children: tuple["ProofNode", ...] = ()


@dataclass(frozen=True)
class SkipInfo:
    partition: Partition
    state: frozenset[str]
    proof: ProofNode


@dataclass(frozen=True)
class PruningPlan:
    selected: tuple[Partition, ...]
    skipped: tuple[SkipInfo, ...]
    proofs: Mapping[str, ProofNode]


def analyze_partition(
    predicate: Any,
    partition: Partition,
    params: Mapping[str, Any] | None = None,
) -> ProofNode:
    params = params or {}
    if has_unbound(predicate, params):
        return ProofNode(
            predicate_text(predicate),
            frozenset({T, F, U}),
            "存在未绑定参数，按保守策略保留",
        )
    if isinstance(predicate, Cmp):
        left_kind = column_kind(predicate.left, partition)
        right_kind = column_kind(predicate.right, partition)
        left = expression_domain(predicate.left, partition, params, right_kind)
        right = expression_domain(predicate.right, partition, params, left_kind)
        state = abstract_compare(predicate.op, left, right)
        return ProofNode(
            predicate_text(predicate),
            state,
            f"左域={domain_text(left)}，右域={domain_text(right)}，比较可能值为{state_text(state)}",
        )
    if isinstance(predicate, InList):
        value_kind = column_kind(predicate.value, partition)
        value = expression_domain(predicate.value, partition, params)
        items = tuple(
            expression_domain(item, partition, params, value_kind)
            for item in predicate.items
        )
        state = abstract_in(value, items, predicate.negated)
        return ProofNode(
            predicate_text(predicate),
            state,
            f"值域={domain_text(value)}，列表域=({', '.join(domain_text(item) for item in items)})，可能值为{state_text(state)}",
        )
    if isinstance(predicate, IsNull):
        domain = expression_domain(predicate.value, partition, params)
        if domain.kind == "null":
            state = frozenset({F if predicate.negated else T})
        elif domain.nullable:
            state = frozenset({T, F})
        else:
            state = frozenset({T if predicate.negated else F})
        return ProofNode(
            predicate_text(predicate),
            state,
            f"值域={domain_text(domain)}，IS NULL三值逻辑判定为{state_text(state)}",
        )
    if isinstance(predicate, And):
        children = tuple(analyze_partition(child, partition, params) for child in predicate.children)
        state = frozenset({T})
        for child in children:
            state = and_state(state, child.state)
        return ProofNode(
            predicate_text(predicate),
            state,
            f"AND按三支真值表合取为{state_text(state)}",
            children,
        )
    if isinstance(predicate, Or):
        children = tuple(analyze_partition(child, partition, params) for child in predicate.children)
        state = frozenset({F})
        for child in children:
            state = or_state(state, child.state)
        return ProofNode(
            predicate_text(predicate),
            state,
            f"OR在分区内合取所有分支，整体为{state_text(state)}",
            children,
        )
    if isinstance(predicate, Not):
        child = analyze_partition(predicate.child, partition, params)
        state = negate_state(child.state)
        return ProofNode(
            predicate_text(predicate),
            state,
            f"NOT交换T/F并保留U，结果为{state_text(state)}",
            (child,),
        )
    raise TypeError(f"unsupported predicate: {type(predicate).__name__}")


def prune(
    predicate: Any,
    scheme: PartitionScheme,
    params: Mapping[str, Any] | None = None,
) -> PruningPlan:
    proofs = {}
    selected = []
    skipped = []
    for partition in scheme.partitions:
        proof = analyze_partition(predicate, partition, params)
        proofs[partition.id] = proof
        if T in proof.state:
            selected.append(partition)
        else:
            skipped.append(SkipInfo(partition, proof.state, proof))
    return PruningPlan(tuple(selected), tuple(skipped), proofs)


def state_text(state: frozenset[str]) -> str:
    names = {T: "TRUE", F: "FALSE", U: "UNKNOWN"}
    return "{" + ",".join(names[item] for item in (T, F, U) if item in state) + "}"


def domain_text(domain: Domain) -> str:
    if isinstance(domain, IntervalDomain):
        lower = "-∞" if domain.lower is None else domain.lower
        upper = "+∞" if domain.upper is None else domain.upper
        text = f"INT[{lower},{upper}]"
    elif isinstance(domain, SetDomain):
        text = "ENUM{" + ",".join(sorted(map(str, domain.values_))) + "}"
    elif isinstance(domain, NullDomain):
        return "NULL"
    else:
        text = "UNKNOWN"
    if domain.nullable:
        text += "∪NULL"
    return text


def expr_text(expr: Any) -> str:
    if isinstance(expr, Col):
        return expr.name
    if isinstance(expr, Param):
        return f":{expr.name}"
    if isinstance(expr, Lit):
        return "NULL" if expr.value is NULL else repr(expr.value)
    raise TypeError(type(expr).__name__)


def predicate_text(predicate: Any) -> str:
    if isinstance(predicate, Cmp):
        return f"{expr_text(predicate.left)} {predicate.op} {expr_text(predicate.right)}"
    if isinstance(predicate, InList):
        keyword = "NOT IN" if predicate.negated else "IN"
        return f"{expr_text(predicate.value)} {keyword} ({', '.join(expr_text(item) for item in predicate.items)})"
    if isinstance(predicate, IsNull):
        keyword = "IS NOT NULL" if predicate.negated else "IS NULL"
        return f"{expr_text(predicate.value)} {keyword}"
    if isinstance(predicate, And):
        return "(" + " AND ".join(predicate_text(child) for child in predicate.children) + ")"
    if isinstance(predicate, Or):
        return "(" + " OR ".join(predicate_text(child) for child in predicate.children) + ")"
    if isinstance(predicate, Not):
        return f"NOT ({predicate_text(predicate.child)})"
    return repr(predicate)


def render_skip_proof(skip: SkipInfo, indent: str = "") -> str:
    lines = [
        f"{indent}跳过分区 {skip.partition.id}：根谓词不可能为TRUE，可能值={state_text(skip.state)}",
        f"{indent}证明：{skip.proof.reason}",
    ]
    for child in skip.proof.children:
        lines.extend(render_proof_node(child, indent + "  "))
    return "\n".join(lines)



def render_proof_node(node: ProofNode, indent: str) -> list[str]:
    lines = [
        f"{indent}- {node.expression} -> {state_text(node.state)}：{node.reason}",
    ]
    for child in node.children:
        lines.extend(render_proof_node(child, indent + "  "))
    return lines


class RelationTable:
    def __init__(self, name: str, scheme: PartitionScheme):
        self.name = name
        self.scheme = scheme
        self.rows: list[dict[str, Any]] = []

    def insert(self, row: Mapping[str, Any]) -> None:
        for column in self.scheme.columns:
            if column.name not in row:
                raise ValueError(f"missing partition column {column.name!r}")
            value = row[column.name]
            if value is not None and column.kind == "integer" and not isinstance(value, int):
                raise TypeError(f"column {column.name!r} expects {column.kind}")
        self.scheme.route(row)
        self.rows.append(dict(row))

    def partition_rows(self) -> Mapping[str, list[dict[str, Any]]]:
        grouped: dict[str, list[dict[str, Any]]] = {
            partition.id: [] for partition in self.scheme.partitions
        }
        for row in self.rows:
            grouped[self.scheme.route(row).id].append(row)
        return grouped


def select_rows(
    table: RelationTable,
    predicate: Any,
    params: Mapping[str, Any] | None = None,
    columns: Sequence[str] | None = None,
) -> list[tuple[Any, ...]]:
    plan = prune(predicate, table.scheme, params)
    selected_ids = {partition.id for partition in plan.selected}
    result = []
    for row in table.rows:
        if table.scheme.route(row).id not in selected_ids:
            continue
        if concrete_eval(predicate, row, params) == T:
            output_columns = tuple(columns) if columns is not None else tuple(row)
            result.append(tuple(row[column] for column in output_columns))
    return result
