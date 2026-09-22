"""End-to-end demo: scan -> filter -> join -> project -> limit, pull-style."""

from __future__ import annotations

import os

from make_data import generate
from pipeline import CsvScan, Filter, HashJoin, Limit, Project, run, sql_gt

DATA = os.path.join(os.path.dirname(os.path.abspath(__file__)), "data")


def main() -> None:
    users_path, orders_path = DATA + "/users.csv", DATA + "/orders.csv"
    if not os.path.exists(users_path):
        generate(out_dir=DATA)

    users = CsvScan(users_path, types=[int, str, int])          # (id, name, age)
    orders = CsvScan(orders_path, types=[int, int, float])      # (oid, user_id, amount)

    big_orders = Filter(orders, lambda r: sql_gt(r[2], 400.0))  # amount > 400
    joined = HashJoin(users, big_orders,
                      left_key=lambda u: (u[0],),               # users.id
                      right_key=lambda o: (o[1],))              # orders.user_id
    projected = Project(joined, lambda r: (r[1], r[5]))         # (name, amount)
    top3 = Limit(projected, 3)

    print("pipeline: CsvScan(users) JOIN CsvScan(orders) WHERE amount>400 "
          "PROJECT (name, amount) LIMIT 3")
    rows = run(top3, sink=lambda b: [print("  row:", tuple(r)) for r in b],
               batch_size=2)
    print(f"delivered {rows} rows")
    print(f"users rows read    : {users.rows_read}")
    print(f"orders rows read   : {orders.rows_read} (hash-join build side, fully materialized by design)")
    print(f"users probe stopped after {users.rows_read} rows thanks to LIMIT 3")
    print(f"all operators closed: users={users.closed}, orders={orders.closed}, "
          f"join={joined.closed}, limit={top3.closed}")
    print(f"users file closed  : {users.file_closed}")


if __name__ == "__main__":
    main()
