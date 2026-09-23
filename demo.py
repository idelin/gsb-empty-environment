from miniqa import Catalog, Column, Comparison, ExecutionEngine, Param, Select, UnionAll, make_table


def build_catalog() -> Catalog:
    orders = make_table(
        name="orders",
        columns=("id", "status", "amount"),
        rows=(
            (1, "PAID", 10),
            (2, "PAID", 25),
            (3, "OPEN", 75),
            (4, "PAID", 90),
            (5, "PAID", 100),
            (6, "CANCELLED", None),
        ),
        indexed_columns=("status", "amount"),
        multiplicity=(1, 2, 1, 1, 1, 1),
    )
    return Catalog({"orders": orders})


def main() -> None:
    catalog = build_catalog()
    engine = ExecutionEngine(catalog)

    amount_query = Select(
        table="orders",
        predicate=Comparison(Column("amount"), ">=", Param("min_amount")),
        outputs=(Column("id"), Column("amount")),
    )
    status_query = Select(
        table="orders",
        predicate=Comparison(Column("status"), "=", Param("state")),
        outputs=(Column("id"), Column("status")),
    )
    amount_ids = Select(
        table="orders",
        predicate=Comparison(Column("amount"), ">=", Param("min_amount")),
        outputs=(Column("id"),),
    )
    status_ids = Select(
        table="orders",
        predicate=Comparison(Column("status"), "=", Param("state")),
        outputs=(Column("id"),),
    )
    union_query = UnionAll(amount_ids, status_ids)
    null_query = Select(
        table="orders",
        predicate=Comparison(Column("status"), "=", Param("state")),
        outputs=(Column("id"), Column("status"), Column("amount")),
    )

    print("amount >= 70:", engine.execute(amount_query, {"min_amount": 70}))
    print("amount >= 25 (same int/in-range category):", engine.execute(amount_query, {"min_amount": 25}))
    print("status = PAID:", engine.execute(status_query, {"state": "PAID"}))
    print("union all:", engine.execute(union_query, {"min_amount": 90, "state": "PAID"}))
    print("status = NULL:", engine.execute(null_query, {"state": None}))
    print("cache stats before refresh:", engine.cache.stats)
    catalog.refresh_stats("orders")
    print("amount >= 25 after stats change:", engine.execute(amount_query, {"min_amount": 25}))
    print("cache stats after refresh:", engine.cache.stats)


if __name__ == "__main__":
    main()
