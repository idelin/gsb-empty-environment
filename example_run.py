from __future__ import annotations

from cdc_compare import ComparisonSession, Replica, WatermarkRegressionError
from cdc_report import format_result


def main() -> None:
    events = {
        "west": [
            ("tx-1", "orders", "same-final", None, "created", 1),
            ("tx-2", "orders", "same-final", "created", None, 2),
            ("tx-3", "orders", "same-final", None, "created", 3),
            ("tx-4", "orders", "reordered", None, "created", 4),
            ("tx-5", "orders", "reordered", "created", "shipped", 6),
            ("tx-6", "orders", "value-split", None, "created", 1),
            ("tx-7", "orders", "value-split", "created", "shipped", 7),
        ],
        "east": [
            ("tx-1", "orders", "same-final", None, "created", 1),
            ("tx-3", "orders", "same-final", None, "created", 3),
            ("tx-4", "orders", "reordered", None, "created", 4),
            ("tx-5", "orders", "reordered", "created", "shipped", 5),
            ("tx-6", "orders", "value-split", None, "created", 1),
            ("tx-7", "orders", "value-split", "created", "cancelled", 7),
        ],
    }

    west = Replica("west", 10, tuple(_event(row) for row in events["west"]))
    east = Replica("east", 10, tuple(_event(row) for row in events["east"]))
    session = ComparisonSession()

    print("== compare at common watermark 10 ==")
    print(format_result(session.compare(west, east, 10)))

    advanced_west = Replica(
        "west", 11, tuple(_event(row) for row in events["west"])
    )
    print("\n== request watermark 11 while east remains at 10 ==")
    print(format_result(session.compare(advanced_west, east, 11)))

    print("\n== attempt to roll back to watermark 9 ==")
    try:
        session.compare(west, east, 9)
    except WatermarkRegressionError as exc:
        print(f"blocked: {exc}")


def _event(row: tuple[str, str, str, object, object, int]):
    from cdc_compare import ChangeEvent

    transaction_id, table, primary_key, before, after, seq = row
    return ChangeEvent(
        transaction_id=transaction_id,
        table=table,
        primary_key=primary_key,
        before_value=before,
        after_value=after,
        commit_seq=seq,
    )


if __name__ == "__main__":
    main()
