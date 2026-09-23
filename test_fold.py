"""Unit tests for the ordered non-commutative matrix fold (Python 3.11+,
standard library only). Run: python3 -m unittest -v
"""

from __future__ import annotations

import random
import unittest

from matrix import IDENTITY, fold, mul, normalize
from replay import load_fixture, replay
from store import ChangeEvent, GroupStore, StoreError
from treap import Treap

MOD = 1_000_000_007
Q = ((1, 1), (1, 0))          # Fibonacci Q-matrix, invertible
SINGULAR = ((1, 2), (2, 4))   # det = 0, no inverse exists


def event(tx, pk, seq, before=None, after=None, table="items"):
    return ChangeEvent(tx_id=tx, table=table, pk=pk, commit_seq=seq,
                       before=before, after=after)


def row(group, sort_key, matrix):
    return {"group": group, "sort_key": sort_key, "matrix": matrix}


class MatrixTest(unittest.TestCase):
    def test_multiplication_is_noncommutative(self):
        a = ((1, 2), (3, 4))
        b = ((0, 1), (1, 0))
        self.assertNotEqual(mul(a, b, MOD), mul(b, a, MOD))

    def test_identity_is_neutral(self):
        self.assertEqual(mul(Q, IDENTITY, MOD), Q)
        self.assertEqual(mul(IDENTITY, Q, MOD), Q)

    def test_fold_empty_is_identity(self):
        self.assertEqual(fold([], MOD), IDENTITY)

    def test_modulus_applied(self):
        big = ((MOD, MOD + 1), (-1, 2 * MOD))
        self.assertEqual(normalize(big, MOD), ((0, 1), (MOD - 1, 0)))


class TreapTest(unittest.TestCase):
    def test_random_ops_match_naive_fold(self):
        rng = random.Random(20260923)
        tree = Treap(MOD)
        model = {}  # key -> matrix
        for _ in range(600):
            key = (rng.randrange(50), f"pk{rng.randrange(80)}")
            if key in model and rng.random() < 0.5:
                self.assertTrue(tree.delete(key))
                del model[key]
            elif key not in model:
                m = tuple(tuple(rng.randrange(MOD) for _ in range(2))
                          for _ in range(2))
                tree.insert(key, m)
                model[key] = m
            expected = fold([model[k] for k in sorted(model)], MOD)
            self.assertEqual(tree.aggregate(), expected)
            self.assertEqual(tree.size, len(model))

    def test_inorder_is_sorted(self):
        rng = random.Random(7)
        tree = Treap(MOD)
        keys = {(rng.randrange(100), f"k{i}") for i in range(200)}
        for key in keys:
            tree.insert(key, Q)
        self.assertEqual([k for k, _ in tree.inorder()], sorted(keys))

    def test_delete_missing_key_returns_false(self):
        tree = Treap(MOD)
        tree.insert((1, "a"), Q)
        self.assertFalse(tree.delete((2, "a")))
        self.assertEqual(tree.aggregate(), Q)


