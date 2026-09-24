"""Incremental partial evaluator / simplifier over the expression DAG.

Key properties:

* Partial binding: bound variables fold to exact rationals, unbound nodes are
  preserved symbolically.
* Domain preservation: a rewrite is applied only when its domain condition is
  discharged (e.g. ``0 * x -> 0`` requires ``x`` total; ``0 * (1/0)`` is NOT
  0).  Every applied rewrite emits a :class:`Certificate` stating the rule,
  the redex, the contractum and the applicable domain.
* Incrementality: ``set``/unbinding dirties only the affected subgraph via a
  maintained dependency graph; stale constant conclusions are retracted
  (journaled); when a ``cond`` folds to one branch, the dependency on the
  unused branch is removed and its now-unreferenced state is garbage
  collected.
* Determinism: the result is a pure function of (source DAG, bindings), so
  binding order cannot affect the final canonical result.
"""

from __future__ import annotations

from collections import Counter, defaultdict
from dataclasses import dataclass
from fractions import Fraction

from dag import Node, is_total, same_expr, show, show_redex, why_total


@dataclass(frozen=True)
class Certificate:
    """Proof artifact for one applied transformation step."""
    seq: int
    rule: str
    before: str
    after: str
    requires: str   # domain condition under which the rule is valid
    holds: str      # why the condition is satisfied at this redex

    def __str__(self):
        return (f"#{self.seq:03d} [{self.rule}] {self.before}  ~~>  {self.after}"
                f"   (requires: {self.requires}; holds: {self.holds})")


@dataclass(frozen=True)
class Event:
    """Journal entry: 'bind' | 'retract' | 'gc' | 'note'."""
    kind: str
    detail: str

    def __str__(self):
        return f"[{self.kind}] {self.detail}"


