"""Cross-source confident-evidence decision engine.

Facts are ground atoms (plain strings). Rules are definite (Horn) clauses:
a finite (possibly empty) premise set entails one conclusion. There is no
negation. Each externally supplied base fact may be supported by several
independent sources. The engine computes, for every derived fact, the
inclusion-minimal union sets of sources that support it (the well-founded /
least-fixpoint model), so a cycle can never support itself out of nothing.
"""

from .engine import BudgetExceeded, Engine, Rule

__all__ = ["BudgetExceeded", "Engine", "Rule"]
