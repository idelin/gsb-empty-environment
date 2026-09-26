import unittest

from xsplice import GainConstraintError, Segment, Session, render, render_range

SRC = {"s": [float(i) for i in range(64)], "u": [float(100 + i) for i in range(64)]}


def make_session():
    return Session(SRC, 48000)


class RevisionTest(unittest.TestCase):
    def test_upsert_reports_output_and_source_spans(self):
        session = make_session()
        rev = session.upsert(Segment("a", "s", 0, 16, 0))
        self.assertEqual(rev.changed_output, ((0, 16),))
        self.assertEqual(rev.changed_source, (("s", 0, 16),))

    def test_edit_merges_old_and_new_spans(self):
        session = make_session()
        session.upsert(Segment("a", "s", 0, 16, 0))
        rev = session.upsert(Segment("a", "s", 0, 16, 8))  # moved to [8, 24)
        self.assertEqual(rev.changed_output, ((0, 24),))
        self.assertEqual(rev.changed_source, (("s", 0, 16),))

    def test_disjoint_move_reports_two_spans(self):
        session = make_session()
        session.upsert(Segment("a", "s", 0, 8, 0))
        rev = session.upsert(Segment("a", "u", 4, 12, 40))
        self.assertEqual(rev.changed_output, ((0, 8), (40, 48)))
        self.assertEqual(rev.changed_source, (("s", 0, 8), ("u", 4, 12)))

    def test_remove_reports_old_span(self):
        session = make_session()
        session.upsert(Segment("a", "s", 0, 16, 0))
        rev = session.remove("a")
        self.assertEqual(rev.changed_output, ((0, 16),))
        self.assertEqual(rev.changed_source, (("s", 0, 16),))
        self.assertEqual(session.plan.total, 0)

    def test_empty_segment_edit_reports_nothing(self):
        session = make_session()
        rev = session.upsert(Segment("e", "s", 3, 3, 10))
        self.assertEqual(rev.changed_output, ())
        self.assertEqual(rev.changed_source, ())


class IncrementalRenderTest(unittest.TestCase):
    def test_rerender_only_changed_intervals(self):
        session = make_session()
        session.upsert(Segment("a", "s", 0, 16, 0, fade_out=4))
        session.upsert(Segment("b", "u", 0, 16, 12, fade_in=4))
        old = render(session.plan)
        # Edit only segment "a": read a different source range, fades stay
        # aligned with "b" so the global gain constraint still holds.
        rev = session.upsert(Segment("a", "s", 8, 24, 0, fade_out=4))
        new = render(session.plan)
        (lo, hi), = rev.changed_output
        # Re-rendering just the dirty interval matches the full re-render.
        self.assertEqual(render_range(session.plan, lo, hi), new[lo:hi])
        # Everything outside the dirty interval is untouched.
        self.assertEqual(old[:lo], new[:lo])
        self.assertEqual(old[hi:], new[hi:])

    def test_failed_edit_leaves_session_untouched(self):
        session = make_session()
        session.upsert(Segment("a", "s", 0, 16, 0))
        before = session.plan
        with self.assertRaises(GainConstraintError):
            session.upsert(Segment("b", "u", 0, 16, 4))  # full-gain overlap
        self.assertIs(session.plan, before)

    def test_remove_unknown_segment_raises(self):
        session = make_session()
        with self.assertRaises(KeyError):
            session.remove("nope")


if __name__ == "__main__":
    unittest.main()
