import unittest

from hyperproj import (
    AddEdge,
    AddGroup,
    AddVertex,
    Collapse,
    Expand,
    HierarchyCycleError,
    IdConflictError,
    InvalidIntervalError,
    MoveGroup,
    MoveVertex,
    Projection,
    UnknownIdError,
)


def base_projection():
    # A, B in group G; C and isolated D at root; multi-edge A->C.
    p = Projection()
    p.apply(
        [
            AddGroup("G"),
            AddVertex("A", "G"),
            AddVertex("B", "G"),
            AddVertex("C"),
            AddVertex("D"),
            AddEdge("e0", "A", "B"),
            AddEdge("e1", "A", "C"),
            AddEdge("e2", "B", "C"),
            AddEdge("e3", "A", "C"),
        ]
    )
    return p


class TestCollapseSemantics(unittest.TestCase):
    def test_internal_edge_never_becomes_self_loop(self):
        p = base_projection()
        p.apply(Collapse("G"))
        # A->B is internal to G: no ("G", "G") cross statistic anywhere
        self.assertNotIn(("G", "G"), p.superedges())
        self.assertEqual(p.internal_edges("G"), ["e0"])
        self.assertEqual(p.edge_location("e0"), ("G", "G"))
        self.assertEqual(p.evidence("G", "G"), [])

    def test_merged_superedge_lists_all_original_edges(self):
        p = base_projection()
        p.apply(Collapse("G"))
        self.assertEqual(p.evidence("G", "C"), ["e1", "e2", "e3"])
        self.assertEqual(p.multiplicity("G", "C"), 3)
        self.assertEqual(p.edge_location("e2"), ("G", "C"))

    def test_isolated_vertex_and_multiedge_preserved(self):
        p = base_projection()
        p.apply(Collapse("G"))
        self.assertIn("D", p.supernodes())
        self.assertEqual(p.supernodes(), {"G", "C", "D"})
        self.assertIn("e1", p.evidence("G", "C"))
        self.assertIn("e3", p.evidence("G", "C"))

    def test_vertex_self_loop_stays_internal(self):
        p = base_projection()
        p.apply(AddEdge("loop", "A", "A"))
        self.assertEqual(p.edge_location("loop"), ("A", "A"))
        p.apply(Collapse("G"))
        self.assertEqual(p.edge_location("loop"), ("G", "G"))
        self.assertNotIn(("G", "G"), p.superedges())
        self.assertEqual(sorted(p.internal_edges("G")), ["e0", "loop"])

    def test_expand_then_collapse_conserves_statistics(self):
        p = base_projection()
        p.apply(Collapse("G"))
        before = (
            p.superedges(),
            p.internal_map(),
            {g: p.member_count(g) for g in p.groups},
            p.visible_map(),
        )
        p.apply(Expand("G"))
        self.assertEqual(p.supernodes(), {"A", "B", "C", "D"})
        p.apply(Collapse("G"))
        after = (
            p.superedges(),
            p.internal_map(),
            {g: p.member_count(g) for g in p.groups},
            p.visible_map(),
        )
        self.assertEqual(before, after)


class TestTimeIntervals(unittest.TestCase):
    def test_half_open_interval(self):
        p = base_projection()
        p.apply(AddEdge("t1", "A", "C", 2, 5))
        p.apply(Collapse("G"))
        self.assertNotIn("t1", p.evidence("G", "C", at=1))
        self.assertIn("t1", p.evidence("G", "C", at=2))
        self.assertIn("t1", p.evidence("G", "C", at=4.5))
        self.assertNotIn("t1", p.evidence("G", "C", at=5))
        self.assertIn("t1", p.evidence("G", "C"))

    def test_open_ended_interval(self):
        p = base_projection()
        p.apply(AddEdge("t2", "C", "A", None, 10))
        self.assertIn("t2", p.evidence("C", "A", at=-1000))
        self.assertNotIn("t2", p.evidence("C", "A", at=10))

    def test_invalid_interval_rejected(self):
        p = base_projection()
        with self.assertRaises(InvalidIntervalError):
            p.apply(AddEdge("bad", "A", "C", 5, 5))
        with self.assertRaises(InvalidIntervalError):
            p.apply(AddEdge("bad", "A", "C", 7, 5))
        self.assertNotIn("bad", p.edges)


