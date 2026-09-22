"""Typed expression trees: serialization, binding, and safe evaluation.

Expressions are never evaluated from strings. An expression is a tagged object
tree deserialized through an explicit allow-list (``EXPR_REGISTRY``); binding
resolves column references and checks types *before* any row is processed.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any

from plan_dag.datatypes import (
    BOOLEAN,
    DOUBLE,
    INTEGER,
    NULL_TYPE,
    is_comparable,
    is_numeric,
    literal_type,
)
from plan_dag.errors import BindingError, InvalidDocumentError, PlanExecutionError
from plan_dag.tags import (
    EXPR_REGISTRY,
    check_type,
    json_scalar,
    require_keys,
    require_object,
    require_tag,
)

CMP_OPS = {"eq", "ne", "lt", "le", "gt", "ge"}
ARITH_OPS = {"add", "sub", "mul", "div"}


class BoundExpr:
    """A bound expression has a known type and can be evaluated per row."""

    type: DataType

    def eval(self, row: list[Any]) -> Any:
        raise NotImplementedError


@dataclass(frozen=True)
class BColumn(BoundExpr):
    index: int
    type: DataType

    def eval(self, row):
        return row[self.index]


@dataclass(frozen=True)
class BLiteral(BoundExpr):
    value: Any
    type: DataType

    def eval(self, row):
        return self.value


@dataclass(frozen=True)
class BCmp(BoundExpr):
    op: str
    left: BoundExpr
    right: BoundExpr
    type: DataType = BOOLEAN

    def eval(self, row):
        lv = self.left.eval(row)
        rv = self.right.eval(row)
        if lv is None or rv is None:
            return None
        if self.op == "eq":
            return lv == rv
        if self.op == "ne":
            return lv != rv
        if self.op == "lt":
            return lv < rv
        if self.op == "le":
            return lv <= rv
        if self.op == "gt":
            return lv > rv
        return lv >= rv


@dataclass(frozen=True)
class BAnd(BoundExpr):
    args: tuple[BoundExpr, ...]
    type: DataType = BOOLEAN

    def eval(self, row):
        saw_null = False
        for a in self.args:
            v = a.eval(row)
            if v is False:
                return False
            if v is None:
                saw_null = True
        return None if saw_null else True


@dataclass(frozen=True)
class BOr(BoundExpr):
    args: tuple[BoundExpr, ...]
    type: DataType = BOOLEAN

    def eval(self, row):
        saw_null = False
        for a in self.args:
            v = a.eval(row)
            if v is True:
                return True
            if v is None:
                saw_null = True
        return None if saw_null else False


@dataclass(frozen=True)
class BNot(BoundExpr):
    arg: BoundExpr
    type: DataType = BOOLEAN

    def eval(self, row):
        v = self.arg.eval(row)
        return None if v is None else not v


@dataclass(frozen=True)
class BArith(BoundExpr):
    op: str
    left: BoundExpr
    right: BoundExpr
    type: DataType

    def eval(self, row):
        lv = self.left.eval(row)
        rv = self.right.eval(row)
        if lv is None or rv is None:
            return None
        if self.op == "add":
            return lv + rv
        if self.op == "sub":
            return lv - rv
        if self.op == "mul":
            return lv * rv
        if rv == 0:
            raise PlanExecutionError("division by zero")
        return lv / rv


@dataclass(frozen=True)
class BIsNull(BoundExpr):
    arg: BoundExpr
    negated: bool
    type: DataType = BOOLEAN

    def eval(self, row):
        is_null = self.arg.eval(row) is None
        return (not is_null) if self.negated else is_null


@dataclass(frozen=True)
class BCoalesce(BoundExpr):
    args: tuple[BoundExpr, ...]
    type: DataType

    def eval(self, row):
        for a in self.args:
            v = a.eval(row)
            if v is not None:
                return v
        return None


class Expr:
    TAG: str = ""

    def to_data(self) -> dict:
        raise NotImplementedError

    @classmethod
    def from_data(cls, data: dict) -> "Expr":
        raise NotImplementedError

    def bind(self, resolver) -> BoundExpr:
        raise NotImplementedError


def _expect_bool_or_null(t: DataType, what: str) -> None:
    if t != BOOLEAN and t != NULL_TYPE:
        raise BindingError(f"{what} requires BOOLEAN, got {t}")


def _numeric_or_null(t: DataType, what: str) -> None:
    if not is_numeric(t) and t != NULL_TYPE:
        raise BindingError(f"{what} requires INTEGER or DOUBLE, got {t}")


@EXPR_REGISTRY.register
@dataclass(frozen=True)
class ColumnRef(Expr):
    TAG = "column_ref"
    name: str

    def to_data(self):
        return {"@type": self.TAG, "name": self.name}

    @classmethod
    def from_data(cls, data):
        check_type(data.get("name"), str, "column_ref.name")
        return cls(data["name"])

    def bind(self, resolver):
        index, col_type = resolver.resolve(self.name)
        return BColumn(index=index, type=col_type)


@EXPR_REGISTRY.register
class Literal(Expr):
    TAG = "literal"

    def __init__(self, value: Any):
        json_scalar(value, "literal.value")
        if isinstance(value, (list, dict)):
            raise InvalidDocumentError("literal.value must be a scalar")
        self.value = value
        self.type = literal_type(value)

    def to_data(self):
        return {"@type": self.TAG, "value": self.value}

    @classmethod
    def from_data(cls, data):
        if "value" not in data:
            raise InvalidDocumentError("literal missing 'value'")
        return cls(data["value"])

    def bind(self, resolver):
        return BLiteral(value=self.value, type=self.type)

    def __eq__(self, other):
        return isinstance(other, Literal) and other.value == self.value


@EXPR_REGISTRY.register
class Cmp(Expr):
    TAG = "cmp"

    def __init__(self, op: str, left: Expr, right: Expr):
        if op not in CMP_OPS:
            raise BindingError(f"unknown comparison {op!r}; expected one of {sorted(CMP_OPS)}")
        self.op = op
        self.left = left
        self.right = right

    def to_data(self):
        return {
            "@type": self.TAG,
            "op": self.op,
            "left": self.left.to_data(),
            "right": self.right.to_data(),
        }

    @classmethod
    def from_data(cls, data):
        op = data.get("op")
        if op not in CMP_OPS:
            raise InvalidDocumentError(f"cmp.op must be one of {sorted(CMP_OPS)}, got {op!r}")
        return cls(op, expr_from_data(data["left"]), expr_from_data(data["right"]))

    def bind(self, resolver):
        lb = self.left.bind(resolver)
        rb = self.right.bind(resolver)
        if not is_comparable(lb.type, rb.type) and NULL_TYPE not in (lb.type, rb.type):
            raise BindingError(
                f"cannot compare {lb.type} with {rb.type} in {self.op!r}"
            )
        return BCmp(op=self.op, left=lb, right=rb)


@EXPR_REGISTRY.register
class And(Expr):
    TAG = "and"

    def __init__(self, *args: Expr):
        if not args:
            raise BindingError("and requires at least one argument")
        self.args = tuple(args)

    def to_data(self):
        return {"@type": self.TAG, "args": [a.to_data() for a in self.args]}

    @classmethod
    def from_data(cls, data):
        args = data.get("args")
        if not isinstance(args, list) or not args:
            raise InvalidDocumentError("and.args must be a non-empty list")
        return cls(*[expr_from_data(a) for a in args])

    def bind(self, resolver):
        bound = tuple(a.bind(resolver) for a in self.args)
        for b in bound:
            _expect_bool_or_null(b.type, "and argument")
        return BAnd(args=bound)


@EXPR_REGISTRY.register
class Or(Expr):
    TAG = "or"

    def __init__(self, *args: Expr):
        if not args:
            raise BindingError("or requires at least one argument")
        self.args = tuple(args)

    def to_data(self):
        return {"@type": self.TAG, "args": [a.to_data() for a in self.args]}

    @classmethod
    def from_data(cls, data):
        args = data.get("args")
        if not isinstance(args, list) or not args:
            raise InvalidDocumentError("or.args must be a non-empty list")
        return cls(*[expr_from_data(a) for a in args])

    def bind(self, resolver):
        bound = tuple(a.bind(resolver) for a in self.args)
        for b in bound:
            _expect_bool_or_null(b.type, "or argument")
        return BOr(args=bound)


@EXPR_REGISTRY.register
class Not(Expr):
    TAG = "not"

    def __init__(self, arg: Expr):
        self.arg = arg

    def to_data(self):
        return {"@type": self.TAG, "arg": self.arg.to_data()}

    @classmethod
    def from_data(cls, data):
        return cls(expr_from_data(data["arg"]))

    def bind(self, resolver):
        b = self.arg.bind(resolver)
        _expect_bool_or_null(b.type, "not argument")
        return BNot(arg=b)


@EXPR_REGISTRY.register
class Arith(Expr):
    TAG = "arith"

    def __init__(self, op: str, left: Expr, right: Expr):
        if op not in ARITH_OPS:
            raise BindingError(f"unknown arithmetic {op!r}; expected one of {sorted(ARITH_OPS)}")
        self.op = op
        self.left = left
        self.right = right

    def to_data(self):
        return {
            "@type": self.TAG,
            "op": self.op,
            "left": self.left.to_data(),
            "right": self.right.to_data(),
        }

    @classmethod
    def from_data(cls, data):
        op = data.get("op")
        if op not in ARITH_OPS:
            raise InvalidDocumentError(f"arith.op must be one of {sorted(ARITH_OPS)}, got {op!r}")
        return cls(op, expr_from_data(data["left"]), expr_from_data(data["right"]))

    def bind(self, resolver):
        lb = self.left.bind(resolver)
        rb = self.right.bind(resolver)
        _numeric_or_null(lb.type, "left operand")
        _numeric_or_null(rb.type, "right operand")
        non_null = [t for t in (lb.type, rb.type) if t != NULL_TYPE]
        if self.op == "div":
            result = DOUBLE
        elif DOUBLE in non_null:
            result = DOUBLE
        elif INTEGER in non_null:
            result = INTEGER
        else:
            result = arithmetic_result(non_null[0]) if non_null else INTEGER
        return BArith(op=self.op, left=lb, right=rb, type=result)


@EXPR_REGISTRY.register
class IsNull(Expr):
    TAG = "is_null"

    def __init__(self, arg: Expr, negated: bool = False):
        self.arg = arg
        self.negated = bool(negated)

    def to_data(self):
        return {"@type": self.TAG, "arg": self.arg.to_data(), "negated": self.negated}

    @classmethod
    def from_data(cls, data):
        negated = data.get("negated", False)
        check_type(negated, bool, "is_null.negated")
        return cls(expr_from_data(data["arg"]), negated=negated)

    def bind(self, resolver):
        return BIsNull(arg=self.arg.bind(resolver), negated=self.negated)


@EXPR_REGISTRY.register
class Coalesce(Expr):
    TAG = "coalesce"

    def __init__(self, *args: Expr):
        if not args:
            raise BindingError("coalesce requires at least one argument")
        self.args = tuple(args)

    def to_data(self):
        return {"@type": self.TAG, "args": [a.to_data() for a in self.args]}

    @classmethod
    def from_data(cls, data):
        args = data.get("args")
        if not isinstance(args, list) or not args:
            raise InvalidDocumentError("coalesce.args must be a non-empty list")
        return cls(*[expr_from_data(a) for a in args])

    def bind(self, resolver):
        bound = tuple(a.bind(resolver) for a in self.args)
        concrete = [b.type for b in bound if b.type != NULL_TYPE]
        if not concrete:
            result = NULL_TYPE
        elif len(set(concrete)) == 1:
            result = concrete[0]
        elif all(is_numeric(t) for t in concrete):
            result = DOUBLE if DOUBLE in concrete else INTEGER
        else:
            raise BindingError(
                f"coalesce arguments have incompatible types {[t.name for t in concrete]}"
            )
        return BCoalesce(args=bound, type=result)


_EXPR_DATA_KEYS = {
    "column_ref": {"@type", "name"},
    "literal": {"@type", "value"},
    "cmp": {"@type", "op", "left", "right"},
    "and": {"@type", "args"},
    "or": {"@type", "args"},
    "not": {"@type", "arg"},
    "arith": {"@type", "op", "left", "right"},
    "is_null": {"@type", "arg", "negated"},
    "coalesce": {"@type", "args"},
}


def expr_from_data(data: object) -> Expr:
    """Deserialize one tagged expression through the expression allow-list."""
    obj = require_object(data, "expression")
    tag, cls = require_tag(obj, EXPR_REGISTRY, "expression")
    require_keys(obj, {"@type"}, _EXPR_DATA_KEYS[tag], f"expression {tag!r}")
    return cls.from_data(obj)
