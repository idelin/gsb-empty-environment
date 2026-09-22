import itertools
import random
import unittest

from vsets import Version, parse_spec

RELEASE_GRID = [
    Version.parse(text)
    for text in (
        "0.0.0", "0.0.1", "1.0.0", "1.0.1", "1.1.0",
        "2.0.0", "2.0.1", "2.1.0", "3.0.0", "4.0.0",
    )
]
PRE_GRID = {
    "alpha": [Version.parse(text) for text in ("1.0.0-alpha.1", "1.0.0-alpha.2",
                                                "2.0.0-alpha.1", "3.0.0-alpha.1")],
    "beta": [Version.parse(text) for text in ("2.0.0-beta.1", "2.0.0-beta.2")],
}


def membership(version_set):
    members = {v for v in RELEASE_GRID if v in version_set}
    for channel, values in PRE_GRID.items():
        for value in values:
            if value in version_set:
                members.add(value)
    return members


class SpecSemanticsFuzzTest(unittest.TestCase):
    def test_hand_built_boundary_sets(self):
        cases = {
            "[1.0.0,3.0.0)": {"1.0.0", "1.1.0", "2.9.9"},
            "[*,2.0.0)": {"0.0.0", "1.9.9"},
            "[3.0.0,*)": {"3.0.0", "4.0.0"},
            "[1.0.0,2.0.0) | [2.0.0,3.0.0)": {"1.0.0", "2.0.0", "2.9.9"},
            "!=2.0.0": {"0.0.0", "1.9.9", "2.0.1", "3.0.0"},
            ">2.0.0": {"2.0.1", "2.1.0", "3.0.0", "4.0.0"},
            "<=2.0.0": {"0.0.0", "1.0.0", "2.0.0"},
        }
        for spec, expected in cases.items():
            parsed = parse_spec(spec)
            for text in expected:
                with self.subTest(spec=spec, text=text):
                    self.assertIn(Version.parse(text), parsed)

    def test_prerelease_channel_grid(self):
        alpha = parse_spec("[1.0.0,3.0.0)@alpha")
        self.assertEqual(
            {str(v) for v in membership(alpha)},
            {"1.0.0-alpha.1", "1.0.0-alpha.2", "2.0.0-alpha.1"},
        )
        beta = parse_spec("[2.0.0,3.0.0)@beta")
        self.assertEqual({str(v) for v in membership(beta)}, {"2.0.0-beta.1", "2.0.0-beta.2"})
        self.assertFalse(membership(alpha & beta))
        self.assertEqual(
            membership(alpha | beta),
            membership(alpha) | membership(beta),
        )

    def test_random_expressions_match_grid_membership(self):
        rng = random.Random(20260921)
        atoms = [
            "[0.0.1,2.0.1)",
            "[*,2.0.0)",
            "[2.0.0,*)",
            "[1.0.0,3.0.0)",
            "!=1.1.0",
            ">=1.0.1",
            "<2.1.0",
            "[1.0.1,2.0.1)@alpha",
        ]
        ops = ("&", "-", "|")
        for _ in range(400):
            left = rng.choice(atoms)
            right = rng.choice(atoms)
            op = rng.choice(ops)
            expression = f"{left} {op} {right}"
            parsed = parse_spec(expression)
            lv = membership(parse_spec(left))
            rv = membership(parse_spec(right))
            if op == "&":
                expected = lv & rv
            elif op == "-":
                expected = lv - rv
            else:
                expected = lv | rv
            self.assertEqual(
                membership(parsed),
                expected,
                msg=expression,
            )

    def test_counterexample_is_smallest_grid_witness(self):
        sets = [
            parse_spec(spec)
            for spec in ("[1.0.0,3.0.0)", "[*,2.0.0)", "[2.0.0,*)", "[1.0.0,2.0.0) | [3.0.0,*)")
        ]
        all_grid = set(RELEASE_GRID) | set(itertools.chain.from_iterable(PRE_GRID.values()))
        for left, right in itertools.product(sets, repeat=2):
            difference = {v for v in all_grid if v in left and v not in right}
            witness = left.counterexample(right)
            if difference:
                self.assertEqual(witness, min(difference), msg=(left.serialize(), right.serialize()))
            else:
                self.assertIsNone(witness)

    def test_serialization_roundtrips_semantics_on_grid(self):
        specs = ["[1.0.0,3.0.0)", "[2.0.0,*)", "!=2.0.0", "[1.0.0,2.0.0)@alpha"]
        for spec in specs:
            once = parse_spec(spec)
            twice = parse_spec(once.serialize())
            self.assertEqual(once.serialize(), parse_spec(once.serialize()).serialize())
            self.assertEqual(membership(once), membership(twice), msg=spec)


if __name__ == "__main__":
    unittest.main()
