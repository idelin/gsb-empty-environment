from math import nan

from interval_join import TypedBatch, interval_join, point_join


def describe(value: object) -> str:
    if value is None:
        return "NULL"
    if isinstance(value, float) and value != value:
        return "NaN"
    return repr(value)


point_schema = {"id": "FLOAT", "point": "INT"}
point_rows = [
    {"id": 1.0, "point": 5},
    {"id": 6.0, "point": 6},
    {"id": None, "point": 4},
    {"id": nan, "point": None},
]

interval_schema = {"id": "FLOAT", "start": "INT", "end": "INT"}
interval_rows = [
    {"id": 10.0, "start": 0, "end": 5},
    {"id": 11.0, "start": 5, "end": 8},
    {"id": 12.0, "start": 5, "end": 5},
    {"id": 13.0, "start": None, "end": 8},
]

print("point-join: point 5 belongs to [5,8), not [0,5)")
for match in point_join(
    TypedBatch(point_schema, point_rows),
    TypedBatch(interval_schema, interval_rows),
):
    print(
        f"  point_id={describe(match.point_id):>4} "
        f"interval_id={describe(match.interval_id):>4} "
        f"overlap={match.overlap_length}"
    )

left_schema = {"id": "STRING", "start": "INT", "end": "INT"}
right_schema = {"id": "STRING", "start": "INT", "end": "INT"}
left_rows = [
    {"id": "A", "start": 0, "end": 7},
    {"id": "B", "start": 0, "end": 7},
]
right_rows = [
    {"id": "X", "start": 3, "end": 5},
    {"id": "Y", "start": 7, "end": 9},
    {"id": "Z", "start": 2, "end": 2},
    {"id": "N", "start": None, "end": 4},
]

print("interval-join: [0,7) x [3,5) has overlap length 2")
for match in interval_join(
    TypedBatch(left_schema, left_rows),
    TypedBatch(right_schema, right_rows),
):
    print(
        f"  left_id={match.left_id} right_id={match.right_id} "
        f"overlap={match.overlap_length}"
    )
