import unittest

from licsel.expr import And, Atom, ExprError, Or, parse, parse_terms


class TestParse(unittest.TestCase):
    def test_precedence_and_binds_tighter(self):
        node = parse("A OR B AND C")
        self.assertIsInstance(node, Or)
        self.assertIsInstance(node.items[1], And)

    def test_parentheses(self):
        node = parse("(A OR B) AND C")
        self.assertIsInstance(node, And)
        self.assertIsInstance(node.items[0], Or)

    def test_with_binds_to_single_atom(self):
        node = parse("GPL-2.0 WITH Classpath-2.0 OR MIT")
        self.assertIsInstance(node, Or)
        with_atom = node.items[0]
        self.assertEqual(with_atom, Atom("GPL-2.0", "Classpath-2.0"))
        self.assertEqual(node.items[1], Atom("MIT"))

    def test_keywords_case_insensitive(self):
        node = parse("a and b or c")
        self.assertIsInstance(node, Or)

    def test_with_on_group_rejected(self):
        with self.assertRaises(ExprError):
            parse("(A OR B) WITH e")

    def test_errors(self):
        for bad in ["", "A AND", "OR A", "(A OR B", "A B", "A OR )"]:
            with self.assertRaises(ExprError, msg=bad):
                parse(bad)


class TestTerms(unittest.TestCase):
    def test_or_gives_alternatives(self):
        terms = parse_terms("MIT OR GPL-3.0")
        self.assertEqual(len(terms), 2)

    def test_and_concatenates(self):
        terms = parse_terms("(A OR B) AND C")
        self.assertEqual(len(terms), 2)
        for term in terms:
            self.assertEqual(len(term), 2)
            self.assertIn(Atom("C"), term)

    def test_with_stays_attached_to_its_atom(self):
        terms = parse_terms("GPL-2.0 WITH Classpath-2.0 OR GPL-2.0")
        self.assertEqual(len(terms), 2)
        flat = {atom for term in terms for atom in term}
        self.assertIn(Atom("GPL-2.0", "Classpath-2.0"), flat)
        self.assertIn(Atom("GPL-2.0", None), flat)
        # the exception must NOT leak onto the plain branch
        plain = [t for t in terms if t == (Atom("GPL-2.0"),)]
        self.assertEqual(len(plain), 1)

    def test_duplicate_terms_collapsed(self):
        terms = parse_terms("MIT OR MIT")
        self.assertEqual(len(terms), 1)


if __name__ == "__main__":
    unittest.main()
