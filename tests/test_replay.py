"""The operation log replays deterministically, including after JSON."""

from __future__ import annotations

import json
import unittest

from samples.replay import OPS, execute


class ReplayTest(unittest.TestCase):
    def test_replay_is_deterministic(self):
        self.assertEqual(execute(OPS), execute(OPS))

    def test_json_round_trip_replays_identically(self):
        cloned = json.loads(json.dumps(OPS))
        self.assertEqual(execute(OPS), execute(cloned))

    def test_key_transcript_facts(self):
        lines = execute(OPS)
        text = "\n".join(lines)

        # non-monotone temp: pre-calibration inverse merges into 2 components
        first_invert = next(l for l in lines if l.startswith("invert temp"))
        self.assertIn("solutions=2", first_invert)

        # pres platform at 10 over [0, 1] -> one whole-interval solution
        plat = next(l for l in lines if l.startswith("invert pres"))
        self.assertIn("solutions=1", plat)
        self.assertIn("interval(0,1)", plat)

        # calibration revokes old evidence; the re-query issues fresh evidence
        self.assertIn("valid e_bp -> False", text)
        self.assertIn("valid e_bp2 -> True", text)

        # snapshot keeps the pre-calibration value, table moved on
        snap_line = next(l for l in lines if l.startswith("snapshot_evaluate"))
        self.assertIn("value=[1,1.4]", snap_line)
        at_one = [l for l in lines if l.startswith("evaluate temp@1 ")]
        self.assertIn("value=[1,1.4]", at_one[0])    # before calibration
        self.assertIn("value=[1.1,1.3]", at_one[1])  # after calibration

        # delete + restore round-trip: post-restore == post-calibration,
        # while the intermediate post-delete result differs
        temp_inverts = [l for l in lines if l.startswith("invert temp")]
        self.assertEqual(len(temp_inverts), 4)
        self.assertEqual(temp_inverts[3], temp_inverts[1])
        self.assertNotEqual(temp_inverts[3], temp_inverts[2])  # delete mattered


if __name__ == "__main__":
    unittest.main()
