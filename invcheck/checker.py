"""Symbolic invariant checking on shared BDDs.

For a candidate invariant ``inv`` the checker decides, using only symbolic
set operations on BDDs:

* base case        -- ``init & !inv`` is empty  (initial states satisfy inv)
* inductive step   -- ``inv & T & !inv'`` is empty  (inv is preserved by T)

A non-empty failing set yields a *minimal* counterexample: the
lexicographically smallest satisfying assignment under the chosen
variable order.  The verdict never depends on the variable order; only
the concrete minimal witness may differ across orders.

The checker additionally computes the exact reachable-state set by a
symbolic fixpoint, because *holding on all reachable states is not the
same as being inductive*: an inductive-step counterexample may start
from an unreachable state, in which case the candidate still holds on
every reachable state but must be strengthened to become inductive.

If a node/time budget is exhausted at any point, the affected check is
reported as ``undecided`` -- never silently as passed or failed.
"""

from __future__ import annotations

from dataclasses import dataclass, field

from .bdd import BDDManager, ResourceExceeded
from . import expr as exprmod

NEXT_SUFFIX = "@next"


def next_var(v):
    return v + NEXT_SUFFIX


def is_next(v):
    return v.endswith(NEXT_SUFFIX)


def cur_of(v):
    return v[: -len(NEXT_SUFFIX)]


def make_var_order(variables, style="interleave", permutation=None):
    """Variable order over current and next-state copies.

    ``permutation`` reorders the current-state variables; the next-state
    copies follow the same relative order so that renaming between the
    two copies is always order-preserving.
    """
    perm = list(permutation) if permutation is not None else list(variables)
    if sorted(perm) != sorted(variables):
        raise ValueError("permutation must be a reordering of the model variables")
    if style == "interleave":
        order = []
        for v in perm:
            order.append(v)
            order.append(next_var(v))
        return order
    if style == "block":
        return list(perm) + [next_var(v) for v in perm]
    raise ValueError(f"unknown order style {style!r}")


@dataclass
class CheckResult:
    invariant: str = ""
    verdict: str = "undecided"  # verified | base_case_failed | inductive_failed | undecided
    base_status: str = "undecided"  # pass | fail | empty_init | undecided
    inductive_status: str = "undecided"  # pass | fail | undecided
    base_counterexample: dict | None = None
    inductive_counterexample: tuple | None = None  # (premise state, successor state)
    reachable_checked: bool = False
    reachable_count: int | None = None
    holds_on_reachable: bool | None = None
    cex_source_reachable: bool | None = None
    notes: list = field(default_factory=list)
    stats: dict = field(default_factory=dict)

    def to_dict(self):
        return {
            "invariant": self.invariant,
            "verdict": self.verdict,
            "base_status": self.base_status,
            "inductive_status": self.inductive_status,
            "base_counterexample": self.base_counterexample,
            "inductive_counterexample": (
                list(self.inductive_counterexample)
                if self.inductive_counterexample is not None
                else None
            ),
            "reachable_checked": self.reachable_checked,
            "reachable_count": self.reachable_count,
            "holds_on_reachable": self.holds_on_reachable,
            "cex_source_reachable": self.cex_source_reachable,
            "notes": list(self.notes),
            "stats": dict(self.stats),
        }


