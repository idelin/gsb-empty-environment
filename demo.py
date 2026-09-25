"""Runnable sample: typed batches -> predicates -> composed bitmaps."""

from rowselect import Batch, Bitmap, Schema, Table, col


def main():
    schema = Schema([("id", "int"), ("price", "float"), ("tag", "str")])
    nan = float("nan")
    table = Table(schema, [
        Batch(schema, [(1, 10.0, "a"), (2, None, "b"), (3, nan, "a")]),
        Batch(schema, [(4, 25.5, "c"), (5, 7.25, "b"), (6, None, None)]),
    ])
    print(f"table: {table.num_rows} rows in {len(table.batches)} batches\n")

    cheap = table.where(col("price") < 20.0)
    tagged_a = table.where(col("tag") == "a")
    print("price < 20.0        ->", cheap.to_indices(), "(null/NaN excluded)")
    print("tag == 'a'          ->", tagged_a.to_indices())
    print("AND                 ->", (cheap & tagged_a).to_indices())
    print("OR                  ->", (cheap | tagged_a).to_indices())
    print("NOT cheap           ->", (~cheap).to_indices())
    print("price IS NULL       ->", table.where(col("price").is_null()).to_indices())
    print("price IS NaN        ->", table.where(col("price").is_nan()).to_indices())
    print("fetch(tag == 'b')   ->", table.fetch(table.where(col("tag") == "b")))

    print("\n-- rank/select --")
    bm = table.where(col("price").is_not_null())
    print("selection:", bm.to_indices())
    for i in bm:
        print(f"  rank({i}) = {bm.rank(i)}, select({bm.rank(i)}) = {bm.select(bm.rank(i))}")

    print("\n-- NOT tail padding (length 5, chunk 512) --")
    flipped = ~Bitmap.zeros(5)
    print("~zeros(5):", flipped.to_indices(), "popcount =", flipped.popcount())

    print("\n-- length mismatch --")
    try:
        Bitmap.zeros(6) & Bitmap.zeros(7)
    except ValueError as exc:
        print("ValueError:", exc)


if __name__ == "__main__":
    main()
