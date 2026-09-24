from fractions import Fraction

from point_location import Location, PolygonSet, RingNode


def main() -> None:
    geometry = PolygonSet(
        [
            RingNode(
                [(0, 0), (10, 0), (10, 10), (0, 10)],
                [
                    RingNode(
                        [(2, 2), (8, 2), (8, 8), (2, 8), (2, 2)],
                        [RingNode([(4, 4), (6, 4), (6, 6), (4, 6)])],
                    )
                ],
            )
        ],
        node_capacity=4,
    )

    queries = [
        ("outer interior", (1, 1)),
        ("hole interior = exterior", (3, 5)),
        ("island interior", (5, 5)),
        ("outer edge", (0, 2)),
        ("hole vertex", (8, 8)),
        ("rational point on outer edge", (Fraction(1, 2), 0)),
        ("rational hole point", (Fraction(7, 2), 5)),
        ("ray through two equal-height vertices", (Fraction(9, 2), 8)),
    ]

    print("single queries:")
    for label, point in queries:
        result = geometry.locate(point)
        print(f"  {label:42} {point} -> {result.value}")

    batch_points = [point for _, point in queries]
    batch_results = geometry.locate_all(batch_points)
    print("\nbatch query:")
    for point, result in zip(batch_points, batch_results):
        print(f"  {point} -> {result.value}")

    assert batch_results[0] is Location.INSIDE
    assert batch_results[1] is Location.OUTSIDE
    assert batch_results[2] is Location.INSIDE
    assert batch_results[3] is Location.BOUNDARY


if __name__ == "__main__":
    main()
