"""本地夹具: 表、主键池与确定性行值生成。不连接任何消息平台。"""
from __future__ import annotations

import random

from .events import freeze_row

TABLES = {
    "users": tuple(f"u{i}" for i in range(1, 7)),
    "orders": tuple(f"o{i}" for i in range(1, 7)),
}

USER_TIERS = ("free", "pro", "vip")
ORDER_STATUS = ("new", "paid", "shipped")


def random_row(rng: random.Random, table: str):
    """从夹具值池中确定性地生成一行(由 rng 驱动)。"""
    if table == "users":
        return freeze_row({
            "balance": rng.randint(0, 999),
            "tier": rng.choice(USER_TIERS),
        })
    if table == "orders":
        return freeze_row({
            "amount": rng.randint(1, 499),
            "status": rng.choice(ORDER_STATUS),
        })
    raise KeyError(f"未知表: {table!r}")
