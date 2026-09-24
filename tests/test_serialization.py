"""Patch JSON round-trip: reversible patches are serializable artifacts."""
import random
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))
sys.path.insert(0, str(Path(__file__).resolve().parent))

from graphpatch import Patch, diff
from helpers import mutate, random_graph


class TestSerialization(unittest.TestCase):
    def test_json_roundtrip_preserves_behavior(self):
        rng = random.Random(31337)
        for trial in range(20):
            g1 = random_graph(rng, rng.randint(1, 8), rng.randint(0, 12))
            g2, renames = mutate(rng, g1, tag=f"s{trial}")
            p = diff(g1, g2, renames)
            text = p.to_json()
            q = Patch.from_json(text)
            self.assertEqual(q.to_jsonable(), p.to_jsonable())
            self.assertEqual(q.apply(g1), g2, f"trial {trial}")
            # inverse of the decoded patch recovers the base
            self.assertEqual(q.inverse().apply(g2), g1, f"trial {trial}")

    def test_inverse_roundtrip(self):
        rng = random.Random(777)
        g1 = random_graph(rng, 6, 10)
        g2, renames = mutate(rng, g1, tag="inv")
        p = diff(g1, g2, renames)
        inv = p.inverse()
        self.assertEqual(inv.apply(g2), g1)
        self.assertEqual(inv.inverse().apply(g1), g2)


if __name__ == "__main__":
    unittest.main()