class Engine:
    def __init__(self, factory, root: Node):
        self.f = factory
        self.root = root
        self.bindings: dict[str, Fraction] = {}
        self.cache: dict[int, Node] = {}            # src id -> simplified node
        self.deps: dict[int, set[int]] = {}         # src id -> src ids it reads
        self.parents: dict[int, set[int]] = defaultdict(set)
        self.refcount: Counter = Counter()
        self.dirty: set[int] = set()
        self.certificates: list[Certificate] = []
        self.journal: list[Event] = []
        self._total_memo: dict[int, bool] = {}
        self.refcount[root.id] = 1                  # root is always live

    # ------------------------------------------------------------ public API

    def result(self) -> Node:
        """Current simplified form of the root (recomputes what is dirty)."""
        return self._get(self.root)

    def set(self, name: str, value) -> None:
        """Bind ``name`` to a rational (or unbind with ``None``), then
        incrementally retract/recompute affected conclusions."""
        old = self.bindings.get(name)
        if value is None:
            if name not in self.bindings:
                return
            del self.bindings[name]
        else:
            value = Fraction(value)
            if old == value:
                return
            self.bindings[name] = value
        self.journal.append(Event("bind", f"{name}: {old} -> {value}"))
        seeds = [v.id for v in self.f.vars_by_name.get(name, ())]
        self._invalidate(seeds)
        self.result()

    def live(self) -> list[int]:
        """Source node ids the current simplified result still depends on."""
        return sorted(self.cache)

    # ------------------------------------------------------- invalidation

    def _invalidate(self, seeds) -> None:
        stack = list(seeds)
        while stack:
            nid = stack.pop()
            if nid in self.dirty:
                continue
            self.dirty.add(nid)
            cached = self.cache.get(nid)
            if cached is not None and cached.op == "const":
                self.journal.append(Event(
                    "retract",
                    f"node {nid}: retract constant conclusion {cached.value}"))
            stack.extend(self.parents.get(nid, ()))

    # ------------------------------------------------------- recomputation

    def _get(self, src: Node) -> Node:
        if src.id in self.dirty or src.id not in self.cache:
            return self._compute(src)
        return self.cache[src.id]

    def _compute(self, src: Node) -> Node:
        op = src.op
        if op in ("const", "undef"):
            out, deps = src, ()
        elif op == "var":
            if src.name in self.bindings:
                v = self.bindings[src.name]
                out = self.f.const(v, origin=src.origin)
                self._cert("bind", show(src), out, "⊤",
                           f"{src.name} ↦ {v} (current binding)")
            else:
                out = src                      # unbound: keep symbolic
            deps = ()
        elif op == "cond":
            out, deps = self._compute_cond(src)
        else:
            cs = tuple(self._get(a) for a in src.args)
            out = self._reduce_op(src, cs)
            deps = tuple(a.id for a in src.args)
        self._install(src, out, set(deps))
        return out

    def _compute_cond(self, src: Node):
        c_src, t_src, e_src = src.args
        all_deps = {c_src.id, t_src.id, e_src.id}
        c = self._get(c_src)
        redex = f"(if {show(c)} {show(t_src)} {show(e_src)})"
        if c.op == "undef":
            self._cert("cond-undef", redex, c, "⊤",
                       "condition evaluated first; its ⊥ is the first error")
            return c, {c_src.id}
        if c.op == "const":
            taken_src = t_src if c.value != 0 else e_src
            dropped_src = e_src if c.value != 0 else t_src
            taken = self._get(taken_src)
            self._cert("cond-fold", redex, taken, "⊤",
                       f"condition is constant {c.value}; keep "
                       f"{'then' if c.value != 0 else 'else'}-branch, release "
                       f"dependency on node {dropped_src.id}")
            return taken, {c_src.id, taken_src.id}
        t = self._get(t_src)
        e = self._get(e_src)
        if (t.op == "undef" and e.op == "undef" and t.origin == e.origin
                and self._total(c)):
            self._cert("cond-both-undef", redex, t, "D(c)",
                       f"both branches ⊥@{t.origin}; condition total "
                       f"({why_total(c)}) so no earlier error")
            return t, all_deps
        if same_expr(t, e) and self._total(c):
            self._cert("cond-same-branch", redex, t, "D(c)",
                       f"branches identical; requires D(condition), holds: "
                       f"{why_total(c)}")
            return t, all_deps
        return self.f.rebuild(src, (c, t, e)), all_deps

    def _reduce_op(self, src: Node, cs) -> Node:
        f = self.f
        op = src.op
        redex = lambda: show_redex(src, cs)  # noqa: E731

        if op == "neg":
            (a,) = cs
            if a.op == "undef":
                self._cert("neg-undef", redex(), a, "⊤",
                           "⊥ propagates through negation")
                return a
            if a.op == "const":
                out = f.const(-a.value, origin=src.origin)
                self._cert("neg-const", redex(), out, "⊤",
                           "exact rational negation")
                return out
            if a.op == "neg":
                out = a.args[0]
                self._cert("neg-neg", redex(), out, "D(x)",
                           "D(-(-x)) = D(x); double negation preserves domain")
                return out
            return f.rebuild(src, cs)

        a, b = cs

        # --- ⊥ propagation, honouring left-to-right evaluation order -------
        if a.op == "undef":
            self._cert(f"{op}-undef-left", redex(), a, "⊤",
                       "left operand evaluated first; its ⊥ is the first error")
            return a
        if b.op == "undef" and self._total(a):
            self._cert(f"{op}-undef-right", redex(), b, "D(a)",
                       f"left operand total ({why_total(a)}), so the right "
                       "operand's ⊥ is the first error")
            return b

        # --- constant folding (exact rational arithmetic) ------------------
        if a.op == "const" and b.op == "const":
            if op == "div" and b.value == 0:
                out = f.undef(src.origin)
                self._cert("div-zero-const", redex(), out, "⊤",
                           "divisor is literal 0: domain is empty")
                return out
            val = {"add": a.value + b.value, "sub": a.value - b.value,
                   "mul": a.value * b.value}.get(op)
            if val is None:
                val = a.value / b.value
            out = f.const(val, origin=src.origin)
            self._cert(f"{op}-const", redex(), out, "⊤",
                       "exact rational arithmetic (Fraction)")
            return out

        # --- operator-specific algebraic rules -----------------------------
        if op == "add":
            if self._is_zero(a):
                self._cert("add-zero-left", redex(), b, "D(b)",
                           "0 + x = x on D(b); domains coincide")
                return b
            if self._is_zero(b):
                self._cert("add-zero-right", redex(), a, "D(a)",
                           "x + 0 = x on D(a); domains coincide")
                return a
        elif op == "sub":
            if self._is_zero(b):
                self._cert("sub-zero-right", redex(), a, "D(a)",
                           "x - 0 = x on D(a); domains coincide")
                return a
            if self._is_zero(a):
                out = f.neg(b, origin=src.origin)
                self._cert("sub-zero-left", redex(), out, "D(b)",
                           "0 - x = -x on D(b); domains coincide")
                return out
            if same_expr(a, b):
                if self._total(a):
                    out = f.const(0, origin=src.origin)
                    self._cert("sub-self", redex(), out, "D(a)",
                               f"x - x = 0 requires D(x); holds: {why_total(a)}")
                    return out
                self.journal.append(Event(
                    "note", f"kept {redex()}: term not provably total; "
                    "x - x = 0 would require D(x)"))
        elif op == "mul":
            if self._is_one(a):
                self._cert("mul-one-left", redex(), b, "D(b)",
                           "1 · x = x on D(b); domains coincide")
                return b
            if self._is_one(b):
                self._cert("mul-one-right", redex(), a, "D(a)",
                           "x · 1 = x on D(a); domains coincide")
                return a
            if self._is_zero(a):
                if self._total(b):
                    out = f.const(0, origin=src.origin)
                    self._cert("mul-zero-left", redex(), out, "D(b)",
                               f"0 · x = 0 requires D(b); holds: {why_total(b)}")
                    return out
                self.journal.append(Event(
                    "note", f"kept {redex()}: right operand not provably "
                    "total; 0 · undefined ≠ 0"))
            elif self._is_zero(b):
                if self._total(a):
                    out = f.const(0, origin=src.origin)
                    self._cert("mul-zero-right", redex(), out, "D(a)",
                               f"x · 0 = 0 requires D(a); holds: {why_total(a)}")
                    return out
                self.journal.append(Event(
                    "note", f"kept {redex()}: left operand not provably "
                    "total; 0 · undefined ≠ 0"))
        elif op == "div":
            if self._is_zero(b):
                if self._total(a):
                    out = f.undef(src.origin)
                    self._cert("div-zero", redex(), out, "D(a)",
                               "divisor literal 0: domain empty once numerator "
                               f"is defined; numerator total ({why_total(a)})")
                    return out
                self.journal.append(Event(
                    "note", f"kept {redex()}: numerator not provably total; "
                    "its own error would precede the division"))
            if self._is_one(b):
                self._cert("div-one", redex(), a, "D(a)",
                           "x / 1 = x on D(a); domains coincide")
                return a

        # --- canonical operand order for commutative ops -------------------
        # Reordering is error-order safe only if at least one side is total
        # (a total side can never raise, so the first error is unchanged).
        if op in ("add", "mul") and a.id > b.id and (
                self._total(a) or self._total(b)):
            out = f.rebuild(src, (b, a))
            self._cert("canon-commute", redex(), out, "D(a) ∧ D(b)",
                       "commutative canonicalisation; ≥1 side total, so "
                       "evaluation/error order is preserved")
            return out
        return f.rebuild(src, cs)

    # ------------------------------------------------------- bookkeeping

    def _install(self, src: Node, out: Node, new_deps: set) -> None:
        old = self.deps.get(src.id, set())
        for d in new_deps - old:
            self.parents[d].add(src.id)
            self.refcount[d] += 1
        for d in old - new_deps:
            self.parents[d].discard(src.id)
            self.refcount[d] -= 1
            if self.refcount[d] <= 0:
                self._gc(d)
        self.deps[src.id] = set(new_deps)
        self.cache[src.id] = out
        self.dirty.discard(src.id)

    def _gc(self, nid: int) -> None:
        if nid == self.root.id:
            return
        if nid not in self.cache and nid not in self.deps:
            return
        self.journal.append(Event(
            "gc", f"node {nid}: dependency no longer used, state released"))
        self.cache.pop(nid, None)
        self.dirty.discard(nid)
        for d in self.deps.pop(nid, set()):
            self.parents[d].discard(nid)
            self.refcount[d] -= 1
            if self.refcount[d] <= 0:
                self._gc(d)

    # ------------------------------------------------------------- helpers

    def _total(self, n: Node) -> bool:
        return is_total(n, self._total_memo)

    @staticmethod
    def _is_zero(n: Node) -> bool:
        return n.op == "const" and n.value == 0

    @staticmethod
    def _is_one(n: Node) -> bool:
        return n.op == "const" and n.value == 1

    def _cert(self, rule, before, after, requires, holds) -> None:
        if isinstance(before, Node):
            before = show(before)
        self.certificates.append(Certificate(
            len(self.certificates) + 1, rule, before, show(after),
            requires, holds))
