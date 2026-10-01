import unittest

from gradacc.coordinator import Coordinator
from gradacc.shard import Decision, Shard
from gradacc.tensor import Tensor


def make_shard():
    params = {"w": Tensor([1.0, 2.0], (1, 2)), "b": Tensor([0.5], (1,))}
    return Shard("s0", params, lr=0.1, momentum=0.5)


def grads():
    return {"w": Tensor([0.4, 0.4], (1, 2)), "b": Tensor([0.2], (1,))}


class ShardTest(unittest.TestCase):
    def test_prepare_does_not_mutate(self):
        shard = make_shard()
        shard.prepare("B1", grads(), Decision.PUBLISH)
        self.assertEqual(shard.params["w"].data, [1.0, 2.0])
        self.assertEqual(shard.velocity["w"].data, [0.0, 0.0])
        self.assertEqual(shard.step, 0)
        self.assertIn("B1", shard.pending)

    def test_publish_applies_momentum_update_atomically(self):
        shard = make_shard()
        shard.prepare("B1", grads(), Decision.PUBLISH)
        shard.publish("B1")
        # v = 0.5*0 + g ; p = p - 0.1*v
        self.assertEqual(shard.velocity["w"].data, [0.4, 0.4])
        self.assertAlmostEqual(shard.params["w"].data[0], 1.0 - 0.04)
        self.assertAlmostEqual(shard.params["b"].data[0], 0.5 - 0.02)
        self.assertEqual(shard.step, 1)
        self.assertNotIn("B1", shard.pending)
        shard.publish("B1")  # idempotent
        self.assertEqual(shard.step, 1)
        self.assertAlmostEqual(shard.params["w"].data[0], 1.0 - 0.04)

    def test_skip_decision_changes_nothing(self):
        shard = make_shard()
        shard.prepare("B1", grads(), Decision.SKIP)
        shard.publish("B1")
        self.assertEqual(shard.params["w"].data, [1.0, 2.0])
        self.assertEqual(shard.velocity["w"].data, [0.0, 0.0])
        self.assertEqual(shard.step, 0)
        self.assertEqual(shard.completed["B1"], "skipped")

    def test_abort_discards_prepared_batch(self):
        shard = make_shard()
        shard.prepare("B1", grads(), Decision.PUBLISH)
        shard.abort("B1")
        self.assertEqual(shard.pending, {})
        self.assertEqual(shard.completed["B1"], "aborted")
        self.assertEqual(shard.step, 0)

    def test_resume_pending_replays_prepare(self):
        shard = make_shard()
        shard.prepare("B1", grads(), Decision.PUBLISH)
        shard.resume_pending()  # simulates crash recovery
        self.assertEqual(shard.step, 1)
        self.assertEqual(shard.completed["B1"], "published")

    def test_prepare_rejects_unknown_param(self):
        shard = make_shard()
        with self.assertRaises(KeyError):
            shard.prepare("B1", {"nope": Tensor([1.0], (1,))},
                          Decision.PUBLISH)


class CoordinatorTest(unittest.TestCase):
    def make_coord(self):
        s0 = Shard("s0", {"w": Tensor([1.0, 2.0], (1, 2))}, 0.1, 0.5)
        s1 = Shard("s1", {"b": Tensor([0.5], (1,))}, 0.1, 0.5)
        return Coordinator([s0, s1]), s0, s1

    def full_grads(self):
        return {"w": Tensor([0.4, 0.4], (1, 2)), "b": Tensor([0.2], (1,))}

    def test_commit_prepares_and_publishes_all_shards(self):
        coord, s0, s1 = self.make_coord()
        outcome = coord.commit("B1", self.full_grads())
        self.assertEqual(outcome, "published")
        self.assertEqual(s0.step, 1)
        self.assertEqual(s1.step, 1)
        # idempotent recommit
        self.assertEqual(coord.commit("B1", self.full_grads()), "published")
        self.assertEqual(s0.step, 1)

    def test_nonfinite_grads_skip_on_all_shards(self):
        coord, s0, s1 = self.make_coord()
        bad = self.full_grads()
        bad["w"] = Tensor([float("inf"), 0.0], (1, 2))
        outcome = coord.commit("B1", bad)
        self.assertEqual(outcome, "skipped")
        self.assertEqual(s0.step, 0)
        self.assertEqual(s1.step, 0)
        self.assertEqual(s0.params["w"].data, [1.0, 2.0])
        self.assertEqual(s1.params["b"].data, [0.5])

    def test_crash_between_prepare_and_publish_then_resume(self):
        coord, s0, s1 = self.make_coord()
        calls = []

        def hook():
            calls.append("hook")
            raise RuntimeError("crash")

        with self.assertRaises(RuntimeError):
            coord.commit("B1", self.full_grads(), hook=hook)
        # prepared but not published: nothing applied yet
        self.assertEqual(s0.step, 0)
        self.assertIn("B1", s0.pending)
        self.assertIn("B1", s1.pending)
        coord.resume_pending()
        self.assertEqual(s0.step, 1)
        self.assertEqual(s1.step, 1)
        self.assertEqual(s0.completed["B1"], "published")
        self.assertEqual(s1.completed["B1"], "published")


if __name__ == "__main__":
    unittest.main()
