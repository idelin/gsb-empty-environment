"""Checkpoint/restore: unconfirmed batches, crash between prepare/publish."""

import json
import unittest

from coordinator import CommitStatus, Coordinator, Shard, ShardState
from optimizer import SGD
from tensor import Tensor

LR = 0.1
MU = 0.9


def make_coordinator(expected=2):
    shard0 = Shard("shard-0", SGD({"w": Tensor([1.0, -2.0])}, LR, MU))
    shard1 = Shard("shard-1", SGD({"b": Tensor([0.5])}, LR, MU))
    return Coordinator([shard0, shard1], expected)


def grads(w, b):
    return {"w": Tensor(w), "b": Tensor(b)}


class CheckpointTest(unittest.TestCase):
    def test_checkpoint_is_json_serializable_and_has_unconfirmed(self):
        coord = make_coordinator(expected=3)
        coord.submit("b1", grads([0.5, 0.5], [0.1]), 0)
        coord.submit("b2", grads([0.25, -0.25], [0.2]), 0)
        snap = json.loads(json.dumps(coord.checkpoint()))
        unconfirmed = {
            c["batch_id"] for c in snap["accumulator"]["contributions"]
        }
        self.assertEqual(unconfirmed, {"b1", "b2"})
        self.assertEqual(snap["accumulator"]["param_version"], 0)

    def test_restore_then_finish_commit(self):
        coord = make_coordinator(expected=3)
        coord.submit("b1", grads([0.5, 0.5], [0.1]), 0)
        coord.submit("b2", grads([0.25, -0.25], [0.2]), 0)
        snap = json.dumps(coord.checkpoint())
        restored = Coordinator.restore(json.loads(snap))
        self.assertEqual(restored.param_version, 0)
        restored.submit("b3", grads([0.25, 0.25], [0.1]), 0)
        self.assertEqual(restored.commit(), CommitStatus.COMMITTED)
        clean = make_coordinator(expected=3)
        clean.submit("b1", grads([0.5, 0.5], [0.1]), 0)
        clean.submit("b2", grads([0.25, -0.25], [0.2]), 0)
        clean.submit("b3", grads([0.25, 0.25], [0.1]), 0)
        clean.commit()
        for r_shard, c_shard in zip(restored.shards, clean.shards):
            self.assertEqual(r_shard.optimizer.params, c_shard.optimizer.params)
            self.assertEqual(
                r_shard.optimizer.momentum_buffers,
                c_shard.optimizer.momentum_buffers,
            )
            self.assertEqual(
                r_shard.optimizer.step_count, c_shard.optimizer.step_count
            )

    def test_crash_after_prepare_recovers_by_publish(self):
        coord = make_coordinator()
        coord.submit("b1", grads([0.5, -0.25], [0.1]), 0)
        coord.submit("b2", grads([1.5, 0.75], [-0.2]), 0)
        agg = coord.accumulator.aggregate()
        for shard in coord.shards:
            shard.prepare({n: agg[n] for n in shard.param_names})
        snap = json.dumps(coord.checkpoint())
        recovered = Coordinator.restore(json.loads(snap))
        for shard in recovered.shards:
            self.assertIs(shard.state, ShardState.PREPARED)
        recovered.recover()
        for shard in recovered.shards:
            self.assertIs(shard.state, ShardState.IDLE)
        clean = make_coordinator()
        clean.submit("b1", grads([0.5, -0.25], [0.1]), 0)
        clean.submit("b2", grads([1.5, 0.75], [-0.2]), 0)
        clean.commit()
        self.assertEqual(recovered.step_count, clean.step_count)
        self.assertEqual(recovered.param_version, clean.param_version)
        for r_shard, c_shard in zip(recovered.shards, clean.shards):
            self.assertEqual(r_shard.optimizer.params, c_shard.optimizer.params)
            self.assertEqual(
                r_shard.optimizer.momentum_buffers,
                c_shard.optimizer.momentum_buffers,
            )

    def test_partial_prepare_recovers_by_abort(self):
        coord = make_coordinator()
        coord.submit("b1", grads([0.5, -0.25], [0.1]), 0)
        coord.submit("b2", grads([1.5, 0.75], [-0.2]), 0)
        agg = coord.accumulator.aggregate()
        shard0 = coord.shards[0]
        shard0.prepare({n: agg[n] for n in shard0.param_names})
        snap = json.dumps(coord.checkpoint())
        recovered = Coordinator.restore(json.loads(snap))
        recovered.recover()
        for shard in recovered.shards:
            self.assertIs(shard.state, ShardState.IDLE)
            self.assertEqual(shard.optimizer.step_count, 0)
        self.assertEqual(
            recovered.shards[0].optimizer.params["w"].data, [1.0, -2.0]
        )
        self.assertEqual(
            recovered.shards[1].optimizer.params["b"].data, [0.5]
        )


if __name__ == "__main__":
    unittest.main()
