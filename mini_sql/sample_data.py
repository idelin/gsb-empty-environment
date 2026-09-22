from .database import Database, Relation


def sample_database():
    customers = Relation(
        ("id", "name"),
        (
            (1, "Ada"),
            (2, "Bo"),
            (3, "Cy"),
            (2, "Bo"),
        ),
    )
    payments = Relation(
        ("id", "customer_id", "amount"),
        (
            (10, 1, 100),
            (11, 1, 50),
            (12, 2, None),
        ),
    )
    boxes = Relation(
        ("box_id", "id", "customer_id"),
        (
            (20, 1, 1),
            (21, 2, 2),
        ),
    )
    return Database({"customers": customers, "payments": payments, "boxes": boxes})
