import random
import unittest

from matrixfold import (
    IDENTITY,
    Change,
    ConflictError,
    FoldEngine,
    FoldGroup,
    SegmentTree,
    Transaction,
    multiply,
    normalize,
)

MOD = 1_000_000_007


def naive_product(items, mod):
    result = IDENTITY
    for m in items:
        result = multiply(result, m, mod)
    return result


def rand_matrix(rng, mod, singular_ratio=0.3):
    if rng.random() < singular_ratio:
        # Singular: second row is a multiple of the first.
        a, b = rng.randrange(mod), rng.randrange(mod)
        k = rng.randrange(mod)
        return (a, b, (k * a) % mod, (k * b) % mod)
    return tuple(rng.randrange(mod) for _ in range(4))


class MatrixTest(unittest.TestCase):
    def test_identity_is_neutral(self):
        m = (3, 5, 7, 11)
        self.assertEqual(multiply(IDENTITY, m, MOD), m)
        self.assertEqual(multiply(m, IDENTITY, MOD), m)

    def test_multiplication_is_non_commutative(self):
        x = (1, 1, 0, 1)
        y = (0, 1, 1, 0)
        self.assertNotEqual(multiply(x, y, MOD), multiply(y, x, MOD))
        self.assertEqual(multiply(x, y, MOD), (1, 1, 1, 0))
        self.assertEqual(multiply(y, x, MOD), (0, 1, 1, 1))

    def test_associativity_matches_textbook(self):
        rng = random.Random(7)
        for _ in range(200):
            a, b, c = (rand_matrix(rng, MOD, 0.0) for _ in range(3))
            left = multiply(multiply(a, b, MOD), c, MOD)
            right = multiply(a, multiply(b, c, MOD), MOD)
            self.assertEqual(left, right)

    def test_singular_matrix_has_no_inverse_but_still_folds(self):
        singular = (1, 2, 2, 4)  # determinant 0
        self.assertEqual((singular[0] * singular[3] - singular[1] * singular[2]) % MOD, 0)
        product = multiply(singular, (1, 0, 1, 1), MOD)
        self.assertEqual(product, (3, 2, 6, 4))

    def test_normalize_reduces_entries(self):
        self.assertEqual(normalize((MOD + 1, -1, 2 * MOD, 5), MOD), (1, MOD - 1, 0, 5))


class SegmentTreeTest(unittest.TestCase):
    def test_empty_tree_is_identity(self):
        self.assertEqual(SegmentTree([], MOD).product(), IDENTITY)

    def test_matches_naive_scan(self):
        rng = random.Random(11)
        for length in (1, 2, 3, 4, 5, 8, 9, 16, 17, 100):
            items = [rand_matrix(rng, MOD) for _ in range(length)]
            self.assertEqual(SegmentTree(items, MOD).product(), naive_product(items, MOD))

    def test_point_updates_match_naive_scan(self):
        rng = random.Random(13)
        items = [rand_matrix(rng, MOD) for _ in range(50)]
        tree = SegmentTree(items, MOD)
        for _ in range(200):
            i = rng.randrange(len(items))
            items[i] = rand_matrix(rng, MOD)
            tree.set(i, items[i])
            self.assertEqual(tree.product(), naive_product(items, MOD))

    def test_small_modulus(self):
        rng = random.Random(17)
        items = [rand_matrix(rng, 2) for _ in range(30)]
        self.assertEqual(SegmentTree(items, 2).product(), naive_product(items, 2))


class FoldGroupTest(unittest.TestCase):
    def test_empty_group_is_identity(self):
        self.assertEqual(FoldGroup(MOD).product(), IDENTITY)

    def test_stable_order_with_equal_sort_keys(self):
        g = FoldGroup(MOD)
        g.insert("b", 1, (0, 1, 1, 0))
        g.insert("a", 1, (1, 1, 0, 1))
        g.insert("c", 0, (2, 0, 0, 2))
        expected = naive_product([(2, 0, 0, 2), (1, 1, 0, 1), (0, 1, 1, 0)], MOD)
        self.assertEqual(g.product(), expected)
        self.assertEqual(g.product(), g.full_scan_product())

    def test_delete_middle_singular_element(self):
        g = FoldGroup(MOD)
        g.insert("x", 1, (1, 1, 0, 1))
        g.insert("y", 2, (1, 2, 2, 4))  # singular middle element
        g.insert("z", 3, (0, 1, 1, 0))
        g.delete("y")
        self.assertEqual(g.product(), multiply((1, 1, 0, 1), (0, 1, 1, 0), MOD))
        self.assertEqual(g.product(), g.full_scan_product())

    def test_update_same_sort_key_uses_point_update(self):
        rng = random.Random(19)
        g = FoldGroup(MOD)
        for i in range(40):
            g.insert(f"k{i:02d}", i, rand_matrix(rng, MOD))
        for i in range(40):
            g.update(f"k{i:02d}", i, rand_matrix(rng, MOD))
            self.assertEqual(g.product(), g.full_scan_product())

    def test_update_changing_sort_key_reorders(self):
        g = FoldGroup(MOD)
        g.insert("a", 1, (1, 1, 0, 1))
        g.insert("b", 2, (0, 1, 1, 0))
        before = g.product()
        g.update("b", 0, (0, 1, 1, 0))  # move b ahead of a
        after = g.product()
        self.assertNotEqual(before, after)
        self.assertEqual(after, multiply((0, 1, 1, 0), (1, 1, 0, 1), MOD))
        self.assertEqual(after, g.full_scan_product())


def make_tx(tx_id, seq, *changes):
    return Transaction(tx_id=tx_id, commit_seq=seq, changes=tuple(changes))


