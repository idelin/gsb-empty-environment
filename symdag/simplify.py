"""Partial evaluation and simplification over the DAG, with certificates.

Semantics preserved (domain preservation): the simplified expression is
defined on exactly the same set of assignments as the original and agrees
with it there.  In particular 0 * (possibly-undefined) is NOT rewritten
to 0 unless the other factor is provably total; an unconditionally
undefined operand propagates an `undef` node carrying the original error
position instead.

Incrementality: a Session caches one Entry per node.  Each entry records
``reads`` (bound variables whose values were consulted) and ``frees``
(variables still free in the simplified result).  Updating a binding
invalidates exactly the entries mentioning that variable, which retracts
stale constant conclusions; entries for unrelated subtrees survive.  A
conditional folds to one branch when its condition becomes constant, and
the untaken branch's dependencies are dropped from the parent entry.

Every applied rewrite appends a Certificate stating the rule, the
before/after forms and the domain on which the step is valid.  Rules that
were considered but rejected on domain grounds are recorded in
``session.suppressed``.
"""
from __future__ import annotations

from collections import Counter
from dataclasses import dataclass, field
from fractions import Fraction

from .nodes import Factory, Node, to_str


@dataclass(frozen=True)
class Certificate:
    """One simplification step: rule, before/after, and applicable domain."""

    rule: str
    before: str
    after: str
    domain: str


@dataclass
class Entry:
    """Cached partial-evaluation result for one node."""

    result: Node
    reads: frozenset   # bound variables consulted to reach this result
    frees: frozenset   # variables still free in the result


