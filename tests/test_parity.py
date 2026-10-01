"""End-to-end parity: the Trainer under a fault schedule must match the
independent per-batch reference on final parameters, momentum and counts."""

import unittest

from gradacc.faults import run_schedule
from gradacc.reference import run_reference
from gradacc.trainer import Trainer
from tests.scenario import (BATCHES, EVENTS, LR, MOMENTUM, make_dataset,
                            make_init_params)


class ParityTest(unittest.TestCase):
    def test_trainer_matches_reference_under_faults(self):
        dataset = make_dataset()
        init_params = make_init_params()
        trainer = Trainer({k: v.copy() for k, v in init_params.items()},
                          LR, MOMENTUM)
        trainer, log = run_schedule(trainer, EVENTS, BATCHES, dataset)
        ref = run_reference(init_params, dataset, BATCHES, EVENTS,
                            LR, MOMENTUM)
        # counts must match exactly
        self.assertEqual(trainer.step, ref["step"])
        self.assertEqual(trainer.version, ref["version"])
        # per-batch outcomes must match
        self.assertEqual(trainer.batch_statuses(), ref["closed"])
        # parameters and momentum buffers must match
        for name in ref["params"]:
            self.assertTrue(
                trainer.params[name].almost_equal(ref["params"][name]),
                msg=(f"param {name}: {trainer.params[name].data} vs "
                     f"{ref['params'][name].data}"))
            self.assertTrue(
                trainer.velocity[name].almost_equal(ref["velocity"][name]),
                msg=(f"momentum {name}: {trainer.velocity[name].data} vs "
                     f"{ref['velocity'][name].data}"))
        # sanity: the schedule actually exercised the interesting paths
        statuses = set(ref["closed"].values())
        self.assertIn("committed", statuses)
        self.assertIn("skipped_overflow", statuses)
        self.assertIn("rejected_stale", statuses)
        self.assertIn(("crash", "C"), log)
        self.assertEqual(ref["step"], 3)  # A, C, E committed; B, D did not


if __name__ == "__main__":
    unittest.main()
