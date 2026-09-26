import unittest

from logmerge import ConflictError, Event, WallClock, dedupe


def ev(eid, payload="x"):
    return Event(eid, "s", WallClock(1, "s"), payload)


class TestEvents(unittest.TestCase):
    def test_identical_duplicates_collapse(self):
        events = dedupe([ev("a"), ev("b"), ev("a")])
        self.assertEqual([e.id for e in events], ["a", "b"])

    def test_conflicting_same_id_rejected(self):
        with self.assertRaises(ConflictError):
            dedupe([ev("a", "one"), ev("a", "two")])

    def test_conflicting_causes_rejected(self):
        e1 = Event("a", "s", WallClock(1, "s"), "x", causes=("b",))
        with self.assertRaises(ConflictError):
            dedupe([ev("a"), e1])


if __name__ == "__main__":
    unittest.main()
