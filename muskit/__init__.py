"""muskit: budget-aware unsat-core extraction for finite-domain constraints.

Pure standard library, no SAT/SMT solver is used.  The package distinguishes

* subset-minimal cores (deletion-based, per-item deletion witnesses), and
* minimum-cardinality cores (hitting-set search with lower-bound evidence),

and propagates budget exhaustion as UNDECIDED instead of guessing SAT/UNSAT.
"""
