import unittest
from pathlib import Path

from cdcstream.fixtures import FixtureError, group_transactions, load_events

FIXTURE = Path(__file__).resolve().parent.parent / "fixtures" / "changes.json"


class FixtureLoadingTest(unittest.TestCase):
    def test_loads_all_changes_with_tx_metadata(self):
        events = load_events(FIXTURE)
        self.assertEqual(len(events), 8)
        self.assertEqual([ev.event_id for ev in events], list(range(1, 9)))
        # Every event carries its transaction size (self-describing stream).
        self.assertTrue(all(ev.tx_size == 2 for ev in events))
        self.assertEqual({ev.tx_id for ev in events}, {1, 2, 3, 4})

    def test_group_transactions_orders_by_commit_seq(self):
        txs = group_transactions(load_events(FIXTURE))
        self.assertEqual([tx[0].tx_id for tx in txs], [1, 2, 3, 4])
        for tx in txs:
            seqs = [ev.commit_seq for ev in tx]
            self.assertEqual(seqs, sorted(seqs))

    def test_malformed_fixture_rejected(self):
        bad = Path(self._tmp.name)
        bad.write_text('{"changes": [{"tx_id": 1}]}', encoding="utf-8")
        with self.assertRaises(FixtureError):
            load_events(bad)

    def setUp(self):
        import tempfile

        self._tmp = tempfile.NamedTemporaryFile(
            mode="w", suffix=".json", delete=False
        )


if __name__ == "__main__":
    unittest.main()
