"""Mini relational algebra with explicit NULL and bag (multiset) semantics.

Pure standard library. No SQL engine is involved: plans are trees of
operators evaluated directly in Python.

Semantics (SQL-like, made explicit):
- NULL is a singleton sentinel. Any comparison involving NULL yields
  UNKNOWN (three-valued logic). A filter keeps only rows whose predicate
  evaluates to TRUE.
- Join conditions likewise match only on TRUE; NULL never equals NULL.
- LEFT OUTER JOIN pads unmatched left rows with NULLs on the right.
- All operators preserve duplicates (bag semantics). Results are
  multisets: row order is irrelevant, multiplicities matter.
- Column names must be disjoint across join inputs (no self-join without
  renaming); this keeps environments unambiguous.
"""
from __future__ import annotations

from dataclasses import dataclass


class _Null:
    """Singleton NULL value. Distinct from every concrete value."""

    _instance = None

    def __new__(cls):
        if cls._instance is None:
            cls._instance = super().__new__(cls)
        return cls._instance

    def __repr__(self):
        return "NULL"

    def __bool__(self):
        raise TypeError("NULL has no truth value")

    def __copy__(self):
        return self

    def __deepcopy__(self, memo):
        return self

    def __reduce__(self):
        return (_Null, ())


NULL = _Null()

# Three-valued logic: True / False / None (UNKNOWN).
UNKNOWN = None


def tv_not(v):
    return UNKNOWN if v is UNKNOWN else (not v)


def tv_and(a, b):
    if a is False or b is False:
        return False
    if a is UNKNOWN or b is UNKNOWN:
        return UNKNOWN
    return True


def tv_or(a, b):
    if a is True or b is True:
        return True
    if a is UNKNOWN or b is UNKNOWN:
        return UNKNOWN
    return False


# ---------------------------------------------------------------- expressions

class Expr:
    """Boolean/scalar expression evaluated against a row environment."""

    def eval(self, env):
        raise NotImplementedError


@dataclass(frozen=True)
class Lit(Expr):
    value: object

    def eval(self, env):
        return self.value


@dataclass(frozen=True)
class Col(Expr):
    name: str

    def eval(self, env):
        return env[self.name]


_OPS = {
    "=": lambda a, b: a == b,
    "!=": lambda a, b: a != b,
    "<": lambda a, b: a < b,
    "<=": lambda a, b: a <= b,
    ">": lambda a, b: a > b,
    ">=": lambda a, b: a >= b,
}


@dataclass(frozen=True)
class Cmp(Expr):
    """Comparison; yields UNKNOWN if either side is NULL."""

    op: str
    left: Expr
    right: Expr

    def eval(self, env):
        lval = self.left.eval(env)
        rval = self.right.eval(env)
        if lval is NULL or rval is NULL:
            return UNKNOWN
        return _OPS[self.op](lval, rval)


@dataclass(frozen=True)
class And(Expr):
    left: Expr
    right: Expr

    def eval(self, env):
        return tv_and(self.left.eval(env), self.right.eval(env))


@dataclass(frozen=True)
class Or(Expr):
    left: Expr
    right: Expr

    def eval(self, env):
        return tv_or(self.left.eval(env), self.right.eval(env))


@dataclass(frozen=True)
class Not(Expr):
    inner: Expr

    def eval(self, env):
        return tv_not(self.inner.eval(env))


@dataclass(frozen=True)
class IsNull(Expr):
    inner: Expr

    def eval(self, env):
        return self.inner.eval(env) is NULL


# ----------------------------------------------------------------- operators

class Op:
    """A query plan node."""


@dataclass(frozen=True)
class Scan(Op):
    name: str


@dataclass(frozen=True)
class Select(Op):
    pred: Expr
    child: Op


@dataclass(frozen=True)
class Project(Op):
    cols: tuple
    child: Op


@dataclass(frozen=True)
class Join(Op):
    """Inner theta-join."""

    pred: Expr
    left: Op
    right: Op


@dataclass(frozen=True)
class LeftJoin(Op):
    """Left outer join; unmatched left rows are padded with NULLs."""

    pred: Expr
    left: Op
    right: Op


def _env(schema, row):
    return dict(zip(schema, row))


def evaluate(plan, catalog, db):
    """Evaluate a plan. Returns (schema, rows) where rows is a list of
    tuples (a bag: duplicates kept, order not semantically relevant).

    catalog: {table_name: [col, ...]}, db: {table_name: [tuple, ...]}.
    """
    if isinstance(plan, Scan):
        return list(catalog[plan.name]), [tuple(r) for r in db[plan.name]]

    if isinstance(plan, Select):
        schema, rows = evaluate(plan.child, catalog, db)
        kept = [r for r in rows if plan.pred.eval(_env(schema, r)) is True]
        return schema, kept

    if isinstance(plan, Project):
        schema, rows = evaluate(plan.child, catalog, db)
        idx = [schema.index(c) for c in plan.cols]
        return list(plan.cols), [tuple(r[i] for i in idx) for r in rows]

    if isinstance(plan, (Join, LeftJoin)):
        lschema, lrows = evaluate(plan.left, catalog, db)
        rschema, rrows = evaluate(plan.right, catalog, db)
        overlap = set(lschema) & set(rschema)
        if overlap:
            raise ValueError(f"ambiguous columns in join: {sorted(overlap)}")
        schema = lschema + rschema
        out = []
        for lrow in lrows:
            matched = False
            for rrow in rrows:
                env = _env(schema, lrow + rrow)
                if plan.pred.eval(env) is True:
                    out.append(lrow + rrow)
                    matched = True
            if isinstance(plan, LeftJoin) and not matched:
                out.append(lrow + (NULL,) * len(rschema))
        return schema, out

    raise TypeError(f"unknown plan node: {plan!r}")
