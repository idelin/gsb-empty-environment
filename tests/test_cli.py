import json
import unittest

from evidence_engine.cli import _run


ACCEPTANCE_DOC = {
    "rules": [
        {"premises": ["A"], "conclusion": "B"},
        {"premises": ["B"], "conclusion": "A"},
    ],
    "base": [{"source": "s", "fact": "A"}],
    "commands": [
        {"op": "state"},
        {"op": "revoke", "source": "s"},
        {"op": "state"},
        {"op": "add", "source": "t", "fact": "B"},
        {"op": "supports", "fact": "A"},
    ],
}


class CliTests(unittest.TestCase):
    def test_acceptance_document(self):
        results = _run(ACCEPTANCE_DOC)
        self.assertEqual(results[0]["state"],
                         {"A": [["s"]], "B": [["s"]]})
        self.assertEqual(results[2]["facts"], [])
        self.assertEqual(results[4]["supports"], [["t"]])
        for result in results:
            self.assertFalse(result["interrupted"])

    def test_unknown_op_rejected(self):
        doc = {"commands": [{"op": "bogus"}]}
        with self.assertRaises(ValueError):
            _run(doc)

    def test_interrupted_flag_propagates(self):
        doc = {
            "budget": 5,
            "rules": [{"premises": ["L", "R"], "conclusion": "C"}],
            "base": (
                [{"source": f"l{i}", "fact": "L"} for i in range(5)]
                + [{"source": f"r{i}", "fact": "R"} for i in range(5)]
            ),
            "commands": [{"op": "state"}],
        }
        results = _run(doc)
        self.assertTrue(results[-1]["interrupted"])


if __name__ == "__main__":
    unittest.main(verbosity=2)
