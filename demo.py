"""Runnable sample: python3.11 demo.py"""

from pivot_engine import Batch, PivotEngine


def show(title, table):
    print(f"\n== {title} ==")
    print(" | ".join(map(str, table.columns)))
    for row in table.rows:
        print(" | ".join(repr(v) for v in row))
    if not table.rows:
        print("(empty)")


def main():
    eng = PivotEngine(agg="sum")

    # Batch 1: typed int batch; (alice, A) conflicts -> summed to 13.
    r1 = eng.apply(Batch("str", "str", "int", rows=[
        ("+", "alice", "A", 10),
        ("+", "alice", "B", 5),
        ("+", "bob", "A", 7),
        ("+", "alice", "A", 3),
    ]))
    show("batch 1: int, conflict (alice,A) summed", eng.snapshot())
    old_row = ("alice", 13, 5)  # emitted under schema v1 (columns A, B)

    # Batch 2: new category C appears; null and NaN stay distinct.
    r2 = eng.apply(Batch("str", "str", "float", rows=[
        ("+", "bob", "C", 1.5),
        ("+", "carol", "C", float("nan")),
        ("+", "carol", "A", None),
    ]))
    show("batch 2: new category C, null vs NaN", eng.snapshot())

    print("\nschema change events:")
    for ev in eng.events:
        print(f"  v{ev.version}: +{list(ev.added_columns)} -> columns {list(ev.columns)}")
    aligned = [old_row[0]] + eng.align_values(
        old_row[1:], from_version=r1.schema_version)
    print(f"old v1 row {old_row} aligned to v{r2.schema_version}: {tuple(aligned)}")

    # Batch 3: retractions. alice loses her only B row but keeps A;
    # carol loses her NaN row but keeps the (null) A cell.
    eng.apply(Batch("str", "str", "int", rows=[("-", "alice", "B", 5)]))
    eng.apply(Batch("str", "str", "float", rows=[("-", "carol", "C", float("nan"))]))
    show("after retracting (alice,B,5) and (carol,C,NaN)", eng.snapshot())

    # Batch 4: retracting the last cell of a key removes the key row.
    eng.apply(Batch("str", "str", "float", rows=[("-", "bob", "C", 1.5)]))
    eng.apply(Batch("str", "str", "int", rows=[("-", "bob", "A", 7)]))
    show("after retracting bob's last cell", eng.snapshot())


if __name__ == "__main__":
    main()