def insert(table, pk, group, key, matrix):
    return Change(table, pk, None, {"group": group, "sort_key": key, "matrix": matrix})


def delete(table, pk, group, key, matrix):
    return Change(table, pk, {"group": group, "sort_key": key, "matrix": matrix}, None)


def update(table, pk, old, new):
    return Change(table, pk, old, new)


class FoldEngineTest(unittest.TestCase):
    def test_insert_update_delete_lifecycle(self):
        engine = FoldEngine(MOD)
        engine.apply_transaction(make_tx("t1", 1, insert("items", "a", "g", 1, [[1, 1], [0, 1]])))
        engine.apply_transaction(make_tx("t2", 2, insert("items", "b", "g", 2, [[0, 1], [1, 0]])))
        self.assertEqual(engine.product("g"), multiply((1, 1, 0, 1), (0, 1, 1, 0), MOD))
        engine.apply_transaction(make_tx(
            "t3", 3,
            update("items", "a",
                   {"group": "g", "sort_key": 1, "matrix": [[1, 1], [0, 1]]},
                   {"group": "g", "sort_key": 1, "matrix": [[2, 0], [0, 2]]}),
        ))
        self.assertEqual(engine.product("g"), multiply((2, 0, 0, 2), (0, 1, 1, 0), MOD))
        engine.apply_transaction(make_tx(
            "t4", 4, delete("items", "b", "g", 2, [[0, 1], [1, 0]])))
        self.assertEqual(engine.product("g"), (2, 0, 0, 2))
        self.assertEqual(engine.product("g"), engine.full_scan_product("g"))

    def test_cross_group_move_is_atomic(self):
        engine = FoldEngine(MOD)
        engine.apply_transaction(make_tx(
            "t1", 1,
            insert("items", "a", "g1", 1, [[1, 2], [2, 4]]),   # singular
            insert("items", "b", "g2", 1, [[3, 0], [0, 3]]),
        ))
        engine.apply_transaction(make_tx(
            "t2", 2,
            update("items", "a",
                   {"group": "g1", "sort_key": 1, "matrix": [[1, 2], [2, 4]]},
                   {"group": "g2", "sort_key": 5, "matrix": [[1, 2], [2, 4]]}),
        ))
        self.assertEqual(engine.product("g1"), IDENTITY)
        self.assertEqual(engine.product("g2"),
                         multiply((3, 0, 0, 3), (1, 2, 2, 4), MOD))
        for name in ("g1", "g2"):
            self.assertEqual(engine.product(name), engine.full_scan_product(name))

    def test_conflicting_transaction_rejected_without_side_effects(self):
        engine = FoldEngine(MOD)
        engine.apply_transaction(make_tx(
            "t1", 1, insert("items", "a", "g", 1, [[1, 1], [0, 1]])))
        before = engine.product("g")
        bad = make_tx(
            "t2", 2,
            insert("items", "b", "g", 2, [[0, 1], [1, 0]]),
            delete("items", "ghost", "g", 9, [[1, 0], [0, 1]]),  # no such row
        )
        with self.assertRaises(ConflictError):
            engine.apply_transaction(bad)
        # Atomic rejection: the valid insert in the same tx is not applied.
        self.assertEqual(engine.product("g"), before)
        self.assertEqual(engine.last_commit_seq, 1)

    def test_commit_seq_must_increase(self):
        engine = FoldEngine(MOD)
        engine.apply_transaction(make_tx("t1", 5))
        with self.assertRaises(ConflictError):
            engine.apply_transaction(make_tx("t2", 5))
        with self.assertRaises(ConflictError):
            engine.apply_transaction(make_tx("t3", 4))

    def test_unknown_group_is_identity(self):
        self.assertEqual(FoldEngine(MOD).product("nope"), IDENTITY)
        self.assertEqual(FoldEngine(MOD).full_scan_product("nope"), IDENTITY)

    def test_randomized_incremental_matches_full_scan(self):
        rng = random.Random(23)
        for mod in (2, 7, MOD):
            engine = FoldEngine(mod)
            live = {}  # (table, pk) -> value dict
            groups = {"g1", "g2", "g3"}
            seq = 0
            for step in range(300):
                seq += 1
                changes = []
                touched = set()
                for _ in range(rng.randrange(1, 4)):
                    candidates = [k for k in live if k not in touched]
                    if candidates and rng.random() < 0.45:
                        key = rng.choice(candidates)
                        touched.add(key)
                        old = live[key]
                        if rng.random() < 0.5:
                            changes.append(Change(key[0], key[1], old, None))
                            del live[key]
                        else:
                            new = {
                                "group": rng.choice(sorted(groups)),
                                "sort_key": rng.randrange(5),
                                "matrix": [list(rand_matrix(rng, mod)[:2]),
                                           list(rand_matrix(rng, mod)[2:])],
                            }
                            changes.append(Change(key[0], key[1], old, new))
                            live[key] = new
                    else:
                        key = ("items", f"k{step}_{rng.randrange(10**6)}")
                        if key in touched:
                            continue
                        touched.add(key)
                        new = {
                            "group": rng.choice(sorted(groups)),
                            "sort_key": rng.randrange(5),
                            "matrix": [list(rand_matrix(rng, mod)[:2]),
                                       list(rand_matrix(rng, mod)[2:])],
                        }
                        changes.append(Change(key[0], key[1], None, new))
                        live[key] = new
                engine.apply_transaction(make_tx(f"tx{seq}", seq, *changes))
                for name in groups:
                    self.assertEqual(
                        engine.product(name),
                        engine.full_scan_product(name),
                        f"mod={mod} step={seq} group={name}",
                    )


if __name__ == "__main__":
    unittest.main()