class TestMoves(unittest.TestCase):
    def test_move_vertex_atomically_revises_indexes(self):
        p = base_projection()
        p.apply(Collapse("G"))
        p.apply(MoveVertex("A", None))
        self.assertEqual(p.member_count("G"), 1)
        self.assertEqual(p.evidence("A", "C"), ["e1", "e3"])
        self.assertEqual(p.evidence("G", "C"), ["e2"])
        self.assertEqual(p.evidence("A", "G"), ["e0"])
        self.assertEqual(p.internal_edges("G"), [])
        self.assertEqual(p.edge_location("e0"), ("A", "G"))
        self.assertEqual(p.edge_location("e1"), ("A", "C"))
        self.assertEqual(p.edge_location("e2"), ("G", "C"))

    def test_move_vertex_between_groups(self):
        p = base_projection()
        p.apply([AddGroup("H"), Collapse("G"), Collapse("H")])
        p.apply(MoveVertex("A", "H"))
        self.assertEqual(p.member_count("G"), 1)
        self.assertEqual(p.member_count("H"), 1)
        self.assertEqual(p.edge_location("e0"), ("H", "G"))
        self.assertEqual(p.evidence("H", "C"), ["e1", "e3"])
        self.assertEqual(p.internal_edges("G"), [])

    def test_move_group_moves_whole_subtree(self):
        p = base_projection()
        p.apply([AddGroup("H", "G"), MoveVertex("B", "H"), AddGroup("R")])
        p.apply(Collapse("G"))
        self.assertEqual(p.visible_node("B"), "G")
        p.apply(MoveGroup("H", "R"))
        self.assertEqual(p.member_count("G"), 1)
        self.assertEqual(p.member_count("R"), 1)
        self.assertEqual(p.visible_node("B"), "B")
        self.assertEqual(p.edge_location("e0"), ("G", "B"))
        self.assertEqual(p.edge_location("e2"), ("B", "C"))

    def test_nested_counts_and_hiding(self):
        p = base_projection()
        p.apply([AddGroup("H", "G"), MoveVertex("B", "H")])
        self.assertEqual(p.member_count("H"), 1)
        self.assertEqual(p.member_count("G"), 2)
        p.apply(Collapse("G"))
        self.assertEqual(p.visible_node("B"), "G")
        p.apply([Expand("G"), Collapse("H")])
        self.assertEqual(p.visible_node("B"), "H")
        self.assertEqual(p.visible_node("A"), "A")
        self.assertEqual(p.edge_location("e0"), ("A", "H"))


class TestBatchRejection(unittest.TestCase):
    def test_cycle_rejects_whole_batch(self):
        p = base_projection()
        p.apply(AddGroup("H", "G"))
        with self.assertRaises(HierarchyCycleError):
            p.apply([AddGroup("K", "H"), MoveGroup("G", "K")])
        self.assertNotIn("K", p.groups)
        self.assertIsNone(p.groups["G"])
        self.assertEqual(p.member_count("G"), 2)

    def test_direct_and_self_cycle(self):
        p = base_projection()
        with self.assertRaises(HierarchyCycleError):
            p.apply(MoveGroup("G", "G"))
        p.apply(AddGroup("H", "G"))
        with self.assertRaises(HierarchyCycleError):
            p.apply(MoveGroup("G", "H"))

    def test_id_conflict_rejects_whole_batch(self):
        p = base_projection()
        with self.assertRaises(IdConflictError):
            p.apply([AddVertex("Z"), AddGroup("Z")])
        self.assertNotIn("Z", p.vertices)
        self.assertNotIn("Z", p.groups)
        with self.assertRaises(IdConflictError):
            p.apply(AddGroup("A"))
        with self.assertRaises(IdConflictError):
            p.apply(AddVertex("G"))
        with self.assertRaises(IdConflictError):
            p.apply(AddEdge("e0", "A", "C"))

    def test_unknown_ids_rejected(self):
        p = base_projection()
        with self.assertRaises(UnknownIdError):
            p.apply(MoveVertex("nope", None))
        with self.assertRaises(UnknownIdError):
            p.apply(AddEdge("x", "A", "nope"))
        with self.assertRaises(UnknownIdError):
            p.apply(Collapse("nope"))
        with self.assertRaises(UnknownIdError):
            p.apply(AddVertex("Q", "nope"))
        with self.assertRaises(UnknownIdError):
            p.apply(MoveGroup("G", "nope"))


class TestHistoryAndViews(unittest.TestCase):
    def test_undo_restores_previous_state(self):
        p = base_projection()
        p.apply(Collapse("G"))
        before = (p.superedges(), p.internal_map(), p.visible_map())
        p.apply(MoveVertex("A", None))
        self.assertNotEqual(p.visible_map(), before[2])
        self.assertTrue(p.undo())
        self.assertEqual((p.superedges(), p.internal_map(), p.visible_map()), before)

    def test_undo_empty_history(self):
        p = Projection()
        self.assertFalse(p.undo())

    def test_named_snapshots(self):
        p = base_projection()
        p.save_snapshot("flat")
        p.apply(Collapse("G"))
        p.save_snapshot("collapsed")
        self.assertEqual(p.snapshot_tags(), ["collapsed", "flat"])
        p.restore("flat")
        self.assertEqual(p.supernodes(), {"A", "B", "C", "D"})
        p.restore("collapsed")
        self.assertEqual(p.supernodes(), {"G", "C", "D"})
        p.undo()
        self.assertEqual(p.supernodes(), {"A", "B", "C", "D"})

    def test_forked_views_are_independent(self):
        p = base_projection()
        view = p.fork()
        view.apply(Collapse("G"))
        self.assertEqual(p.supernodes(), {"A", "B", "C", "D"})
        self.assertEqual(p.evidence("A", "C"), ["e1", "e3"])
        p.apply(MoveVertex("A", None))
        self.assertEqual(view.member_count("G"), 2)
        self.assertEqual(view.evidence("G", "C"), ["e1", "e2", "e3"])


if __name__ == "__main__":
    unittest.main()
