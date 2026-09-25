import unittest

import mini_autograd as ma


class TestNoGrad(unittest.TestCase):
    def test_no_grad_suspends_recording(self):
        with ma.Graph().use() as g:
            x = ma.tensor([1.0, 2.0], requires_grad=True)
            with ma.no_grad():
                frozen = ma.add(x, x)
            self.assertIsNone(frozen.grad_fn)
            y = ma.sum(ma.mul(x, x))
            self.assertEqual(len(g.nodes), 2)  # mul + sum; add was not recorded
            y.backward()
        self.assertEqual(x.grad.data, [2.0, 4.0])


class TestMultiRoot(unittest.TestCase):
    def test_backward_multi_one_transaction(self):
        with ma.Graph().use():
            x = ma.tensor([1.0, 2.0, 3.0], requires_grad=True)
            l1 = ma.sum(ma.mul(x, x))
            l2 = ma.sum(ma.relu(x))
            ma.backward_multi([l1, l2])
        # 2x + 1
        self.assertEqual(x.grad.data, [3.0, 5.0, 7.0])


class TestCheckpointFailure(unittest.TestCase):
    def test_failed_replay_pollutes_nothing(self):
        def seg(a):
            return ma.sum(ma.faulty(ma.relu(a), token="replay-boom"))

        with ma.Graph().use():
            a = ma.tensor([1.0, -1.0], requires_grad=True)
            y = ma.checkpoint(seg, a, seed=3)
            with ma.faulty_backward("replay-boom"):
                with self.assertRaises(ma.InjectedBackwardError):
                    y.backward()
            self.assertIsNone(a.grad)


if __name__ == "__main__":
    unittest.main()
