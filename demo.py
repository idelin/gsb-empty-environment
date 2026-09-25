"""Run sample: composable compressed bitmap selection over typed batches."""

from bitmap import Bitmap, BitmapLengthError, CHUNK_BITS
from table import ColType, Table


def main():
    print("== typed batches ==")
    table = Table({"id": ColType.INT, "temp": ColType.FLOAT,
                   "city": ColType.STRING})
    table.add_batch({
        "id": [1, 2, 3, 4],
        "temp": [21.5, float("nan"), None, 35.0],
        "city": ["beijing", "shanghai", None, "beijing"],
    })
    table.add_batch({
        "id": [5, 6],
        "temp": [28.0, 19.5],
        "city": ["shenzhen", "beijing"],
    })
    print(f"ingested {table.num_rows} rows in {table.num_batches} batches")

    print("\n== null vs NaN ==")
    is_null = table.where(table.col("temp").is_null())
    is_nan = table.where(table.col("temp").is_nan())
    print(f"null rows: {is_null.to_indices()}  nan rows: {is_nan.to_indices()}")
    print(f"null & nan overlap: {(is_null & is_nan).count()} (must be 0)")

    print("\n== composable selection ==")
    pred = (table.col("city") == "beijing") & ~(table.col("temp") < 30.0)
    bm = table.where(pred)
    print(f"(city == 'beijing') & ~(temp < 30) -> {bm!r} rows {bm.to_indices()}")
    for row in table.filter(pred):
        print(f"  {row}")

    print("\n== rank/select roundtrip ==")
    sel = table.where((table.col("city") == "beijing")
                      | (table.col("temp") < 20.0))
    print(f"selection rows: {sel.to_indices()}")
    for i in sel:
        print(f"  rank({i}) = {sel.rank(i)}  "
              f"select({sel.rank(i)}) = {sel.select(sel.rank(i))}")

    print("\n== exact integer arithmetic ==")
    big = Table({"n": ColType.INT})
    big.add_batch({"n": [2 ** 62, 2 ** 62 + 1, None]})
    hit = big.where(big.col("n") * 2 == 2 ** 63 + 2)
    print(f"n*2 == 2**63 + 2 selects {hit.to_indices()} "
          f"(floats would merge 2**62 and 2**62+1)")

    print("\n== compressed segments (sparse + dense) ==")
    n = 2 * CHUNK_BITS + 100  # not segment-aligned: tail padding exists
    idx = list(range(0, CHUNK_BITS, 2))              # dense region
    idx += [CHUNK_BITS + 100, CHUNK_BITS + 200]      # sparse region
    wide = Bitmap.from_indices(n, idx)
    print(f"segments: {wide.segment_kinds()}  count={wide.count()}")
    flipped = ~wide
    print(f"NOT: count={flipped.count()} of length {len(flipped)}; "
          f"max index {max(flipped)} < {len(flipped)} (no tail-bit leak)")

    print("\n== length mismatch ==")
    try:
        _ = Bitmap.zeros(4) & Bitmap.zeros(5)
    except BitmapLengthError as exc:
        print(f"BitmapLengthError: {exc}")
    try:
        table.filter(Bitmap.zeros(3))
    except BitmapLengthError as exc:
        print(f"BitmapLengthError: {exc}")


if __name__ == "__main__":
    main()
