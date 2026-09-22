"""本地合成关系表：NULL(None) 与多重集合（重复行）语义显式。"""

USERS_SCHEMA = ("users.id", "users.name", "users.age", "users.city", "users.email")

USERS = [
    {"users.id": 1, "users.name": "Ada", "users.age": 36,   "users.city": "Paris", "users.email": "ada@x.io"},
    {"users.id": 2, "users.name": "Ben", "users.age": 17,   "users.city": None,    "users.email": None},
    {"users.id": 3, "users.name": "Cid", "users.age": 29,   "users.city": "Paris", "users.email": "cid@x.io"},
    {"users.id": 4, "users.name": "Dee", "users.age": 41,   "users.city": "Lyon",  "users.email": "dee@x.io"},
    {"users.id": 4, "users.name": "Dee", "users.age": 41,   "users.city": "Lyon",  "users.email": "dee@x.io"},  # 完全重复行
    {"users.id": 5, "users.name": "Eli", "users.age": None, "users.city": "Nice",  "users.email": "eli@x.io"},
]

ORDERS_SCHEMA = ("orders.id", "orders.user_id", "orders.amount", "orders.status", "orders.note")

ORDERS = [
    {"orders.id": 10, "orders.user_id": 1,    "orders.amount": 120.0, "orders.status": "paid", "orders.note": "gift"},
    {"orders.id": 11, "orders.user_id": 1,    "orders.amount": None,  "orders.status": "paid", "orders.note": None},
    {"orders.id": 12, "orders.user_id": 2,    "orders.amount": 45.0,  "orders.status": "void", "orders.note": "fraud"},
    {"orders.id": 13, "orders.user_id": 3,    "orders.amount": 45.0,  "orders.status": "paid", "orders.note": "dup"},
    {"orders.id": 13, "orders.user_id": 3,    "orders.amount": 45.0,  "orders.status": "paid", "orders.note": "dup"},  # 完全重复行
    {"orders.id": 14, "orders.user_id": None, "orders.amount": 60.0,  "orders.status": "paid", "orders.note": "anon"},  # NULL 外键
]
