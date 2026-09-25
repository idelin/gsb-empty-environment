import gc
import unittest

import mini_autograd as ma


class TestVersionCounting(unittest.TestCase):
    def test_inplace_after_forward_rejected_at_backward(self):
        with ma.Graph().use():
            x = ma.tensor([1.0, 2.0, 3.0], requires_grad=True)
            y = ma.sum(ma.mul(x, x))
            x.add_([1.0, 0.0, -1.0])  # mutate saved input after forward
            self.assertEqual(x.version, 1)
            with self.assertRaises(ma.VersionMismatchError):
                y.backward()
        self.assertIsNone(x.grad)  # failed transaction committed nothing

    def test_copy_data_bumps_version(self):
        x = ma.tensor([1.0, 2.0], requires_grad=True)
        self.assertEqual(x.version, 0)
        x.copy_data([2.0, 3.0])
        self.assertEqual(x.version, 1)
        self.assertEqual(x.data, [2.0, 3.0])

    def test_inplace_on_nonleaf_forbidden(self):
        with ma.Graph().use():
            x = ma.tensor([1.0, 2.0], requires_grad=True)
            h = ma.relu(x)
            with self.assertRaises(ma.InplaceError):
                h.add_([1.0, 1.0])

    def test_graph_is_usable_again_after_failure(self):
        with ma.Graph().use() as g:
            x = ma.tensor([1.0, 2.0], requires_grad=True)
            y = ma.sum(ma.mul(x, x))
            x.add_([0.0, 0.0])
            with self.assertRaises(ma.VersionMismatchError):
                y.backward()
            self.assertFalse(g.released)

    def test_successful_retry_after_atomic_failure(self):
        with ma.Graph().use():
            x = ma.tensor([1.0, 2.0], requires_grad=True)
            y = ma.sum(ma.faulty(ma.mul(x, x), token="retry"))
            with ma.faulty_backward("retry"):
                with self.assertRaises(ma.InjectedBackwardError):
                    y.backward(retain_graph=True)
            self.assertIsNone(x.grad)
            # Disarm the fault and retry the exact same retained transaction.
            y.backward()
            self.assertEqual(x.grad.data, [2.0, 4.0])


class TestRetainGraph(unittest.TestCase):
    def test_second_backward_without_retain_rejected(self):
        with ma.Graph().use():
            x = ma.tensor([1.0, 2.0], requires_grad=True)
            y = ma.sum(ma.mul(x, x))
            y.backward()
            with self.assertRaises(ma.GraphReleasedError):
                y.backward()

    def test_repeat_backward_accumulates(self):
        with ma.Graph().use():
            x = ma.tensor([1.0, 2.0], requires_grad=True)
            y = ma.sum(ma.mul(x, x))
            y.backward(retain_graph=True)
            y.backward(retain_graph=True)
            self.assertEqual(x.grad.data, [4.0, 8.0])

    def test_retain_then_release(self):
        with ma.Graph().use() as g:
            x = ma.tensor([1.0], requires_grad=True)
            y = ma.sum(ma.mul(x, x))
            y.backward(retain_graph=True)
            self.assertFalse(g.released)
        y.backward()
        self.assertTrue(g.released)


class TestSharedParameters(unittest.TestCase):
    def test_two_graphs_sharing_leaf(self):
        with ma.Graph("g1").use():
            p = ma.tensor([2.0, 3.0], requires_grad=True)
            l1 = ma.sum(ma.mul(p, p))            # dl1/dp = 2p
        with ma.Graph("g2").use():
            l2 = ma.sum(ma.add(p, ma.tensor(3.0)))  # dl2/dp = 1
        l1.backward()
        self.assertEqual(p.grad.data, [4.0, 6.0])
        l2.backward()
        self.assertEqual(p.grad.data, [5.0, 7.0])

    def test_failure_in_one_graph_preserves_committed_grad(self):
        with ma.Graph("good").use():
            p = ma.tensor([1.0, 1.0], requires_grad=True)
            good = ma.sum(ma.mul(p, p))
        with ma.Graph("bad").use():
            bad = ma.sum(ma.faulty(p, token="boom"))
        good.backward()
        before = list(p.grad.data)
        with ma.faulty_backward("boom"):
            with self.assertRaises(ma.InjectedBackwardError):
                bad.backward()
        self.assertEqual(p.grad.data, before)  # committed gradient untouched


class TestAtomicCancel(unittest.TestCase):
    def test_cancel_aborts_whole_transaction(self):
        with ma.Graph().use():
            x = ma.tensor([1.0, 2.0, 3.0], requires_grad=True)
            h = ma.relu(x)
            y = ma.sum(ma.mul(h, h))
            seen = []

            def cancel(node):
                seen.append(node.name)
                return len(seen) >= 2

            with self.assertRaises(ma.BackwardAbortedError):
                y.backward(cancel=cancel)
            self.assertIsNone(x.grad)

    def test_injected_failure_is_atomic(self):
        with ma.Graph().use():
            x = ma.tensor([1.0, 2.0], requires_grad=True)
            h = ma.relu(x)
            y = ma.sum(ma.faulty(h, token="late"))
            with ma.faulty_backward("late"):
                with self.assertRaises(ma.InjectedBackwardError):
                    y.backward()
            self.assertIsNone(x.grad)


class TestIndependentGraphs(unittest.TestCase):
    def test_error_does_not_poison_sibling_graph(self):
        with ma.Graph("ok").use():
            a = ma.tensor([1.0, 1.0], requires_grad=True)
            ok = ma.sum(ma.relu(a))
        with ma.Graph("broken").use() as broken:
            b = ma.tensor([1.0, 1.0], requires_grad=True)
            broken_loss = ma.sum(ma.faulty(b, token="zap"))
        with ma.faulty_backward("zap"):
            with self.assertRaises(ma.InjectedBackwardError):
                broken_loss.backward()
        self.assertFalse(broken.released is None)  # graph object intact
        ok.backward()
        self.assertEqual(a.grad.data, [1.0, 1.0])


if __name__ == "__main__":
    unittest.main()
