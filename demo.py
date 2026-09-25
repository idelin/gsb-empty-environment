"""End-to-end demo of the schemamig package.  Run: python3.11 demo.py"""
from decimal import Decimal

from schemamig import (
    AddColumn, AmbiguousMappingError, Batch, Column, ConvertColumn,
    LossyImplicitError, MergeConflict, Migration, Policy, RenameColumn,
    Repository, Schema, Type, WidenColumn, find_counterexample,
    three_way_merge,
)


def show(title):
    print(f"\n=== {title} ===")


def main():
    schema_v1 = Schema([
        Column("c_id", "id", Type.INT, nullable=False),
        Column("c_name", "name", Type.STRING),
        Column("c_bal", "balance", Type.INT),
    ])
    repo = Repository(schema_v1)
    repo.ingest(Batch(schema_v1, {
        "c_id": (1, 2, 3),
        "c_name": ("ada", "bob", None),
        "c_bal": (100, -5, 2 ** 70 + 1),
    }))
    show("v1 schema + batch")
    print("schema:", repo.schema)
    print("rows:  ", repo.batches[0].rows())

    show("publish v2: rename + exact widening + new nullable column")
    mig_v2 = Migration([
        RenameColumn("c_name", "full_name"),
        WidenColumn("c_bal", Type.DECIMAL),
        AddColumn("c_mail", "email", Type.STRING),
    ])
    repo.publish(mig_v2, batch=Batch(schema_v1, {
        "c_id": (4,), "c_name": ("cid",), "c_bal": (9,),
    }))
    print("schema:", repo.schema)
    for batch in repo.batches:
        print("rows:  ", batch.rows())
    print("identity kept: c_name ->", repo.schema.column("c_name").name,
          "| big int exact:", repo.batches[0].column("c_bal")[2])

    show("rejected: lossy implicit conversion (decimal -> int)")
    try:
        Migration([WidenColumn("c_bal", Type.INT)]).apply(repo.schema)
    except LossyImplicitError as exc:
        print("LossyImplicitError:", exc)

    show("rejected: ambiguous mapping (two sources -> one target name)")
    try:
        Migration([RenameColumn("c_id", "dup"),
                   RenameColumn("c_name", "dup")]).apply(repo.schema)
    except AmbiguousMappingError as exc:
        print("AmbiguousMappingError:", exc)

    show("explicit lossy conversion keeps per-value diagnostics")
    amounts = Schema([Column("d", "amount", Type.DECIMAL)])
    batch = Batch(amounts, {"d": (Decimal("1.5"), Decimal("-2.25"),
                                  Decimal("7"), None, Decimal("NaN"))})
    out, diags = Migration(
        [ConvertColumn("d", Type.INT, Policy.TRUNCATE)]
    ).project(amounts, Batch(amounts, {"d": batch.column("d")[:4]}))
    print("projected:", out.column("d"))
    for d in diags:
        print(f"  diagnostic row {d.row}: {d.old_value} -> {d.new_value}"
              f" ({d.reason})")
    print("null vs NaN distinct: null =", batch.column("d")[3],
          "| NaN =", batch.column("d")[4])

    show("atomic publish: failure leaves no partial new columns")
    try:
        repo.publish(Migration([
            AddColumn("c_extra", "extra", Type.INT),
            RenameColumn("c_name", "balance"),
        ]))
    except AmbiguousMappingError as exc:
        print("publish failed:", exc)
    print("schema unchanged:", repo.schema)
    print("no partial column extra:", not repo.schema.has("c_extra"))

    show("three-way merge of branch schemas")
    base = repo.schema
    mig_a = Migration([RenameColumn("c_name", "display_name")])
    mig_b = Migration([RenameColumn("c_name", "tmp"),
                       RenameColumn("c_name", "display_name"),
                       AddColumn("c_tag", "tag", Type.STRING)])
    result = three_way_merge(base, mig_a, mig_b)
    print("merged:", result.schema)
    path_a = mig_a + result.from_a
    path_b = mig_b + result.from_b
    cex = find_counterexample(base, path_a, path_b, max_rows=1)
    print("paths base->A->merge vs base->B->merge value-equivalent:",
          cex is None)

    show("merge conflict: rename chains that do not converge")
    try:
        three_way_merge(base,
                        Migration([RenameColumn("c_name", "x")]),
                        Migration([RenameColumn("c_name", "y")]))
    except MergeConflict as exc:
        print("MergeConflict:", exc)

    show("merge conflict: new column must not swallow an old column")
    try:
        three_way_merge(base,
                        Migration([RenameColumn("c_bal", "total")]),
                        Migration([AddColumn("c_new", "total", Type.INT)]))
    except MergeConflict as exc:
        print("MergeConflict:", exc)

    show("counterexample: truncate vs floor are not value-equivalent")
    amounts = Schema([Column("d", "amount", Type.DECIMAL)])
    cex = find_counterexample(
        amounts,
        Migration([ConvertColumn("d", Type.INT, Policy.TRUNCATE)]),
        Migration([ConvertColumn("d", Type.INT, Policy.FLOOR)]))
    print("counterexample input rows:", cex.batch.rows())
    print("truncate path:", cex.left.column("d"),
          "| floor path:", cex.right.column("d"))


if __name__ == "__main__":
    main()
