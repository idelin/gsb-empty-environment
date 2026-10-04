"""Stable identifiers and versions for facts, rules, and proof steps.

Every fact (evidence record), rule (hash/padding/verification rule), and
step (evidence-log entry) carries a stable, human-readable ID plus a
version.  IDs never change once published; a behavioural change requires a
new version suffix.
"""

#: Global scheme version.  Mixed into every hash so that proofs produced
#: under different versions can never be spliced together.
SCHEME_VERSION = "merkle-evidence/1"

# --- Rule IDs (stable, versioned) ---------------------------------------
RULE_LEAF_HASH = "rule:leaf-hash@1"        # leaf hash binds version+index+content
RULE_PAD_LEAF = "rule:pad-leaf@1"          # deterministic padding leaf
RULE_PAD_SUBTREE = "rule:pad-subtree@1"    # deterministic padding subtree root
RULE_NODE_HASH = "rule:node-hash@1"        # internal node hash
RULE_EMPTY_ROOT = "rule:empty-root@1"      # root of the empty tree
RULE_MEMBERSHIP = "rule:membership-proof@1"
RULE_BATCH = "rule:batch-proof@1"          # shared-path multi-member proof
RULE_CONSISTENCY = "rule:consistency-proof@1"  # append-only consistency
RULE_REPLAY = "rule:evidence-replay@1"     # deterministic log replay

#: Marker used inside ordered sibling lists for a padding node that the
#: verifier must recompute itself (never trust the sender for it).
PAD_MARKER = "PAD"


def fact_id(seq: int) -> str:
    """Stable ID for the ``seq``-th evidence fact (0-based)."""
    return f"fact:{seq:06d}@1"


def step_id(seq: int) -> str:
    """Stable ID for the ``seq``-th evidence-log step (0-based)."""
    return f"step:{seq:06d}@1"
