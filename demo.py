"""Runnable demo: dynamic insert/remove/move, exact queries, determinism."""

from fractions import Fraction

from bvh import AABB, DynamicBVH


def main():
    tree = DynamicBVH()

    # --- insert a small scene (ints and Fractions mix freely) ---
    tree.insert("player", AABB((0, 0), (2, 2)))
    tree.insert("tree", AABB((5, 0), (6, 3)))
    tree.insert("rock", AABB((Fraction(3, 2), 5), (Fraction(5, 2), 6)))
    tree.insert("lamp", AABB((8, 8), (8, 8)))  # degenerate point box
    print("scene size:", len(tree))

    # --- intersection query (closed boxes: touching counts) ---
    region = AABB((1, 1), (5, 5))
    print("objects intersecting", region, "->", tree.query(region))

    # --- nearest query: exact squared distances, ties broken by id ---
    near = tree.nearest((4, 1), k=3)
    print("3 nearest to (4,1):", near)

    # --- move: the old region must no longer report the object ---
    tree.move("player", AABB((20, 20), (22, 22)))
    print("after move, old region:", tree.query(AABB((0, 0), (2, 2))))
    print("after move, new region:", tree.query(AABB((20, 20), (22, 22))))
    tree.check_invariants()

    # --- remove ---
    tree.remove("rock")
    print("after removing rock:", tree.query(AABB((0, 0), (10, 10))))

    # --- empty tree behaves gracefully ---
    for obj in list(tree):
        tree.remove(obj)
    print("empty query:", tree.query(AABB((0, 0), (9, 9))))
    print("empty nearest:", tree.nearest((0, 0)))

    # --- exact rational nearest, no epsilon anywhere ---
    t2 = DynamicBVH()
    t2.insert("a", AABB((Fraction(1, 3),), (Fraction(1, 3),)))
    t2.insert("b", AABB((Fraction(2, 3),), (Fraction(2, 3),)))
    print("exact nearest to 1/2:", t2.nearest((Fraction(1, 2),), k=2))


if __name__ == "__main__":
    main()
