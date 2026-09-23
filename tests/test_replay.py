"""Replay the committed example log and compare against the recorded outputs."""
import os

from examples.replay_demo import replay


def test_replay_log(tmp_path):
    log_path = os.path.join(os.path.dirname(__file__), "..", "examples",
                            "replay_log.jsonl")
    harness, outputs = replay(log_path, str(tmp_path / "replay"))
    assert harness.output_log == outputs
    assert harness.consumer.duplicates >= 1  # the crash forced a redelivery
    assert not harness.engine.batches
