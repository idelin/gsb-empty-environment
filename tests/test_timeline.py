import unittest
from fractions import Fraction

from timestack import Conflict, Instant, OffsetTable, Timeline, WallClock


def spans(tl):
    return [(p.start.offset, p.end.offset, p.value, p.sources, p.conflict)
            for p in tl.pieces()]


class TestPriorityStacking(unittest.TestCase):
    def test_high_priority_covers_only_middle(self):
        tl = Timeline()
        tl.add("low", 0, 10, "L", priority=0)
        tl.add("high", 3, 7, "H", priority=10)
        self.assertEqual(
            spans(tl),
            [
                (0, 3, "L", frozenset({"low"}), None),
                (3, 7, "H", frozenset({"high"}), None),
                (7, 10, "L", frozenset({"low"}), None),
            ],
        )

    def test_retraction_restores_underlying(self):
        tl = Timeline()
        tl.add("low", 0, 10, "L", priority=0)
        token = tl.add("high", 3, 7, "H", priority=10)
        tl.remove(token)
        self.assertEqual(spans(tl), [(0, 10, "L", frozenset({"low"}), None)])

    def test_partial_overlap_edges(self):
        tl = Timeline()
        tl.add("a", 0, 5, "A", priority=1)
        tl.add("b", 4, 9, "B", priority=2)
        self.assertEqual(
            [ (p.start.offset, p.end.offset, p.value) for p in tl.pieces() ],
            [(0, 4, "A"), (4, 9, "B")],
        )

    def test_gaps_have_no_pieces(self):
        tl = Timeline()
        tl.add("a", 0, 2, "A")
        tl.add("b", 5, 7, "B")
        self.assertIsNone(tl.value_at(3))
        self.assertEqual(tl.value_at(1), ("A", frozenset({"a"})))


class TestConflicts(unittest.TestCase):
    def test_same_priority_conflict_is_explicit(self):
        tl = Timeline()
        tl.add("s1", 2, 6, "X", priority=5)
        tl.add("s2", 4, 8, "Y", priority=5)
        pieces = tl.pieces()
        conflict = [p for p in pieces if p.is_conflict]
        self.assertEqual(len(conflict), 1)
        c = conflict[0]
        self.assertEqual((c.start.offset, c.end.offset), (4, 6))
        self.assertEqual(c.conflict.values, frozenset({"X", "Y"}))
        self.assertEqual(c.sources, frozenset({"s1", "s2"}))
        # Non-conflicted outer parts keep their own value and source.
        self.assertEqual(tl.value_at(3), ("X", frozenset({"s1"})))
        self.assertEqual(tl.value_at(7), ("Y", frozenset({"s2"})))

    def test_conflict_value_at_returns_conflict_object(self):
        tl = Timeline()
        tl.add("s1", 0, 4, "X", priority=1)
        tl.add("s2", 0, 4, "Y", priority=1)
        value, sources = tl.value_at(2)
        self.assertIsInstance(value, Conflict)
        self.assertEqual(sources, frozenset({"s1", "s2"}))

    def test_same_priority_same_value_merges_sources(self):
        tl = Timeline()
        tl.add("s1", 0, 4, "X", priority=1)
        tl.add("s2", 0, 4, "X", priority=1)
        self.assertEqual(spans(tl), [(0, 4, "X", frozenset({"s1", "s2"}), None)])

    def test_higher_priority_hides_conflict_below(self):
        tl = Timeline()
        tl.add("s1", 0, 4, "X", priority=1)
        tl.add("s2", 0, 4, "Y", priority=1)
        tl.add("boss", 1, 3, "Z", priority=9)
        self.assertEqual(
            [(p.start.offset, p.end.offset, p.value, p.is_conflict) for p in tl.pieces()],
            [(0, 1, None, True), (1, 3, "Z", False), (3, 4, None, True)],
        )


