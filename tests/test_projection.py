"""Projection behaviour: placeholders, completeness, errors, no leaks."""
import unittest

from secexplain.engine import Engine
from secexplain.model import PUBLIC, SECRET, Fact, Rule
from secexplain.projection import (PUBLIC_ERROR, ExplanationError,
                                   PFactNode, PPlaceholder, project)
from secexplain.sampledata import build_world


def derive(facts, rules):
    return Engine(rules).derive(facts)


class PlaceholderTest(unittest.TestCase):
    def test_multiple_hidden_premises_collapse_to_one_placeholder(self):
        facts = [
            Fact("F-A", 1, "a", "true", PUBLIC),
            Fact("F-H1", 1, "h1", "x", SECRET),
            Fact("F-H2", 1, "h2", "y", SECRET),
        ]
        rules = [Rule("R-V", 1, ("a", "h1", "h2"), "c", PUBLIC, PUBLIC)]
        expl = project(derive(facts, rules), "c", PUBLIC)
        children = expl.root.support.children
        placeholders = [c for c in children if isinstance(c, PPlaceholder)]
        self.assertEqual(len(placeholders), 1)
        self.assertEqual(expl.summary["placeholders"], 1)
        self.assertFalse(expl.complete)

    def test_visible_conclusion_hidden_premise_gives_restricted_placeholder(self):
        world = build_world(chain_len=3, extra_hidden=2)
        expl = project(derive(world.facts, world.rules), "approved", PUBLIC)
        self.assertEqual(expl.root.key, "approved")
        children = expl.root.support.children
        visible = [c for c in children if isinstance(c, PFactNode)]
        hidden = [c for c in children if isinstance(c, PPlaceholder)]
        self.assertEqual([c.key for c in visible],
                         ["income_verified", "id_verified"])
        self.assertEqual(len(hidden), 1)
        self.assertEqual(hidden[0].token, "[RESTRICTED]")
        self.assertFalse(expl.complete)

    def test_hidden_chain_length_not_leaked_via_depth(self):
        depths = []
        for chain_len in (1, 5):
            world = build_world(chain_len=chain_len)
            expl = project(derive(world.facts, world.rules), "approved", PUBLIC)
            depths.append(expl.summary["max_depth"])
        self.assertEqual(depths[0], depths[1])

    def test_visible_fact_with_hidden_rule_support(self):
        facts = [Fact("F-A", 1, "a", "true", PUBLIC)]
        rules = [Rule("R-H", 1, ("a",), "b", PUBLIC, SECRET)]
        expl = project(derive(facts, rules), "b", PUBLIC)
        self.assertIsInstance(expl.root.support, PPlaceholder)
        self.assertFalse(expl.complete)

    def test_fully_visible_derivation_is_complete(self):
        facts = [Fact("F-A", 1, "a", "true", PUBLIC)]
        rules = [Rule("R-V", 3, ("a",), "b", PUBLIC, PUBLIC)]
        expl = project(derive(facts, rules), "b", PUBLIC)
        self.assertTrue(expl.complete)
        self.assertEqual(expl.summary["placeholders"], 0)
        self.assertEqual(expl.root.support.rule_id, "R-V")
        self.assertEqual(expl.root.support.rule_version, 3)


class ErrorTest(unittest.TestCase):
    def test_missing_and_hidden_conclusions_give_identical_errors(self):
        world = build_world(chain_len=2, extra_hidden=1)
        derivation = derive(world.facts, world.rules)
        with self.assertRaises(ExplanationError) as missing:
            project(derivation, "no_such_key", PUBLIC)
        with self.assertRaises(ExplanationError) as hidden:
            project(derivation, "risk_cleared", PUBLIC)
        self.assertEqual(str(missing.exception), PUBLIC_ERROR)
        self.assertEqual(str(hidden.exception), PUBLIC_ERROR)


class NoLeakTest(unittest.TestCase):
    FORBIDDEN = ("risk_cleared", "screen_0", "F-SCREEN", "F-NOTE",
                 "R-CHAIN", "note_0", "redacted")

    def test_public_output_contains_no_hidden_details(self):
        world = build_world(chain_len=4, extra_hidden=2)
        expl = project(derive(world.facts, world.rules), "approved", PUBLIC)
        blob = expl.to_json()
        for token in self.FORBIDDEN:
            self.assertNotIn(token, blob)


if __name__ == "__main__":
    unittest.main()
