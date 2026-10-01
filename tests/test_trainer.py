import unittest

from gradacc.model import loss_and_grad
from gradacc.tensor import Tensor
from gradacc.trainer import Trainer


def make_trainer():
    params = {"w": Tensor([1.0, -1.0], (1, 2)), "b": Tensor([0.5], (1,))}
    return Trainer(params, lr=0.1, momentum=0.9)


def grads_for(trainer, samples):
    _, grads = loss_and_grad(trainer.params, samples)
    return grads


SAMPLES = [([1.0, 0.0], [1.0]), ([0.0, 1.0], [2.0])]


class TrainerTest(unittest.TestCase):
    def test_nonfinite_second_micro_keeps_all_params_old(self):
        trainer = make_trainer()
        before = {k: v.copy() for k, v in trainer.params.items()}
        trainer.begin_batch("B1")
        trainer.add_microbatch("B1", "m1", grads_for(trainer, [SAMPLES[0]]))
        # second micro-batch carries a non-finite gradient
        trainer.add_microbatch(
            "B1", "m2",
            {"w": Tensor([float("nan"), 0.0], (1, 2)),
             "b": Tensor([0.0], (1,))})
        result = trainer.commit_batch("B1")
        self.assertEqual(result.status.value, "skipped_overflow")
        for name, old in before.items():
            self.assertEqual(trainer.params[name].data, old.data,
                             f"param {name} must keep its old value")
        for name, vel in trainer.velocity.items():
            self.assertEqual(vel.data, [0.0] * vel.size,
                             f"momentum buffer {name} must stay zero")
        self.assertEqual(trainer.step, 0)
        self.assertEqual(trainer.version, 0)

    def test_commit_updates_params_momentum_and_counts(self):
        trainer = make_trainer()
        trainer.begin_batch("B1")
        trainer.add_microbatch("B1", "m1", grads_for(trainer, SAMPLES),
                               weight=len(SAMPLES))
        result = trainer.commit_batch("B1")
        self.assertTrue(result.applied)
        self.assertEqual(trainer.step, 1)
        self.assertEqual(trainer.version, 1)
        self.assertNotEqual(trainer.params["w"].data, [1.0, -1.0])

    def test_duplicate_commit_same_batch_id_is_idempotent(self):
        trainer = make_trainer()
        trainer.begin_batch("B1")
        trainer.add_microbatch("B1", "m1", grads_for(trainer, SAMPLES),
                               weight=len(SAMPLES))
        first = trainer.commit_batch("B1")
        snapshot = {k: v.copy() for k, v in trainer.params.items()}
        second = trainer.commit_batch("B1")
        self.assertIs(first, second)
        self.assertEqual(trainer.step, 1)
        for name, old in snapshot.items():
            self.assertEqual(trainer.params[name].data, old.data)

    def test_batches_do_not_cross_contaminate(self):
        trainer = make_trainer()
        trainer.begin_batch("B1")
        trainer.begin_batch("B2")
        trainer.add_microbatch("B1", "m1", grads_for(trainer, [SAMPLES[0]]))
        trainer.add_microbatch("B2", "m1", grads_for(trainer, [SAMPLES[1]]))
        trainer.commit_batch("B1")
        # B2 is now stale; zero it (clear + re-baseline) and refill with its
        # own micro-batch only
        trainer.zero_batch("B2")
        trainer.add_microbatch("B2", "m2", grads_for(trainer, [SAMPLES[1]]))
        trainer.commit_batch("B2")
        # compare against a fresh trainer that only ever saw B2's gradient
        clean = make_trainer()
        clean.begin_batch("X")
        clean.add_microbatch("X", "m1", grads_for(clean, [SAMPLES[0]]))
        clean.commit_batch("X")
        clean.begin_batch("Y")
        clean.add_microbatch("Y", "m1", grads_for(clean, [SAMPLES[1]]))
        clean.commit_batch("Y")
        for name in trainer.params:
            self.assertTrue(
                trainer.params[name].almost_equal(clean.params[name]),
                msg=f"param {name} diverged")
            self.assertTrue(
                trainer.velocity[name].almost_equal(clean.velocity[name]),
                msg=f"momentum {name} diverged")
        self.assertEqual(trainer.step, clean.step)

    def test_stale_batch_leaves_params_untouched(self):
        trainer = make_trainer()
        trainer.begin_batch("B1")
        trainer.begin_batch("B2")
        trainer.add_microbatch("B1", "m1", grads_for(trainer, [SAMPLES[0]]))
        trainer.add_microbatch("B2", "m1", grads_for(trainer, [SAMPLES[1]]))
        trainer.commit_batch("B1")
        snapshot = {k: v.copy() for k, v in trainer.params.items()}
        result = trainer.commit_batch("B2")
        self.assertEqual(result.status.value, "rejected_stale")
        for name, old in snapshot.items():
            self.assertEqual(trainer.params[name].data, old.data)
        self.assertEqual(trainer.step, 1)


if __name__ == "__main__":
    unittest.main()
