import ast
import inspect
import unittest
from pathlib import Path

import cdcstream.oracle as oracle_module
from cdcstream import ChangeEvent, full_state, load_events

FIXTURE = Path(__file__).resolve().parent.parent / "fixtures" / "changes.json"


class OracleTest(unittest.TestCase):
    def test_full_state_on_fixture(self):
        expected = {
            ("accounts", "alice"): {"balance": 120},
            ("orders", "o1"): {"status": "shipped"},
        }
        self.assertEqual(full_state(load_events(FIXTURE)), expected)

    def test_last_commit_seq_wins_and_delete_removes(self):
        events = [
            ChangeEvent(1, 1, "t", "k", None, {"v": 1}, commit_seq=1),
            ChangeEvent(2, 2, "t", "k", {"v": 1}, {"v": 2}, commit_seq=2),
            ChangeEvent(3, 3, "t", "k", {"v": 2}, None, commit_seq=3),
        ]
        self.assertEqual(full_state(events), {})
        self.assertEqual(full_state(events[:2]), {("t", "k"): {"v": 2}})

    def test_oracle_does_not_use_the_incremental_algorithm(self):
        # The oracle must be independent: no import of the processor module
        # and no reference to the processor classes anywhere in its code.
        tree = ast.parse(inspect.getsource(oracle_module))
        imported = set()
        for node in ast.walk(tree):
            if isinstance(node, ast.Import):
                imported.update(alias.name for alias in node.names)
            elif isinstance(node, ast.ImportFrom):
                imported.add(node.module or "")
        self.assertFalse(any("processor" in mod for mod in imported))

        names = {
            node.id for node in ast.walk(tree) if isinstance(node, ast.Name)
        }
        self.assertFalse(names & {"IncrementalProcessor", "ArrivalOrderProcessor"})


if __name__ == "__main__":
    unittest.main()