class GroupStoreTest(unittest.TestCase):
    def test_empty_group_is_identity(self):
        store = GroupStore(MOD)
        self.assertEqual(store.product("nope"), IDENTITY)
        store.apply(event("t1", "x", 1, after=row("g", 1, Q)))
        store.apply(event("t2", "x", 2, before=row("g", 1, Q)))
        self.assertEqual(store.product("g"), IDENTITY)

    def test_singular_middle_delete_without_inverse(self):
        # SINGULAR has determinant 0, so no inverse exists; the tree must
        # still remove it correctly by recomputing aggregates.
        store = GroupStore(MOD)
        store.apply(event("t1", "a", 1, after=row("g", 1, Q)))
        store.apply(event("t2", "b", 2, after=row("g", 2, SINGULAR)))
        store.apply(event("t3", "c", 3, after=row("g", 3, Q)))
        store.apply(event("t4", "b", 4, before=row("g", 2, SINGULAR)))
        self.assertEqual(store.product("g"), mul(Q, Q, MOD))
        self.assertEqual(store.product("g"), store.full_scan_product("g"))

    def test_order_matters_in_group_product(self):
        store = GroupStore(MOD)
        a = ((1, 2), (3, 4))
        b = ((0, 1), (1, 0))
        store.apply(event("t1", "a", 1, after=row("g", 1, a)))
        store.apply(event("t2", "b", 2, after=row("g", 2, b)))
        self.assertEqual(store.product("g"), mul(a, b, MOD))
        # Re-key b before a: the product must change (non-commutative).
        store.apply(event("t3", "b", 3, before=row("g", 2, b),
                          after=row("g", 0, b)))
        self.assertEqual(store.product("g"), mul(b, a, MOD))

    def test_cross_group_migration_is_atomic(self):
        store = GroupStore(MOD)
        store.apply(event("t1", "a", 1, after=row("g1", 1, Q)))
        store.apply(event("t2", "b", 2, after=row("g1", 2, SINGULAR)))
        before_g1 = store.product("g1")
        before_g2 = store.product("g2")
        store.apply(event("t3", "b", 3,
                          before=row("g1", 2, SINGULAR),
                          after=row("g2", 9, SINGULAR)))
        self.assertEqual(store.product("g1"), Q)
        self.assertEqual(store.product("g2"), SINGULAR)
        self.assertNotEqual(before_g1, store.product("g1"))
        self.assertEqual(before_g2, IDENTITY)
        for group in ("g1", "g2"):
            self.assertEqual(store.product(group), store.full_scan_product(group))

    def test_failed_migration_leaves_state_unchanged(self):
        store = GroupStore(MOD)
        store.apply(event("t1", "a", 1, after=row("g1", 1, Q)))
        snapshot = {g: store.product(g) for g in store.groups()}
        with self.assertRaises(StoreError):
            store.apply(event("t2", "ghost", 2,
                              before=row("g1", 5, Q),
                              after=row("g2", 5, Q)))
        self.assertEqual(snapshot, {g: store.product(g) for g in store.groups()})
        with self.assertRaises(StoreError):
            store.apply(event("t3", "a", 3, after=row("g2", 1, Q)))
        self.assertEqual(snapshot, {g: store.product(g) for g in store.groups()})

    def test_random_event_stream_matches_full_scan(self):
        rng = random.Random(99)
        store = GroupStore(97)
        live = {}  # pk -> row image
        for seq in range(1, 401):
            pk = f"pk{rng.randrange(30)}"
            group = f"g{rng.randrange(4)}"
            op = rng.random()
            if pk not in live or op < 0.45:
                after = row(group, rng.randrange(20),
                            [[rng.randrange(97) for _ in range(2)]
                             for _ in range(2)])
                store.apply(event(f"tx{seq}", pk, seq, before=live.get(pk),
                                  after=after))
                live[pk] = after
            else:
                store.apply(event(f"tx{seq}", pk, seq, before=live.pop(pk)))
            for g in store.groups():
                self.assertEqual(store.product(g), store.full_scan_product(g),
                                 f"diverged at seq={seq} group={g}")


class ReplayTest(unittest.TestCase):
    def test_fixture_replays_and_verifies(self):
        modulus, events = load_fixture("fixtures/sample_changes.json")
        self.assertEqual([e.commit_seq for e in events],
                         sorted(e.commit_seq for e in events))
        store, history = replay(modulus, events, verify=True)
        self.assertEqual(len(history), 10)
        # All groups end empty -> identity everywhere.
        for group in store.groups():
            self.assertEqual(store.product(group), IDENTITY)

    def test_out_of_order_fixture_sorted_by_commit_seq(self):
        modulus, events = load_fixture("fixtures/sample_changes.json")
        shuffled = list(reversed(events))
        import json, tempfile, os
        payload = {"modulus": modulus,
                   "events": [vars(e) for e in shuffled]}
        with tempfile.NamedTemporaryFile("w", suffix=".json",
                                         delete=False) as fh:
            json.dump(payload, fh)
            tmp = fh.name
        try:
            _, reloaded = load_fixture(tmp)
        finally:
            os.unlink(tmp)
        self.assertEqual([e.commit_seq for e in reloaded],
                         [e.commit_seq for e in events])


if __name__ == "__main__":
    unittest.main()