class Session:
    """Incremental simplifier over a set of variable bindings."""

    def __init__(self, factory: Factory | None = None):
        self.f = factory or Factory()
        self.bindings: dict[str, Fraction] = {}
        self.cache: dict[Node, Entry] = {}
        self.certificates: list[Certificate] = []
        self.suppressed: list[Certificate] = []
        self.stats: Counter = Counter()
        self._roots: set[Node] = set()

    # ------------------------------------------------------------------ #
    # binding updates with incremental invalidation
    # ------------------------------------------------------------------ #
    def set(self, name, value) -> None:
        value = Fraction(value)
        if self.bindings.get(name) == value:
            return
        self.bindings[name] = value
        self._invalidate(name)

    def unset(self, name) -> None:
        if name in self.bindings:
            del self.bindings[name]
            self._invalidate(name)

    def _invalidate(self, name) -> None:
        """Retract every conclusion that depends on ``name``."""
        doomed = [n for n, e in self.cache.items()
                  if name in e.reads or name in e.frees]
        for n in doomed:
            entry = self.cache.pop(n)
            self.stats["invalidated"] += 1
            if entry.result.op == "const":
                self.stats["retracted_constants"] += 1
        self._gc()

    def _gc(self) -> None:
        """Drop cache entries unreachable from registered roots."""
        live: set[Node] = set()
        stack = list(self._roots)
        while stack:
            node = stack.pop()
            if node in live:
                continue
            live.add(node)
            stack.extend(node.args)
            entry = self.cache.get(node)
            if entry is not None and entry.result is not node:
                stack.append(entry.result)
        doomed = [n for n in self.cache if n not in live]
        for n in doomed:
            del self.cache[n]
        self.stats["pruned"] += len(doomed)

    # ------------------------------------------------------------------ #
    # public simplification entry point
    # ------------------------------------------------------------------ #
    def simplify(self, node, *, root=True):
        if root:
            self._roots.add(node)
        return self._pe(node).result

    def _cert(self, rule, before, after, domain):
        self.certificates.append(
            Certificate(rule, to_str(before), to_str(after), domain))

    def _suppress(self, rule, before, domain):
        self.suppressed.append(
            Certificate(rule, to_str(before), to_str(before), domain))

    # ------------------------------------------------------------------ #
    # partial evaluation
    # ------------------------------------------------------------------ #
    def _pe(self, node) -> Entry:
        cached = self.cache.get(node)
        if cached is not None:
            self.stats["hits"] += 1
            return cached
        self.stats["computed"] += 1
        op = node.op
        if op in ("const", "undef"):
            entry = Entry(node, frozenset(), frozenset())
        elif op == "var":
            name = node.name
            if name in self.bindings:
                result = self.f.const(self.bindings[name], origin=node.origin)
                self._cert(
                    "bind", node, result,
                    f"substitute {name} := {self.bindings[name]}; "
                    "exact on the whole domain")
                entry = Entry(result, frozenset({name}), frozenset())
            else:
                entry = Entry(node, frozenset(), frozenset({name}))
        elif op == "if":
            entry = self._pe_if(node)
        else:
            parts = [self._pe(a) for a in node.args]
            kids = [p.result for p in parts]
            reads = frozenset().union(*(p.reads for p in parts))
            result = self._combine(node, kids)
            entry = Entry(result, reads, self.f.free_vars(result))
        self.cache[node] = entry
        return entry

    def _pe_if(self, node) -> Entry:
        f = self.f
        parts = [self._pe(a) for a in node.args]
        cond, then, other = (p.result for p in parts)
        if cond.op == "undef":
            self._cert(
                "propagate-undefined", node, cond,
                "the condition is unconditionally undefined, so the whole "
                "conditional is undefined at the same source position")
            return Entry(cond, parts[0].reads, f.free_vars(cond))
        if cond.op == "const":
            taken = parts[1] if cond.value != 0 else parts[2]
            self._cert(
                "if-const", f.if_(cond, then, other, origin=node.origin),
                taken.result,
                "condition is a constant, so exactly one branch is "
                "reachable; the dead branch and its dependencies are "
                "dropped without changing the domain")
            reads = parts[0].reads | taken.reads
            return Entry(taken.result, reads, f.free_vars(taken.result))
        if then is other and f.is_total(cond):
            self._cert(
                "if-same", f.if_(cond, then, other, origin=node.origin), then,
                "both branches coincide and the condition is total, so the "
                "conditional equals either branch on its whole domain")
            reads = parts[0].reads | parts[1].reads
            return Entry(then, reads, f.free_vars(then))
        if (cond, then, other) == node.args:
            rebuilt = node
        else:
            rebuilt = f.if_(cond, then, other, origin=node.origin)
        reads = parts[0].reads | parts[1].reads | parts[2].reads
        return Entry(rebuilt, reads, f.free_vars(rebuilt))

    # ------------------------------------------------------------------ #
    # rewrite rules (children already simplified)
    # ------------------------------------------------------------------ #
    def _rebuild(self, node, kids):
        if tuple(kids) == node.args:
            return node
        ctor = {
            "add": self.f.add, "sub": self.f.sub, "mul": self.f.mul,
            "div": self.f.div, "neg": self.f.neg,
            "lt": self.f.lt, "eq": self.f.eq,
        }[node.op]
        return ctor(*kids, origin=node.origin)

    @staticmethod
    def _is_const(node, value) -> bool:
        return node.op == "const" and node.value == value

    def _combine(self, node, kids):
        f = self.f
        op = node.op
        # 1. undefined propagation: an unconditionally undefined operand
        #    makes the whole operation undefined at the same position.
        #    Note this is NOT the same as folding 0 * x to 0.
        for kid in kids:
            if kid.op == "undef":
                self._cert(
                    "propagate-undefined", self._rebuild(node, kids), kid,
                    "an operand is unconditionally undefined, so the "
                    "operation is undefined at the same source position; "
                    "in particular 0 * undefined stays undefined, never 0")
                return kid
        # 2. exact constant folding
        if all(k.op == "const" for k in kids):
            return self._fold(node, kids)
        before = self._rebuild(node, kids)
        # 3. algebraic identities, each guarded by a domain check
        if op == "add":
            if self._is_const(kids[0], 0):
                self._cert("add-zero", before, kids[1],
                           "0 + x = x wherever x is defined; domain unchanged")
                return kids[1]
            if self._is_const(kids[1], 0):
                self._cert("add-zero", before, kids[0],
                           "x + 0 = x wherever x is defined; domain unchanged")
                return kids[0]
        elif op == "sub":
            if self._is_const(kids[1], 0):
                self._cert("sub-zero", before, kids[0],
                           "x - 0 = x wherever x is defined; domain unchanged")
                return kids[0]
            if kids[0] is kids[1]:
                if f.is_total(kids[0]):
                    self._cert("sub-self", before, f.const(0, origin=node.origin),
                               "x - x = 0 wherever x is defined; x is total, "
                               "so the constant 0 does not enlarge the domain")
                    return f.const(0, origin=node.origin)
                self._suppress(
                    "sub-self", before,
                    "NOT applied: x may be undefined for some inputs, so "
                    "replacing x - x by the everywhere-defined 0 would "
                    "enlarge the domain")
        elif op == "mul":
            if self._is_const(kids[0], 1):
                self._cert("mul-one", before, kids[1],
                           "1 * x = x wherever x is defined; domain unchanged")
                return kids[1]
            if self._is_const(kids[1], 1):
                self._cert("mul-one", before, kids[0],
                           "x * 1 = x wherever x is defined; domain unchanged")
                return kids[0]
            for side, other in ((0, kids[1]), (1, kids[0])):
                if self._is_const(kids[side], 0):
                    if f.is_total(other):
                        self._cert(
                            "mul-zero", before, f.const(0, origin=node.origin),
                            "0 * x = 0 wherever x is defined; x is provably "
                            "total, so the constant 0 does not enlarge the "
                            "domain")
                        return f.const(0, origin=node.origin)
                    self._suppress(
                        "mul-zero", before,
                        "NOT applied: the other factor may be undefined "
                        "(e.g. 1/0); the original is undefined there while "
                        "0 is defined, so folding would enlarge the domain")
        elif op == "div":
            if self._is_const(kids[1], 1):
                self._cert("div-one", before, kids[0],
                           "x / 1 = x wherever x is defined; domain unchanged")
                return kids[0]
        elif op == "neg":
            if kids[0].op == "neg":
                self._cert("neg-neg", before, kids[0].args[0],
                           "-(-x) = x wherever x is defined; domain unchanged")
                return kids[0].args[0]
        return before

    def _fold(self, node, kids):
        """Exact rational constant folding (fractions.Fraction)."""
        f = self.f
        op = node.op
        vals = [k.value for k in kids]
        if op == "div" and vals[1] == 0:
            result = f.undef(origin=node.origin, reason="division by zero")
            self._cert(
                "fold-undefined", node, result,
                "exact rational division by zero is undefined; the original "
                "expression lies outside its domain here, and the failure "
                "is recorded at its source position")
            return result
        if op == "add":
            value = vals[0] + vals[1]
        elif op == "sub":
            value = vals[0] - vals[1]
        elif op == "mul":
            value = vals[0] * vals[1]
        elif op == "div":
            value = vals[0] / vals[1]
        elif op == "neg":
            value = -vals[0]
        elif op == "lt":
            value = Fraction(1 if vals[0] < vals[1] else 0)
        elif op == "eq":
            value = Fraction(1 if vals[0] == vals[1] else 0)
        else:  # pragma: no cover - defensive
            raise ValueError(f"cannot fold op {op!r}")
        result = f.const(value, origin=node.origin)
        self._cert(
            "const-fold", node, result,
            "exact rational arithmetic on fractions.Fraction; no rounding, "
            "valid wherever the operands are defined")
        return result


def simplify_once(node, bindings=None, factory=None):
    """One-shot partial evaluation.  Returns (result, certificates, session)."""
    session = Session(factory)
    for name, value in (bindings or {}).items():
        session.bindings[name] = Fraction(value)
    result = session.simplify(node)
    return result, session.certificates, session
