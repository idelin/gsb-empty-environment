"""Generate synthetic relational tables locally (no SQL engine involved).

Tables (CSV, empty field = NULL, duplicates included on purpose):
- users(id, name, age)     -- some NULL ages, some duplicate rows
- orders(oid, user_id, amount) -- some NULL user_ids, duplicate user_ids
"""

from __future__ import annotations

import csv
import os
import random

DATA_DIR = os.path.join(os.path.dirname(os.path.abspath(__file__)), "data")

FIRST = ["alice", "bob", "carol", "dave", "erin", "frank", "grace", "heidi"]


def generate(n_users: int = 1000, n_orders: int = 5000, seed: int = 42,
             out_dir: str = DATA_DIR) -> tuple[str, str]:
    rng = random.Random(seed)
    os.makedirs(out_dir, exist_ok=True)
    users_path = os.path.join(out_dir, "users.csv")
    orders_path = os.path.join(out_dir, "orders.csv")

    with open(users_path, "w", newline="", encoding="utf-8") as fh:
        w = csv.writer(fh)
        w.writerow(["id", "name", "age"])
        for uid in range(1, n_users + 1):
            name = rng.choice(FIRST)
            age = "" if rng.random() < 0.1 else rng.randint(18, 80)  # ~10% NULL
            w.writerow([uid, name, age])
            if rng.random() < 0.02:  # ~2% exact duplicate rows (multiset)
                w.writerow([uid, name, age])

    with open(orders_path, "w", newline="", encoding="utf-8") as fh:
        w = csv.writer(fh)
        w.writerow(["oid", "user_id", "amount"])
        for oid in range(1, n_orders + 1):
            user_id = "" if rng.random() < 0.05 else rng.randint(1, n_users)  # ~5% NULL
            amount = round(rng.uniform(1, 500), 2)
            w.writerow([oid, user_id, amount])

    return users_path, orders_path


if __name__ == "__main__":
    u, o = generate()
    print(f"wrote {u} and {o}")