class InvariantChecker:
    """Reusable symbolic checker for one model.

    The model encoding (init predicate, transition relation, reachable
    set) is built once; successive candidates can then be checked
    incrementally via :meth:`check`, reusing the shared BDD manager and
    its caches.
    """

    def __init__(self, model, *, order_style="interleave", permutation=None,
                 node_limit=None, time_limit=None, reachability=True):
        self.model = model
        self.var_order = make_var_order(model.variables, order_style, permutation)
        self.mgr = BDDManager(self.var_order, node_limit=node_limit,
                              time_limit=time_limit)
        self._cur2next = {v: next_var(v) for v in model.variables}
        self._next2cur = {next_var(v): v for v in model.variables}
        self._next_vars = [next_var(v) for v in model.variables]
        self.error = None
        self.reach_error = None
        self._init = None
        self._T = None
        self._R = None
        try:
            self._init = exprmod.build(model.init, self.mgr)
            self._T = self._build_transition_relation()
        except ResourceExceeded as exc:
            self.error = f"resource limit while encoding model: {exc}"
        if self.error is None and reachability:
            try:
                self._R = self._compute_reachable()
            except ResourceExceeded as exc:
                self.reach_error = f"reachability undecided: {exc}"

    # ------------------------------------------------------------------
    # symbolic model encoding
    # ------------------------------------------------------------------
    def _build_transition_relation(self):
        mgr = self.mgr
        rel_all = mgr.constant(False)
        for t in self.model.transitions:
            rel = exprmod.build(t.guard, mgr)
            for v in self.model.variables:
                rhs_ast = t.assign.get(v)
                rhs = exprmod.build(rhs_ast, mgr) if rhs_ast is not None else mgr.var(v)
                rel = mgr.apply("and", rel, mgr.apply("iff", mgr.var(next_var(v)), rhs))
            rel_all = mgr.apply("or", rel_all, rel)
        return rel_all

    def _compute_reachable(self):
        mgr = self.mgr
        reach = self._init
        while True:
            img = mgr.rename(
                mgr.exists(mgr.apply("and", reach, self._T), self.model.variables),
                self._next2cur,
            )
            new = mgr.apply("or", reach, img)
            if new == reach:
                return reach
            reach = new

    # ------------------------------------------------------------------
    # checking
    # ------------------------------------------------------------------
    def check(self, inv_ast, inv_text=""):
        res = CheckResult(invariant=inv_text)
        mgr = self.mgr
        if self.error is not None:
            res.notes.append(self.error)
            return self._finish(res)
        try:
            inv = exprmod.build(inv_ast, mgr)
            ninv = mgr.negate(inv)
            # ---- base case: init ⊆ inv ---------------------------------
            if self._init == 0:
                res.base_status = "empty_init"
                res.notes.append(
                    "initial state set is empty; the base case holds vacuously"
                )
            else:
                bad0 = mgr.apply("and", self._init, ninv)
                if bad0 == 0:
                    res.base_status = "pass"
                else:
                    res.base_status = "fail"
                    wit = mgr.pick_sat(bad0)
                    res.base_counterexample = {v: wit[v] for v in self.model.variables}
            # ---- inductive step: inv ∧ T ⇒ inv' -------------------------
            ninv_next = mgr.rename(ninv, self._cur2next)
            pre_bad = mgr.exists(mgr.apply("and", self._T, ninv_next), self._next_vars)
            bad1 = mgr.apply("and", inv, pre_bad)
            if bad1 == 0:
                res.inductive_status = "pass"
            else:
                res.inductive_status = "fail"
                s_full = mgr.pick_sat(bad1)
                s = {v: s_full[v] for v in self.model.variables}
                succ = mgr.apply("and", mgr.restrict(self._T, s), ninv_next)
                sp_full = mgr.pick_sat(succ)
                sp = {cur_of(k): val for k, val in sp_full.items() if is_next(k)}
                res.inductive_counterexample = (s, sp)
        except ResourceExceeded as exc:
            res.notes.append(f"resource limit during check: {exc}")
            return self._finish(res)
        # ---- reachable-state facts --------------------------------------
        if self._R is not None:
            res.reachable_checked = True
            res.reachable_count = mgr.sat_count(self._R, self.model.variables)
            res.holds_on_reachable = mgr.apply("and", self._R, ninv) == 0
            if res.inductive_counterexample is not None:
                src = res.inductive_counterexample[0]
                res.cex_source_reachable = mgr.eval(self._R, src)
                if not res.cex_source_reachable:
                    res.notes.append(
                        "inductive counterexample starts from an UNREACHABLE state: "
                        "the candidate holds on every reachable state but is not "
                        "inductive; holding on explored paths does not imply "
                        "inductiveness -- strengthen the candidate"
                    )
        elif self.reach_error:
            res.notes.append(self.reach_error)
        # ---- verdict -----------------------------------------------------
        if res.base_status == "fail":
            res.verdict = "base_case_failed"
        elif res.inductive_status == "fail":
            res.verdict = "inductive_failed"
        elif res.base_status in ("pass", "empty_init") and res.inductive_status == "pass":
            res.verdict = "verified"
            res.notes.append(
                "candidate is an inductive invariant (decided symbolically, "
                "without enumerating states)"
            )
        return self._finish(res)

    def _finish(self, res):
        res.stats = {
            "bdd_nodes": self.mgr.num_nodes,
            "variable_order": list(self.var_order),
        }
        return res


def check_invariant(model, inv_ast, inv_text="", **kw):
    """One-shot convenience wrapper around :class:`InvariantChecker`."""
    return InvariantChecker(model, **kw).check(inv_ast, inv_text)


def check_incremental(model, inv_ast, inv_text, refinements, **kw):
    """Check a candidate and successive strengthenings on one checker.

    ``refinements`` is a list of ``(ast, text)``; each stage conjoins its
    refinement to the previous candidate.  Returns one CheckResult per
    stage.  The model encoding and BDD caches are shared across stages.
    """
    checker = InvariantChecker(model, **kw)
    stages = []
    ast, text = inv_ast, inv_text
    stages.append(checker.check(ast, text))
    for r_ast, r_text in refinements:
        ast = ("and", ast, r_ast)
        text = f"({text}) & ({r_text})"
        stages.append(checker.check(ast, text))
    return stages
