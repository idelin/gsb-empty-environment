import json
import unittest

from gradacc import checkpoint as ckpt
from gradacc.model import loss_and_grad
from gradacc.tensor import Tensor
from gradacc.trainer import Trainer

SAMPLES = [([1.0, 0.0], [1.0]), ([0.0, 1.0], [2.0]), ([1.0, 1.0], [0.5])]


def make_trainer():
    params = {"w": Tensor([1.0, -1.0], (1, 2)), "b": Tensor([0.5], (1,))}
    return Trainer(params, lr=0.1, momentum=0.9)


def deliver(trainer, batch_id, micro_id, samples):
    _, grads = loss_and_grad(trainer.params, samples)
    trainer.add_microbatch(batch_id, micro_id, grads, weight=len(samples))


def assert_same_state(testcase, left, right):
    testcase.assertEqual(left.step, right.step)
    testcase.assertEqual(left.version, right.version)
    for name in left.params:
        testcase.assertEqual(left.params[name].data, right.params[name].data)
        testcase.assertEqual(left.velocity[name].data,
                             right.velocity[name].data)
    testcase.assertEqual(left.batch_statuses(), right.batch_statuses())


class CheckpointTest(unittest.TestCase):
    def test_checkpoint_contains_unconfirmed_batches(self):
        trainer = make_trainer()
        trainer.begin_batch("B1")
        deliver(trainer, "B1", "m1", [SAMPLES[0]])
        payload = json.loads(ckpt.dumps(trainer))
        self.assertIn("B1", payload["open_batches"])
        self.assertIn("m1", payload["open_batches"]["B1"]["micros"])

    def test_roundtrip_preserves_full_state(self):
        trainer = make_trainer()
        trainer.begin_batch("B1")
        deliver(trainer, "B1", "m1", SAMPLES[:2])
        trainer.commit_batch("B1")
        trainer.begin_batch("B2")  # unconfirmed batch in flight
        deliver(trainer, "B2", "m1", [SAMPLES[2]])
        restored = ckpt.loads(ckpt.dumps(trainer))
        assert_same_state(self, trainer, restored)
        self.assertTrue(restored.accumulator.is_open("B2"))

    def test_continue_after_restore_matches_uninterrupted(self):
        # uninterrupted run
        ref = make_trainer()
        ref.begin_batch("B1")
        deliver(ref, "B1", "m1", SAMPLES[:2])
        ref.commit_batch("B1")
        ref.begin_batch("B2")
        deliver(ref, "B2", "m1", [SAMPLES[2]])
        ref.commit_batch("B2")
        # checkpoint right after B1 commits, restore, then continue
        trainer = make_trainer()
        trainer.begin_batch("B1")
        deliver(trainer, "B1", "m1", SAMPLES[:2])
        trainer.commit_batch("B1")
        trainer.begin_batch("B2")
        deliver(trainer, "B2", "m1", [SAMPLES[2]])
        restored = ckpt.loads(ckpt.dumps(trainer))
        restored.commit_batch("B2")
        assert_same_state(self, ref, restored)

    def test_recommit_after_restore_stays_idempotent(self):
        trainer = make_trainer()
        trainer.begin_batch("B1")
        deliver(trainer, "B1", "m1", SAMPLES[:2])
        trainer.commit_batch("B1")
        restored = ckpt.loads(ckpt.dumps(trainer))
        result = restored.commit_batch("B1")  # replays stored result
        self.assertEqual(result.status.value, "committed")
        self.assertEqual(restored.step, 1)  # not applied twice

    def test_pending_prepared_batch_survives_checkpoint(self):
        # crash between prepare and publish, then recover from the checkpoint
        trainer = make_trainer()
        trainer.begin_batch("B1")
        deliver(trainer, "B1", "m1", SAMPLES[:2])
        saved = {}

        def crash_hook():
            saved["ckpt"] = ckpt.dumps(trainer)
            raise RuntimeError("simulated crash")

        with self.assertRaises(RuntimeError):
            trainer.commit_batch("B1", hook=crash_hook)
        payload = json.loads(saved["ckpt"])
        pending = payload["shards"]["shard-0"]["pending"]
        self.assertIn("B1", pending)  # prepared-but-unpublished is captured
        recovered = ckpt.loads(saved["ckpt"])
        self.assertEqual(recovered.step, 0)  # nothing applied before recovery
        recovered.resume_pending()
        result = recovered.commit_batch("B1")  # idempotent retry closes it
        self.assertEqual(result.status.value, "committed")
        # equals a run that never crashed
        ref = make_trainer()
        ref.begin_batch("B1")
        deliver(ref, "B1", "m1", SAMPLES[:2])
        ref.commit_batch("B1")
        assert_same_state(self, ref, recovered)


if __name__ == "__main__":
    unittest.main()
