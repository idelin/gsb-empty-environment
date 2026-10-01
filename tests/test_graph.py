import unittest

from factorelim import DomainConflictError, FactorGraph, UnknownVariableError


class TestGraphRegistry(unittest.TestCase):
    def test_same_variable_different_domain_rejected(self):
        g = FactorGraph()
        g.add_variable("A", (0, 1))
        with self.assertRaises(DomainConflictError):
            g.add_variable("A", (0, 1, 2))

    def test_same_variable_same_domain_ok(self):
        g = FactorGraph()
        g.add_variable("A", (0, 1))
        g.add_variable("A", (0, 1))  # idempotent, no error
        self.assertEqual(g.domains["A"], (0, 1))

    def test_factor_over_undeclared_variable_rejected(self):
        g = FactorGraph()
        g.add_variable("A", (0, 1))
        with self.assertRaises(UnknownVariableError):
            g.add_factor(("A", "B"), [1, 2, 3, 4])

    def test_empty_domain_rejected(self):
        g = FactorGraph()
        with self.assertRaises(ValueError):
            g.add_variable("A", ())


if __name__ == "__main__":
    unittest.main()
