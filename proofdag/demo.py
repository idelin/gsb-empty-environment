"""Runnable synthetic sample: ``python -m proofdag`` (or proofdag.demo).

Builds a small equivalence-proof DAG from synthetic data, shows structural
sharing, canonical numbering/digest, evidence replay and incremental edge
deletion with revocation of unreachable shared subgraphs.
"""

from __future__ import annotations

from .model import Fact, Rule, Step
from .replay import replay
from .store import ProofStore

CONJ = Rule("conj-intro", 1, commutative=True)
CHAIN = Rule("ordered-chain", 1, commutative=False)

SEMANTICS = {
    ("conj-intro", 1): lambda ps: " ∧ ".join(sorted(ps)),
    ("ordered-chain", 1): lambda ps: ps[0] + " ⇒ " + ps[-1],
}


def build_sample_store() -> ProofStore:
    store = ProofStore()
    # Same value "t1 == t2" from two different sources: independent identities.
    f_a = Fact("eq-t1-t2", 1, "sensor-alpha", "t1 == t2")
    f_a2 = Fact("eq-t1-t2", 1, "sensor-gamma", "t1 == t2")
    f_b = Fact("eq-t2-t1", 1, "sensor-beta", "t2 == t1")

    conj = Step(
        "s-conj",
        1,
        CONJ,
        (f_a, f_b),
        Fact("conj-1", 1, "derived", "t1 == t2 ∧ t2 == t1"),
    )
    chain = Step(
        "s-chain",
        1,
        CHAIN,
        (conj, f_a2),
        Fact("chain-1", 1, "derived", "t1 == t2 ∧ t2 == t1 ⇒ t1 == t2"),
    )
    chain_key = store.add_step(chain)
    store.mark_root(chain_key)
    return store


def main() -> None:
    store = build_sample_store()
    print("== synthetic proof DAG ==")
    print(f"interned nodes: {store.node_count()}")

    # Commutative premises swapped -> same interned node, stored once.
    swapped = Step(
        "s-conj-swapped",
        1,
        CONJ,
        (
            Fact("eq-t2-t1", 1, "sensor-beta", "t2 == t1"),
            Fact("eq-t1-t2", 1, "sensor-alpha", "t1 == t2"),
        ),
        Fact("conj-1", 1, "derived", "t1 == t2 ∧ t2 == t1"),
    )
    key_a = store.step_index["s-conj"]
    key_b = store.add_step(swapped)
    print(f"swapped conjunctive premises intern to same node: {key_a == key_b}")
    print(f"nodes after re-adding swapped step: {store.node_count()}")

    form = store.canonical_form()
    print(f"canonical nodes: {len(form.nodes)}, roots: {form.roots}")
    print(f"canonical digest: {form.digest}")

    # Evidence is replayable: structure and rule semantics are re-derived.
    evidence = store.evidence()
    replayed = replay(evidence, SEMANTICS)
    print(f"evidence replay ok: {replayed.digest == evidence['digest']}")

    # Incremental deletion: add a second parent sharing the conjunctive
    # subproof, then remove edges and watch unreachable nodes get revoked.
    f_a2 = Fact("eq-t1-t2", 1, "sensor-gamma", "t1 == t2")
    conj_key = store.step_index["s-conj"]
    conj_step = Step(
        "s-conj-again",
        1,
        CONJ,
        (
            Fact("eq-t1-t2", 1, "sensor-alpha", "t1 == t2"),
            Fact("eq-t2-t1", 1, "sensor-beta", "t2 == t1"),
        ),
        Fact("conj-1", 1, "derived", "t1 == t2 ∧ t2 == t1"),
    )
    chain2 = Step(
        "s-chain-2",
        1,
        CHAIN,
        (f_a2, conj_step),
        Fact("chain-2", 1, "derived", "t1 == t2 ⇒ t1 == t2 ∧ t2 == t1"),
    )
    chain2_key = store.add_step(chain2)
    store.mark_root(chain2_key)
    print(f"nodes with shared subproof referenced twice: {store.node_count()}")
    print(f"back-references of shared conj node: {len(store.parents_of(conj_key))}")

    store.remove_edge(chain2_key, conj_key)
    print(f"after removing one edge, conj still reachable: {store.has_node(conj_key)}")
    store.remove_root(chain2_key)
    print(f"after removing its root, chain-2 revoked: {not store.has_node(chain2_key)}")
    print(f"shared conj node still alive via other root: {store.has_node(conj_key)}")
    print(f"final node count: {store.node_count()}")


if __name__ == "__main__":
    main()
