"""invcheck: symbolic invariant checking for finite-state models.

Candidate invariants are represented as shared reduced ordered binary
decision diagrams (ROBDDs).  The initial-state check and the one-step
preservation (inductiveness) check are performed with symbolic set
operations on BDDs; individual states are only materialized when a
counterexample witness is extracted.  A brute-force reference checker
is provided for small domains only.
"""

__version__ = "0.1.0"
