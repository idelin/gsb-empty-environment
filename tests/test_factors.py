import unittest

from factorelim import (
    DomainConflictError,
    Factor,
    InvalidEvidenceError,
    UnknownVariableError,
)

BIN = {"A": (0, 1), "B": (0, 1), "C": (0, 1)}


class TestFactorOps(unittest.TestCase):
    def test_multiply_union_scope(self):
        f = Factor(("A", "B"), BIN, [1, 2, 3, 4])
        g = Factor(("B", "C"), BIN, [5, 6, 7, 8])
        h = f.multiply(g)
        self.assertEqual(h.variables, ("A", "B", "C"))
        # h[a,b,c] = f[a,b] * g[b,c]
        self.assertEqual(h.table, (5.0, 6.0, 14.0, 16.0, 15.0, 18.0, 28.0, 32.0))

    def test_sum_out(self):
        f = Factor(("A", "B"), BIN, [1, 2, 3, 4])
        g = f.sum_out("A")
        self.assertEqual(g.variables, ("B",))
        self.assertEqual(g.table, (4.0, 6.0))
        h = f.sum_out("B")
        self.assertEqual(h.table, (3.0, 7.0))

    def test_sum_out_all_gives_scalar(self):
        f = Factor(("A",), BIN, [3, 5])
        g = f.sum_out("A")
        self.assertEqual(g.variables, ())
        self.assertEqual(g.table, (8.0,))

    def test_restrict(self):
        f = Factor(("A", "B"), BIN, [1, 2, 3, 4])
        r = f.restrict({"A": 1})
        self.assertEqual(r.variables, ("B",))
        self.assertEqual(r.table, (3.0, 4.0))

    def test_restrict_all_gives_scalar(self):
        f = Factor(("A", "B"), BIN, [1, 2, 3, 4])
        r = f.restrict({"A": 0, "B": 1})
        self.assertEqual(r.variables, ())
        self.assertEqual(r.table, (2.0,))

    def test_restrict_ignores_foreign_evidence(self):
        f = Factor(("A",), BIN, [1, 2])
        self.assertIs(f.restrict({"B": 0}), f)

    def test_invalid_evidence_value_rejected(self):
        f = Factor(("A",), BIN, [1, 2])
        with self.assertRaises(InvalidEvidenceError):
            f.restrict({"A": 7})

    def test_zero_probabilities_preserved(self):
        f = Factor(("A", "B"), BIN, [0, 0, 3, 4])
        g = f.sum_out("A")
        self.assertEqual(g.table, (3.0, 4.0))
        n, z = f.normalize()
        self.assertEqual(z, 7.0)
        self.assertEqual(n.table[0], 0.0)

    def test_domain_conflict_on_multiply(self):
        f = Factor(("A",), {"A": (0, 1)}, [1, 2])
        g = Factor(("A",), {"A": ("x", "y", "z")}, [1, 2, 3])
        with self.assertRaises(DomainConflictError):
            f.multiply(g)

    def test_table_size_mismatch_rejected(self):
        with self.assertRaises(ValueError):
            Factor(("A", "B"), BIN, [1, 2, 3])

    def test_unknown_domain_rejected(self):
        with self.assertRaises(UnknownVariableError):
            Factor(("Z",), BIN, [1, 2])

    def test_value_lookup(self):
        f = Factor(("A", "B"), BIN, [1, 2, 3, 4])
        self.assertEqual(f.value({"A": 1, "B": 0}), 3.0)


if __name__ == "__main__":
    unittest.main()
