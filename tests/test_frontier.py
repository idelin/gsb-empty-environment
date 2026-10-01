import tempfile
import unittest

from modelcheck.frontier import LayeredFrontier


class TestLayeredFrontier(unittest.TestCase):
    def test_write_read_roundtrip(self):
        with tempfile.TemporaryDirectory() as tmp:
            frontier = LayeredFrontier(tmp)
            entries = [(bytes([i]) * 4, bytes([i + 1])) for i in range(10)]
            frontier.write_layer(3, entries)
            self.assertTrue(frontier.has_layer(3))
            self.assertFalse(frontier.has_layer(4))
            self.assertEqual(frontier.read_layer(3), entries)

    def test_empty_layer(self):
        with tempfile.TemporaryDirectory() as tmp:
            frontier = LayeredFrontier(tmp)
            frontier.write_layer(0, [])
            self.assertEqual(frontier.read_layer(0), [])

    def test_order_preserved_across_layers(self):
        with tempfile.TemporaryDirectory() as tmp:
            frontier = LayeredFrontier(tmp)
            per_layer = [[(f"k{layer}-{i}".encode(), b"") for i in range(layer + 1)]
                         for layer in range(5)]
            for layer, entries in enumerate(per_layer):
                frontier.write_layer(layer, entries)
            for layer, entries in enumerate(per_layer):
                self.assertEqual(frontier.read_layer(layer), entries)


if __name__ == "__main__":
    unittest.main()
