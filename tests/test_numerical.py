"""Hand-computed numerical verification of the full accumulate-commit path.

Scenario (1-D linear model, MSE loss, SGD with momentum):
    w0 = 0.5, b0 = 0.1, lr = 0.1, momentum = 0.5
    batch B1: micro m1 = [s0], micro m2 = [s1, s2]   -> applied
    batch B2: one corrupt micro                       -> skipped entirely
    batch B3: micro m1 = [s0]                         -> applied
All expected values below are derived by hand from the update rules
    v = momentum * v + g ;  p = p - lr * v
and are independent of the library code.
"""

import unittest

from gradacc.model import loss_and_grad
from gradacc.tensor import Tensor
from gradacc.trainer import Trainer

S0 = ([1.0], [1.0])
S1 = ([2.0], [2.0])
S2 = ([0.0], [0.0])


def make_trainer():
    params = {"w": Tensor([0.5], (1, 1)), "b": Tensor([0.1], (1,))}
    return Trainer(params, lr=0.1, momentum=0.5)


def deliver(trainer, batch_id, micro_id, samples):
    _, grads = loss_and_grad(trainer.params, samples)
    trainer.add_microbatch(batch_id, micro_id, grads, weight=len(samples))


class HandComputedTest(unittest.TestCase):
    def test_full_scenario_matches_hand_computation(self):
        trainer = make_trainer()
        # --- batch B1: grads dw = -4.4/3, db = -0.8 (weighted mean) ---
        trainer.begin_batch("B1")
        deliver(trainer, "B1", "m1", [S0])
        deliver(trainer, "B1", "m2", [S1, S2])
        r1 = trainer.commit_batch("B1")
        self.assertTrue(r1.applied)
        gw1 = -4.4 / 3.0
        gb1 = -0.8
        vw1 = gw1            # 0.5*0 + gw1
        vb1 = gb1
        w1 = 0.5 - 0.1 * vw1
        b1 = 0.1 - 0.1 * vb1
        self.assertAlmostEqual(trainer.params["w"].data[0], w1, delta=1e-12)
        self.assertAlmostEqual(trainer.params["b"].data[0], b1, delta=1e-12)
        self.assertAlmostEqual(trainer.velocity["w"].data[0], vw1, delta=1e-12)
        self.assertAlmostEqual(trainer.velocity["b"].data[0], vb1, delta=1e-12)
        self.assertEqual(trainer.step, 1)
        # --- batch B2: corrupt -> skip; nothing may change ---
        trainer.begin_batch("B2")
        trainer.add_microbatch(
            "B2", "m1",
            {"w": Tensor([float("inf")], (1, 1)), "b": Tensor([0.0], (1,))})
        r2 = trainer.commit_batch("B2")
        self.assertEqual(r2.status.value, "skipped_overflow")
        self.assertAlmostEqual(trainer.params["w"].data[0], w1, delta=1e-12)
        self.assertAlmostEqual(trainer.params["b"].data[0], b1, delta=1e-12)
        self.assertAlmostEqual(trainer.velocity["w"].data[0], vw1, delta=1e-12)
        self.assertAlmostEqual(trainer.velocity["b"].data[0], vb1, delta=1e-12)
        self.assertEqual(trainer.step, 1)   # step must not advance
        self.assertEqual(trainer.version, 1)
        # --- batch B3: single micro on s0 with the updated params ---
        trainer.begin_batch("B3")
        deliver(trainer, "B3", "m1", [S0])
        r3 = trainer.commit_batch("B3")
        self.assertTrue(r3.applied)
        err = (w1 * 1.0 + b1) - 1.0
        gw3 = 2.0 * err
        gb3 = 2.0 * err
        vw2 = 0.5 * vw1 + gw3
        vb2 = 0.5 * vb1 + gb3
        w2 = w1 - 0.1 * vw2
        b2 = b1 - 0.1 * vb2
        self.assertAlmostEqual(trainer.params["w"].data[0], w2, delta=1e-12)
        self.assertAlmostEqual(trainer.params["b"].data[0], b2, delta=1e-12)
        self.assertAlmostEqual(trainer.velocity["w"].data[0], vw2, delta=1e-12)
        self.assertAlmostEqual(trainer.velocity["b"].data[0], vb2, delta=1e-12)
        self.assertEqual(trainer.step, 2)
        self.assertEqual(trainer.version, 2)


if __name__ == "__main__":
    unittest.main()
