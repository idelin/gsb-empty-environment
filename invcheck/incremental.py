"""Incremental candidate correction.

A session keeps one BDD manager (and its caches) alive while the user
edits the candidate invariant, so re-checks are cheap.  It can also
suggest strengthenings derived from step counterexamples and iterate them
to a fixpoint.
"""

from __future__ import annotations

from . import expr as expr_mod
from .checker import FAIL_BASE, FAIL_STEP, PASS, UNKNOWN, InvariantChecker
from .model import load_model


class IncrementalSession:
    def __init__(self, model, variable_order=None, max_nodes=None, max_ops=None,
                 max_reach_iter=100000):
        self.model = load_model(model)
        self.checker = InvariantChecker(
            self.model, variable_order=variable_order,
            max_nodes=max_nodes, max_ops=max_ops,
            max_reach_iter=max_reach_iter,
        )
        self.invariant = self.model.invariant
        self.history = []  # list of (invariant_text, CheckResult)

    def check(self, invariant=None):
        """(Re)check the current or a newly supplied candidate."""
        if invariant is not None:
            self.invariant = invariant
        result = self.checker.check(self.invariant)
        self.history.append((self.invariant, result))
        return result

    def suggest_strengthening(self):
        """Conjoin the negation of the latest step-cex source cube.

        Returns the new candidate text, or None if no step counterexample
        is available.  Excluding the source cube strictly shrinks the
        candidate's state set, so iteration terminates on finite models.
        """
        if not self.history:
            return None
        result = self.history[-1][1]
        if result.step_cex is None:
            return None
        clause = expr_mod.cube_to_expr(result.step_cex.state)
        return "(%s) & ~(%s)" % (self.invariant, clause)

    def auto_strengthen(self, max_rounds=16):
        """Iterate: check -> strengthen on FAIL_STEP, until fixpoint.

        Stops on PASS (inductive), FAIL_BASE (strengthening killed an
        initial state -- candidate family cannot work this way), UNKNOWN
        (resources), or when no further suggestion exists.
        """
        for _ in range(max_rounds):
            result = self.check()
            if result.verdict != FAIL_STEP:
                return result
            suggestion = self.suggest_strengthening()
            if suggestion is None or suggestion == self.invariant:
                return result
            self.invariant = suggestion
        return self.history[-1][1] if self.history else None
