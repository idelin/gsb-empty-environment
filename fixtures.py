"""Local fixtures: a fixed snapshot plus a stream of CDC changes."""

from cdc import Change, Snapshot


def sample_snapshot() -> Snapshot:
    """Consistent snapshot of the source at LSN 100."""
    return Snapshot(
        lsn=100,
        tables={
            "accounts": {
                1: {"id": 1, "balance": 500},
                2: {"id": 2, "balance": 300},
            },
            "orders": {
                10: {"id": 10, "status": "open"},
            },
        },
    )


def sample_changes() -> list[Change]:
    """Increments committed after the snapshot (commit_seq > 100)."""
    return [
        Change("txn-101", "accounts", 1,
               {"id": 1, "balance": 500}, {"id": 1, "balance": 450}, 101),
        Change("txn-102", "accounts", 3,
               None, {"id": 3, "balance": 200}, 102),
        Change("txn-103", "orders", 10,
               {"id": 10, "status": "open"}, {"id": 10, "status": "paid"}, 103),
        Change("txn-104", "accounts", 2,
               {"id": 2, "balance": 300}, None, 104),
    ]


def late_change() -> Change:
    """Arrives while a rebuild is in flight (used by demo and tests)."""
    return Change("txn-105", "accounts", 4,
                  None, {"id": 4, "balance": 900}, 105)
