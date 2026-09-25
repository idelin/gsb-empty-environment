from fractions import Fraction

from bvh import BVHIndex


def main() -> None:
    tree = BVHIndex(dimension=2)

    print("empty intersection:", tree.intersects([(0, 0), (1, 1)]))
    print("empty nearest:", tree.nearest((0, 0)))

    tree.insert(
        2,
        [(Fraction(-1), Fraction(1)), (Fraction(-1), Fraction(1))],
    )
    tree.insert(
        1,
        [(Fraction(1), Fraction(2)), (Fraction(0), Fraction(1))],
    )
    tree.insert(
        3,
        [(Fraction(-4), Fraction(-2)), (Fraction(0), Fraction(0))],
    )

    touching = [(Fraction(1), Fraction(1)), (Fraction(0), Fraction(0))]
    print("touching intersection:", tree.intersects(touching))

    point = (Fraction(5, 2), Fraction(1, 2))
    print("nearest k=3:", tree.nearest_k(point, 3))

    tree.move(1, [(10, 11), (10, 11)])
    print("old region after move:", tree.intersects(touching))
    print("new region after move:", tree.intersects([(10, 11), (10, 11)]))

    tree.remove(3)
    print("ids after remove:", tree.ids())
    tree.check_invariants()
    print("invariants: OK")


if __name__ == "__main__":
    main()
