"""Runnable sample (synthetic data only):  python3 -m secxpl.demo"""

from __future__ import annotations

from .explain import explain, replay
from .naive import naive_explain
from .noninterference import find_violations
from .sample import QUERIES, UNIVERSE, all_worlds, make_world


def show(title: str, body: str) -> None:
    print(f"=== {title} ===")
    print(body)
    print()


def main() -> None:
    full = make_world(f.fact_id for f in UNIVERSE)

    show(
        "1. clearance 0, query 'shutdown-required' (restricted premises)",
        explain(full, 0, "shutdown-required").transcript(),
    )
    show(
        "2. clearance 1, query 'shutdown-required' (fully visible)",
        explain(full, 1, "shutdown-required").transcript(),
    )

    err_hidden = explain(full, 0, "manual-override")
    err_unknown = explain(full, 0, "nonexistent-id")
    normalize = lambda e: e.error.replace(f"'{e.query}'", "'<id>'")
    show(
        "3. hidden fact vs nonexistent id: identical errors",
        f"hidden:      {err_hidden.error}\nnonexistent: {err_unknown.error}\n"
        f"identical up to the caller-supplied id: "
        f"{normalize(err_hidden) == normalize(err_unknown)}",
    )

    world_short = make_world(["temp-reading-ok", "power-ok", "core-temp-critical"])
    world_long = make_world(["temp-reading-ok", "power-ok", "sensor-raw-anomaly"])
    show(
        "4. low-equivalent pair, naive renderer LEAKS via counts/depth",
        "world A (hidden chain length 1):\n"
        + naive_explain(world_short, 0, "shutdown-required").transcript()
        + "\n\nworld B (hidden chain length 2):\n"
        + naive_explain(world_long, 0, "shutdown-required").transcript(),
    )
    show(
        "5. same pair, secure renderer: identical transcripts",
        "identical: "
        + str(
            explain(world_short, 0, "shutdown-required").transcript()
            == explain(world_long, 0, "shutdown-required").transcript()
        )
        + "\n"
        + explain(world_short, 0, "shutdown-required").transcript(),
    )

    evidence = explain(full, 0, "shutdown-required").evidence
    show(
        "6. evidence replay",
        f"replay(evidence): {replay(evidence)}",
    )

    worlds = all_worlds()
    naive_violations = find_violations(naive_explain, worlds, 0, QUERIES)
    secure_violations = find_violations(explain, worlds, 0, QUERIES)
    show(
        "7. finite-domain pairwise noninterference test "
        f"({len(worlds)} worlds, {len(QUERIES)} queries, clearance 0)",
        f"naive renderer violations:  {len(naive_violations)}\n"
        f"secure renderer violations: {len(secure_violations)}",
    )


if __name__ == "__main__":
    main()
