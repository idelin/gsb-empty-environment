"""Symbolic invariant checking: base case and one-step preservation.

Everything here works on BDDs (symbolic set operations); no state is ever
enumerated.  Verdicts:

* ``PASS``       -- candidate holds in all initial states and is preserved
                    by every transition from *any* state satisfying it
                    (inductive, hence an invariant of the reachable part).
* ``FAIL_BASE``  -- some initial state violates the candidate.
* ``FAIL_STEP``  -- base case holds, but some state satisfying the
                    candidate has a successor violating it.  The source
                    state may be unreachable; that is reported explicitly
                    because "holds on explored paths" is not induction.
* ``UNKNOWN``    -- a resource budget was exceeded, or the reachability
                    search did not converge; nothing is concluded.
"""

from __future__ import annotations

from dataclasses import dataclass, field

from . import expr as expr_mod
from .bdd import BDDManager, ResourceLimitExceeded
from .model import Model, load_model, next_name, strip_prime

PASS = "PASS"
FAIL_BASE = "FAIL_BASE"
FAIL_STEP = "FAIL_STEP"
UNKNOWN = "UNKNOWN"

REACHABLE = "reachable"
UNREACHABLE = "unreachable"
MIXED = "mixed"
REACH_UNKNOWN = "unknown"


@dataclass
class StepCounterexample:
    state: dict          # minimal partial assignment, current-state vars
    next_state: dict     # minimal partial assignment, next-state vars (primes stripped)
    reachability: str = REACH_UNKNOWN

    def to_dict(self):
        return {
            "state": dict(sorted(self.state.items())),
            "next_state": dict(sorted(self.next_state.items())),
            "reachability": self.reachability,
        }


@dataclass
class CheckResult:
    verdict: str
    invariant: str
    variable_order: list
    init_empty: bool = False
    base_ok: bool = False
    base_cex: dict = None            # minimal partial assignment (current vars)
    step_ok: bool = False
    step_cex: StepCounterexample = None
    reachable_complete: bool = False
    reachable_states: int = None     # count if reachability converged
    holds_on_reachable: bool = None  # None if reachability did not converge
    unknown_reason: str = None
    notes: list = field(default_factory=list)
    stats: dict = field(default_factory=dict)

    def to_dict(self):
        return {
            "verdict": self.verdict,
            "invariant": self.invariant,
            "variable_order": list(self.variable_order),
            "init_empty": self.init_empty,
            "base_ok": self.base_ok,
            "base_cex": dict(sorted(self.base_cex.items())) if self.base_cex else None,
            "step_ok": self.step_ok,
            "step_cex": self.step_cex.to_dict() if self.step_cex else None,
            "reachable_complete": self.reachable_complete,
            "reachable_states": self.reachable_states,
            "holds_on_reachable": self.holds_on_reachable,
            "unknown_reason": self.unknown_reason,
            "notes": list(self.notes),
            "stats": dict(self.stats),
        }


