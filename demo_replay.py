#!/usr/bin/env python3
"""Deterministic replay for sparse masked A @ B maintenance."""

from pathlib import Path
import tempfile

from masked_product import MaskedABProduct


def show(engine, label):
    print(f"-- {label}")
    print(f"version={engine.current_version.as_tuple()} result={engine.query()}")
    print(
        "memory="
        f"{engine.memory_contribution_count} spilled_rows="
        f"{engine.spilled_row_count} active="
        f"{engine.active_contribution_count}"
    )


def main():
    with tempfile.TemporaryDirectory() as tmp:
        engine = MaskedABProduct(
            2,
            3,
            2,
            row_threshold=2,
            memory_contributions=3,
            spill_dir=Path(tmp) / "spill",
        )
        with engine.batch() as batch:
            batch.set_a(0, 0, 2).set_a(0, 1, -2).set_a(1, 0, 4)
            batch.set_b(0, 0, 3).set_b(0, 1, 1).set_b(1, 0, 3)
            batch.add_mask(0, 0).add_mask(0, 1).add_mask(1, 0)
        show(engine, "two k-paths cancel at (0,0); (1,0)=12")

        old = engine.snapshot()
        with engine.batch() as batch:
            batch.delete_a(0, 1)
        show(engine, "withdraw one path: (0,0) is restored to 6")
        print(f"sources(0,0)={engine.contribution_sources(0, 0)}")
        print(f"pinned old result={old.rows()}")
        old.close()

        with engine.batch() as batch:
            batch.remove_mask(0, 1)
        show(engine, "mask removal changes visibility only: (0,1) hidden")

        with engine.batch() as batch:
            batch.add_mask(0, 1)
        show(engine, "mask rejoin restores (0,1)=2")

        with engine.batch() as batch:
            batch.set_b(0, 1, 5)
        show(engine, "B update inside one transaction")

        engine.check_integrity()
        engine.close()


if __name__ == "__main__":
    main()
