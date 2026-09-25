"""Synthetic sample data and an end-to-end demo scenario.

All entities and evidence below are fabricated for demonstration only.

Scenario:
  e1 "Alice Zhang" and e2 "A. Zhang" share an email   -> f1: e1 = e2
  e2 and e3 "Alice Z." share a phone number           -> f2: e2 = e3
  f3 claims e1 != e3 (different employer records)     -> CONFLICT, ring f1-f2-f3
  f2 is retracted (phone was recycled)                -> component splits
  f3 is asserted again                                -> now accepted
  e4 is also named "Alice Zhang" but is never merged  -> names prove nothing
"""
from __future__ import annotations

from .engine import ConflictError, Engine
from .model import Fact, FactKind

ENTITIES: tuple[tuple[str, str], ...] = (
    ("e1", "Alice Zhang"),
    ("e2", "A. Zhang"),
    ("e3", "Alice Z."),
    ("e4", "Alice Zhang"),  # same display name as e1, different real entity
    ("e5", "Bob Li"),
)

F1 = Fact("f1", 1, FactKind.EQUIV, "e1", "e2", evidence="shared email alice.z@example.com")
F2 = Fact("f2", 1, FactKind.EQUIV, "e2", "e3", evidence="shared phone +86-138-0000-0000")
F3 = Fact("f3", 1, FactKind.NEQUIV, "e1", "e3", evidence="conflicting employer records")


def build_engine(verbose: bool = False) -> Engine:
    engine = Engine()
    for key, name in ENTITIES:
        engine.register_entity(key, name)

    def show(message: str) -> None:
        if verbose:
            print(message)

    show("== assert f1: e1 = e2 (shared email) ==")
    engine.assert_fact(F1)
    show("    components: " + _format_components(engine))

    show("== assert f2: e2 = e3 (shared phone) ==")
    engine.assert_fact(F2)
    show("    components: " + _format_components(engine))
    show(f"    representative of e3: {engine.representative('e3')}")

    show("== assert f3: e1 != e3 (employer records) ==")
    try:
        engine.assert_fact(F3)
    except ConflictError as error:
        show(f"    CONFLICT rejected: {error}")
        show(f"    evidence ring: {' -> '.join(error.ring.fact_ids)}")

    show("== retract f2 (phone number was recycled) ==")
    engine.retract_fact("f2")
    show("    components: " + _format_components(engine))

    show("== re-assert f3: e1 != e3 ==")
    engine.assert_fact(F3)
    show("    components: " + _format_components(engine))

    show("== same-name check ==")
    show(
        "    e1 and e4 are both named 'Alice Zhang' but stay separate: "
        f"rep(e1)={engine.representative('e1')}, rep(e4)={engine.representative('e4')}"
    )

    show("== replay verification ==")
    clone = engine.replayed()
    same = clone.components() == engine.components()
    show(f"    replayed {len(engine.steps)} steps, components identical: {same}")
    return engine


def _format_components(engine: Engine) -> str:
    parts = []
    for root, members in engine.components().items():
        label = ",".join(sorted(members))
        parts.append(f"[{label} rep={root}]")
    return " ".join(parts)


def main() -> None:
    build_engine(verbose=True)


if __name__ == "__main__":
    main()