class InvariantChecker:
    """Checks candidates against one finite-state model, symbolically."""

    def __init__(self, model, variable_order=None, max_nodes=None, max_ops=None,
                 max_reach_iter=100000):
        self.model = load_model(model)
        order = list(variable_order) if variable_order else self.model.all_variables()
        if sorted(order) != sorted(self.model.all_variables()):
            raise ValueError("variable_order must be a permutation of all "
                             "current and next variables")
        self.order = order
        self.mgr = BDDManager(order, max_nodes=max_nodes, max_ops=max_ops)
        self.max_reach_iter = max_reach_iter
        self.current_vars = list(self.model.variables)
        self.next_vars = self.model.next_variables()
        self._cur_to_next = {v: next_name(v) for v in self.current_vars}
        self._next_to_cur = {next_name(v): v for v in self.current_vars}
        self._build_error = None
        try:
            self._build_static()
        except ResourceLimitExceeded as exc:
            self._build_error = str(exc)

    # ------------------------------------------------------------- construction

    def _build_static(self):
        mgr = self.mgr
        self.init_bdd = expr_mod.to_bdd(mgr, self.model._init_ast)
        self.trans_bdd = self._build_transition()

    def _build_transition(self):
        mgr = self.mgr
        model = self.model
        kind = model.transition_kind()
        if kind == "relation":
            return expr_mod.to_bdd(mgr, expr_mod.parse(model.transition["relation"]))
        if kind == "next":
            clauses = []
            for v in model.variables:
                f = model.transition["next"].get(v)
                body = (expr_mod.to_bdd(mgr, expr_mod.parse(f)) if f is not None
                        else mgr.var(v))
                clauses.append(mgr.iff(mgr.var(next_name(v)), body))
            return mgr.and_all(clauses)
        if kind == "edges":
            mgr = self.mgr
            edge_bdds = []
            for src, dst in model.transition["edges"]:
                src_cube = mgr.cube(model.value_to_state(src))
                dst_cube = mgr.cube({next_name(k): b
                                     for k, b in model.value_to_state(dst).items()})
                edge_bdds.append(mgr.and_(src_cube, dst_cube))
            return mgr.or_all(edge_bdds)
        raise ValueError("invalid transition spec")

    # --------------------------------------------------------------- internals

    def _stats(self):
        return {
            "bdd_nodes": self.mgr.node_count,
            "bdd_ops": self.mgr.op_count,
            "variables": len(self.order),
        }

    def _split_cube(self, cube):
        state, nxt = {}, {}
        for name, val in cube.items():
            if name in self._next_to_cur:
                nxt[strip_prime(name)] = val
            else:
                state[name] = val
        return state, nxt

    def _reachable(self):
        """BFS fixpoint of the image operator.  Returns (R, converged, iters)."""
        mgr = self.mgr
        reached = self.init_bdd
        frontier = self.init_bdd
        iters = 0
        while frontier != mgr.FALSE and iters < self.max_reach_iter:
            img_next = mgr.exists(mgr.and_(frontier, self.trans_bdd), self.current_vars)
            img = mgr.rename(img_next, self._next_to_cur)
            new = mgr.and_(img, mgr.negate(reached))
            reached = mgr.or_(reached, new)
            frontier = new
            iters += 1
        converged = frontier == mgr.FALSE
        return reached, converged, iters

    # ------------------------------------------------------------------ check

    def check(self, invariant=None):
        mgr = self.mgr
        inv_text = invariant if invariant is not None else self.model.invariant
        if self._build_error is not None:
            return CheckResult(
                verdict=UNKNOWN, invariant=inv_text, variable_order=self.order,
                unknown_reason="model construction exceeded resources: " + self._build_error,
                notes=["resource budget exceeded; verdict left undecided"],
                stats=self._stats(),
            )
        try:
            mgr.reset_op_count()
            inv_ast = expr_mod.parse(inv_text)
            unknown_vars = expr_mod.ast_variables(inv_ast) - set(self.current_vars)
            if unknown_vars:
                raise ValueError("invariant uses unknown variables: %s" % sorted(unknown_vars))
            inv = expr_mod.to_bdd(mgr, inv_ast)
            inv_next = mgr.rename(inv, self._cur_to_next)

            notes = []
            init_empty = self.init_bdd == mgr.FALSE
            if init_empty:
                notes.append("initial state set is EMPTY; the base case holds "
                             "vacuously and is reported separately")

            # ---- base case: Init & ~Inv ----------------------------------
            bad_base = mgr.and_(self.init_bdd, mgr.negate(inv))
            base_ok = bad_base == mgr.FALSE
            base_cex = None
            if not base_ok:
                cube = mgr.one_cube(bad_base)
                base_cex = mgr.minimize_cube(bad_base, cube)

            # ---- inductive step: Inv & T & ~Inv' -------------------------
            bad_step = mgr.and_(mgr.and_(inv, self.trans_bdd), mgr.negate(inv_next))
            step_ok = bad_step == mgr.FALSE
            step_cex = None
            if not step_ok:
                cube = mgr.one_cube(bad_step)
                cube = mgr.minimize_cube(bad_step, cube)
                state, nxt = self._split_cube(cube)
                step_cex = StepCounterexample(state=state, next_state=nxt)

            # ---- reachability (symbolic BFS, resource-bounded) -----------
            reachable_complete = False
            reachable_states = None
            holds_on_reachable = None
            try:
                mgr.reset_op_count()
                reached, converged, iters = self._reachable()
                if converged:
                    reachable_complete = True
                    # sat_count ranges over ALL manager variables; `reached`
                    # only mentions current vars, so divide out the 2^n_next
                    # don't-care factor to get the plain state count.
                    total = mgr.sat_count(reached)
                    reachable_states = total >> len(self.next_vars)
                    holds_on_reachable = mgr.and_(reached, mgr.negate(inv)) == mgr.FALSE
                    if step_cex is not None:
                        src = mgr.cube(step_cex.state)
                        inter_r = mgr.and_(src, reached) != mgr.FALSE
                        inter_nr = mgr.and_(src, mgr.negate(reached)) != mgr.FALSE
                        if inter_r and not inter_nr:
                            step_cex.reachability = REACHABLE
                        elif inter_nr and not inter_r:
                            step_cex.reachability = UNREACHABLE
                        else:
                            step_cex.reachability = MIXED
                else:
                    notes.append("reachability search did not converge within "
                                 "%d iterations; reachability of counterexamples "
                                 "is left undecided" % self.max_reach_iter)
            except ResourceLimitExceeded as exc:
                notes.append("reachability search exceeded resources (%s); "
                             "reachability facts left undecided" % exc)

            # ---- verdict ---------------------------------------------------
            if not base_ok:
                verdict = FAIL_BASE
                notes.append("base case failed: an initial state violates the "
                             "candidate; replay the counterexample to confirm")
            elif not step_ok:
                verdict = FAIL_STEP
                if step_cex.reachability == UNREACHABLE:
                    notes.append(
                        "inductive step failed ONLY through unreachable states: "
                        "the candidate may still hold on every reachable state, "
                        "but it is not inductive; holding on explored paths is "
                        "not induction -- strengthen the candidate")
                elif step_cex.reachability == REACHABLE:
                    notes.append("inductive step failed through a REACHABLE state: "
                                 "the candidate is falsified by real behaviour")
                else:
                    notes.append("inductive step failed; reachability of the "
                                 "counterexample source is undecided")
            else:
                verdict = PASS
                notes.append("candidate is inductive: it holds in all initial "
                             "states and is preserved by every transition from "
                             "any satisfying state, reachable or not")
            if reachable_complete and holds_on_reachable is False:
                notes.append("the candidate is VIOLATED on reachable states")
            elif reachable_complete and verdict == FAIL_STEP and holds_on_reachable:
                notes.append("the candidate holds on ALL reachable states "
                             "(confirmed by complete symbolic reachability), "
                             "yet it is not inductive")

            return CheckResult(
                verdict=verdict,
                invariant=inv_text,
                variable_order=self.order,
                init_empty=init_empty,
                base_ok=base_ok,
                base_cex=base_cex,
                step_ok=step_ok,
                step_cex=step_cex,
                reachable_complete=reachable_complete,
                reachable_states=reachable_states,
                holds_on_reachable=holds_on_reachable,
                notes=notes,
                stats=self._stats(),
            )
        except ResourceLimitExceeded as exc:
            return CheckResult(
                verdict=UNKNOWN, invariant=inv_text, variable_order=self.order,
                unknown_reason=str(exc),
                notes=["resource budget exceeded; verdict left undecided"],
                stats=self._stats(),
            )
