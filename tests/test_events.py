import hashlib
import unittest

from buildstate.events import EventError, parse_event


def digest(text):
    return hashlib.sha256(text.encode()).hexdigest()


class EventParseTests(unittest.TestCase):
    def test_started(self):
        event = parse_event(
            {"event_id": "e1", "node": "n", "attempt": 1,
             "kind": "started", "timestamp": "t"}
        )
        self.assertEqual(event.event_id, "e1")

    def test_artifacts_need_sha256(self):
        base = {"event_id": "e1", "node": "n", "attempt": 1,
                "kind": "artifacts_written", "timestamp": "t"}
        with self.assertRaises(EventError):
            parse_event({**base, "artifacts": []})
        with self.assertRaises(EventError):
            parse_event({**base, "artifacts": [{"name": "a", "sha256": "bad"}]})
        ok = parse_event({**base, "artifacts": [{"name": "a", "sha256": digest("a")}]})
        self.assertEqual(ok.artifacts[0].name, "a")

    def test_failed_requires_reason(self):
        base = {"event_id": "e1", "node": "n", "attempt": 1,
                "kind": "failed", "timestamp": "t"}
        with self.assertRaises(EventError):
            parse_event(base)
        event = parse_event({**base, "reason": "boom"})
        self.assertEqual(event.reason, "boom")

    def test_attempt_must_be_positive(self):
        for attempt in (0, -1, True):
            with self.subTest(attempt=attempt):
                with self.assertRaises(EventError):
                    parse_event({"event_id": "e", "node": "n", "attempt": attempt,
                                 "kind": "started", "timestamp": "t"})

    def test_unknown_kind_and_fields(self):
        with self.assertRaises(EventError):
            parse_event({"event_id": "e", "node": "n", "attempt": 1,
                         "kind": "nope", "timestamp": "t"})
        with self.assertRaises(EventError):
            parse_event({"event_id": "e", "node": "n", "attempt": 1,
                         "kind": "started", "timestamp": "t", "x": 1})

    def test_artifacts_only_on_artifacts_event(self):
        with self.assertRaises(EventError):
            parse_event({"event_id": "e", "node": "n", "attempt": 1,
                         "kind": "submitted", "timestamp": "t",
                         "artifacts": [{"name": "a", "sha256": digest("a")}]})


if __name__ == "__main__":
    unittest.main()