class TestProvenance(unittest.TestCase):
    def test_adjacent_same_value_different_sources_not_merged(self):
        tl = Timeline()
        tl.add("src-a", 0, 5, "V", priority=0)
        tl.add("src-b", 5, 10, "V", priority=0)
        pieces = tl.pieces()
        self.assertEqual(len(pieces), 2)
        self.assertEqual(pieces[0].sources, frozenset({"src-a"}))
        self.assertEqual(pieces[1].sources, frozenset({"src-b"}))

    def test_adjacent_same_value_same_sources_merged(self):
        tl = Timeline()
        tl.add("src-a", 0, 5, "V", priority=0)
        tl.add("src-a", 5, 10, "V", priority=0)
        self.assertEqual(spans(tl), [(0, 10, "V", frozenset({"src-a"}), None)])

    def test_provenance_survives_local_update_elsewhere(self):
        tl = Timeline()
        tl.add("src-a", 0, 5, "V", priority=0)
        tl.add("src-b", 5, 10, "V", priority=0)
        token = tl.add("tmp", 20, 30, "T", priority=0)
        tl.remove(token)
        pieces = tl.pieces()
        self.assertEqual(len(pieces), 2)
        self.assertEqual(pieces[0].sources, frozenset({"src-a"}))
        self.assertEqual(pieces[1].sources, frozenset({"src-b"}))


class TestLocalUpdates(unittest.TestCase):
    """Incremental cache must always match a from-scratch rebuild."""

    def _rebuild(self, ops):
        tl = Timeline()
        tokens = {}
        for op in ops:
            if op[0] == "add":
                _, key, src, a, b, v, p = op
                tokens[key] = tl.add(src, a, b, v, priority=p)
            else:
                _, key = op
                tl.remove(tokens[key])
        return spans(tl)

    def test_incremental_matches_rebuild(self):
        adds = [
            ("add", "k1", "low", 0, 10, "L", 0),
            ("add", "k2", "high", 3, 7, "H", 10),
            ("add", "k3", "mid", 2, 5, "M", 5),
            ("add", "k4", "c1", 6, 9, "X", 5),
            ("add", "k5", "c2", 6, 9, "Y", 5),
            ("add", "k6", "adj", 10, 15, "L", 0),
        ]
        removals = [("rem", "k2"), ("rem", "k3"), ("rem", "k5"), ("rem", "k1")]
        tl = Timeline()
        tokens = {}
        history = []
        for op in adds + removals:
            if op[0] == "add":
                _, key, src, a, b, v, p = op
                tokens[key] = tl.add(src, a, b, v, priority=p)
            else:
                tl.remove(tokens[op[1]])
            history.append(op)
            # After every single mutation, compare against a fresh rebuild.
            self.assertEqual(spans(tl), self._rebuild(history), msg=f"after {op}")

    def test_update_outside_cached_region_keeps_cache(self):
        tl = Timeline()
        tl.add("a", 0, 5, "A", priority=0)
        self.assertEqual(len(tl.pieces()), 1)
        cached = tl._resolved  # introspection: cache object identity
        tl.add("b", 100, 110, "B", priority=0)
        tl.pieces()
        self.assertIs(tl._resolved[0], cached[0])  # left part untouched

    def test_remove_unknown_token_raises(self):
        tl = Timeline()
        with self.assertRaises(KeyError):
            tl.remove(999)

    def test_empty_segment_rejected(self):
        tl = Timeline()
        with self.assertRaises(ValueError):
            tl.add("a", 5, 5, "V")
        with self.assertRaises(ValueError):
            tl.add("a", 6, 5, "V")


class TestWallClockIntegration(unittest.TestCase):
    def test_wallclock_inputs_via_offset_table(self):
        table = OffsetTable.synthetic(base_offset=100)
        tl = Timeline(offset_table=table)
        tl.add("w", WallClock(0), WallClock(10), "V", priority=0)
        piece = tl.pieces()[0]
        self.assertEqual(piece.start, Instant(100))
        self.assertEqual(piece.end, Instant(110))

    def test_wallclock_without_table_rejected(self):
        tl = Timeline()
        with self.assertRaises(TypeError):
            tl.add("w", WallClock(0), WallClock(10), "V")

    def test_duration_is_not_a_point(self):
        from timestack import Duration
        tl = Timeline()
        with self.assertRaises(TypeError):
            tl.add("d", Duration(0), Duration(5), "V")

    def test_fractional_exact_boundaries(self):
        tl = Timeline()
        tl.add("a", Fraction(1, 3), Fraction(1, 2), "A", priority=0)
        tl.add("b", Fraction(1, 2), Fraction(2, 3), "B", priority=1)
        pieces = tl.pieces()
        self.assertEqual(pieces[0].end.offset, Fraction(1, 2))
        self.assertEqual(pieces[1].start.offset, Fraction(1, 2))
        self.assertEqual(pieces[0].duration.seconds, Fraction(1, 6))


if __name__ == "__main__":
    unittest.main()
