"""Requirement-driven test suite for minigrad.  Python 3.11, stdlib only."""

import math
import threading
import unittest

from minigrad import (
    BackwardCancelled,
    CancelToken,
    Graph,
    GraphReleasedError,
    StaleInputError,
    Tensor,
    ops,
)


def numerical_grad(fn, tensors, eps=1e-5):
    """Independent central-difference gradient of fn() w.r.t. each tensor."""
    grads = []
    for t in tensors:
        g = []
        for i in range(t.size):
            orig = t.data[i]
            t.data[i] = orig + eps
            hi = fn()
            t.data[i] = orig - eps
            lo = fn()
            t.data[i] = orig
            g.append((hi - lo) / (2 * eps))
        grads.append(g)
    return grads


def assert_close(tc, got, want, tol=1e-4):
    tc.assertEqual(len(got), len(want))
    for a, b in zip(got, want):
        tc.assertAlmostEqual(a, b, delta=tol)


class TestTopologicalAccumulation(unittest.TestCase):
    def test_fanout_accumulates_in_reverse_topological_order(self):
        with Graph("fanout") as g:
            x = Tensor([1.0, -2.0, 3.0], requires_grad=True, name="x")
            a = x * x            # branch 1
            b = (x + 1.0).tanh()  # branch 2
            y = (a * b).sum()
            g.backward(y)
        # dy/dx = 2x*tanh(x+1) + x^2*(1-tanh(x+1)^2)
        want = []
        for v in [1.0, -2.0, 3.0]:
            t = math.tanh(v + 1.0)
            want.append(2 * v * t + v * v * (1 - t * t))
        assert_close(self, x.grad.data, want)

    def test_shared_node_backward_runs_exactly_once(self):
        with Graph("shared") as g:
            x = Tensor([2.0], requires_grad=True)
            y = x * x
            z = y * y + y  # y feeds two consumers -> one shared node
            out = z.sum()
            executions = g.backward(out)
        # nodes: mul, mul, add, sum -> each executed once
        self.assertEqual(executions, 4)
        self.assertAlmostEqual(x.grad.data[0], 4 * 2.0**3 + 2 * 2.0)


class TestRetainAndRelease(unittest.TestCase):
    def test_second_backward_without_retain_is_refused(self):
        with Graph("once") as g:
            x = Tensor([3.0], requires_grad=True)
            y = (x * x).sum()
            g.backward(y)
        self.assertTrue(g.released)
        with self.assertRaises(GraphReleasedError):
            g.backward(y)

    def test_retain_allows_repeated_backward_and_accumulates(self):
        with Graph("retained") as g:
            x = Tensor([3.0], requires_grad=True)
            y = (x * x).sum()
            g.backward(y, retain_graph=True)
            g.backward(y, retain_graph=True)
            g.backward(y)  # final call consumes the graph
        self.assertAlmostEqual(x.grad.data[0], 3 * 6.0)
        self.assertTrue(g.released)

    def test_double_release_raises(self):
        g = Graph("double")
        with g:
            x = Tensor([1.0], requires_grad=True)
            _ = (x * 2.0).sum()
        g.release()
        with self.assertRaises(GraphReleasedError):
            g.release()

    def test_storage_survives_release_while_other_graph_references_it(self):
        shared = Tensor([1.5, -0.5], requires_grad=True, name="shared")
        storage = shared._storage
        pins_before = storage.pins
        with Graph("g1") as g1:
            y1 = (shared * 2.0).sum()
        with Graph("g2") as g2:
            y2 = (shared * shared).sum()
        self.assertGreater(storage.pins, pins_before)  # both graphs pin it
        g1.release()
        self.assertFalse(storage.is_freed)  # g2 still references it
        g2.backward(y2)  # backward on g2 still works after g1 released
        assert_close(self, shared.grad.data, [3.0, -1.0])
        self.assertTrue(g2.released)  # non-retained backward consumed it
        self.assertEqual(storage.pins, pins_before)


class TestVersionCounter(unittest.TestCase):
    def test_inplace_mutation_of_saved_input_is_detected(self):
        with Graph("stale") as g:
            x = Tensor([2.0, 3.0], requires_grad=True, name="x")
            y = (x * x).sum()
            x.add_(1.0)  # in-place after forward
            with self.assertRaises(StaleInputError):
                g.backward(y)

    def test_mutation_after_backward_is_fine_when_graph_consumed(self):
        with Graph("ok") as g:
            x = Tensor([2.0], requires_grad=True)
            y = (x * x).sum()
            g.backward(y)
            x.mul_(5.0)  # graph already consumed; no error
        self.assertAlmostEqual(x.grad.data[0], 4.0)


class TestAtomicTransactions(unittest.TestCase):
    def _failing_graph(self, w):
        """Graph whose middle op raises during backward."""
        g = Graph("failing")
        with g:
            a = w * 2.0

            def fwd(t):
                return [v * 3.0 for v in t.data], t.shape

            def bwd(gout, node):
                raise RuntimeError("injected mid-graph failure")

            b = ops.apply((a,), fwd, bwd, name="boom")
            out = b.sum()
        return g, out

    def test_failed_backward_rolls_back_and_preserves_committed_grads(self):
        w = Tensor([1.0, 2.0], requires_grad=True, name="w")
        with Graph("good") as good:
            y = (w * w).sum()
            good.backward(y)  # commits dw = 2w
        committed = list(w.grad.data)

        g, out = self._failing_graph(w)
        with self.assertRaises(RuntimeError):
            g.backward(out)
        # failed transaction committed nothing on top of the old grads
        assert_close(self, w.grad.data, committed)

    def test_error_does_not_pollute_independent_graphs(self):
        w1 = Tensor([1.0], requires_grad=True)
        w2 = Tensor([4.0], requires_grad=True)
        g1, out1 = self._failing_graph(w1)
        with Graph("independent") as g2:
            y2 = (w2 * w2).sum()
        with self.assertRaises(RuntimeError):
            g1.backward(out1)
        g2.backward(y2)  # unaffected by the other graph's failure
        self.assertIsNone(w1.grad)
        self.assertAlmostEqual(w2.grad.data[0], 8.0)

    def test_cancellation_rolls_back_transaction(self):
        x = Tensor([1.0, 2.0, 3.0], requires_grad=True)
        with Graph("cancel") as g:
            y = (x * x * x).sum()
            token = CancelToken()
            token.cancel()
            with self.assertRaises(BackwardCancelled):
                g.backward(y, cancel_token=token)
        self.assertIsNone(x.grad)  # nothing committed
        g.backward(y)  # graph still usable after rollback
        assert_close(self, x.grad.data, [3.0, 12.0, 27.0])

    def test_multi_graph_shared_params_atomic_accumulation(self):
        p = Tensor([0.5, -1.0], requires_grad=True, name="p")
        with Graph("t1") as g1:
            y1 = (p * p).sum()
        with Graph("t2") as g2:
            y2 = (p * 3.0).sum()
        g1.backward(y1)
        g2.backward(y2)  # second transaction commits on top of the first
        assert_close(self, p.grad.data, [1.0 + 3.0, -2.0 + 3.0])


class TestRecomputeNode(unittest.TestCase):
    def test_dropout_recompute_matches_eager_mask(self):
        import random
        x = Tensor.rand((4,), seed=7, requires_grad=True)
        with Graph("drop") as g:
            y = ops.dropout(x, p=0.5, key=1234).sum()
            g.backward(y, retain_graph=True)
        rng = random.Random(1234)
        mask = [(2.0 if rng.random() >= 0.5 else 0.0) for _ in range(4)]
        assert_close(self, x.grad.data, mask)
        # retained graph: recomputation is deterministic across calls
        g.backward(y)
        assert_close(self, x.grad.data, [2 * m for m in mask])

    def test_recompute_node_detects_stale_input(self):
        x = Tensor.rand((3,), seed=3, requires_grad=True)
        with Graph("drop-stale") as g:
            y = ops.dropout(x, p=0.25, key=99).sum()
            x.fill_(0.0)
            with self.assertRaises(StaleInputError):
                g.backward(y)


class TestNumericalVerification(unittest.TestCase):
    def test_finite_difference_matches_autograd(self):
        def build():
            x = Tensor.rand((2, 3), seed=11, requires_grad=True)
            w = Tensor.rand((3, 2), seed=12, requires_grad=True)
            b = Tensor.rand((2,), seed=13, requires_grad=True)
            c = Tensor.rand((2,), seed=14, requires_grad=True)
            return x, w, b, c

        x, w, b, c = build()

        def fn():
            h = (x @ w + b).tanh()
            return ((h * c).exp().sum()).item()

        with Graph("fd") as g:
            loss = ((x @ w + b).tanh() * c).exp().sum()
            g.backward(loss)

        eps = 1e-5
        for t, grad in zip((x, w, b, c),
                           numerical_grad(fn, (x, w, b, c), eps)):
            assert_close(self, t.grad.data, grad, tol=1e-3)

    def test_div_log_sigmoid_chain_finite_difference(self):
        x = Tensor([0.7, 1.3, -0.4], requires_grad=True)
        y = Tensor([2.0, 0.5, 1.5], requires_grad=True)

        def fn():
            z = ops.sigmoid(ops.log(x * x + y)).sum()
            return z.item()

        with Graph("fd2") as g:
            loss = ops.sigmoid(ops.log(x * x + y)).sum()
            g.backward(loss)
        gx, gy = numerical_grad(fn, (x, y))
        assert_close(self, x.grad.data, gx, tol=1e-3)
        assert_close(self, y.grad.data, gy, tol=1e-3)


class TestConcurrentLifecycle(unittest.TestCase):
    def test_concurrent_release_exactly_one_winner(self):
        results = {"ok": 0, "err": 0}
        errors = []

        def releaser(g):
            try:
                g.release()
                results["ok"] += 1
            except GraphReleasedError:
                results["err"] += 1
            except Exception as exc:  # pragma: no cover
                errors.append(exc)

        for _ in range(20):
            g = Graph("race")
            with g:
                x = Tensor([1.0], requires_grad=True)
                _ = (x * x).sum()
            threads = [threading.Thread(target=releaser, args=(g,))
                       for _ in range(8)]
            for t in threads:
                t.start()
            for t in threads:
                t.join()
            self.assertEqual(results["ok"], 1)
            self.assertEqual(results["err"], 7)
            self.assertTrue(g.released)
            results["ok"] = results["err"] = 0
        self.assertFalse(errors)

    def test_release_blocked_until_backward_transaction_finishes(self):
        import time
        x = Tensor.rand((6,), seed=5, requires_grad=True)
        g = Graph("blocking")
        released = []

        def fwd(t):
            return [v * v for v in t.data], t.shape

        def slow_bwd(gout, node):
            # backward holds the graph lock; a concurrent release must block
            t = threading.Thread(
                target=lambda: released.append(_try_release(g)), daemon=True)
            t.start()
            time.sleep(0.1)
            raise RuntimeError("injected failure during backward")

        with g:
            a = x * 2.0
            b = ops.apply((a,), fwd, slow_bwd, name="slow")
            out = b.sum()
        with self.assertRaises(RuntimeError):
            g.backward(out)
        time.sleep(0.2)  # let the blocked release thread finish
        # rollback kept the graph alive; the queued release then succeeded
        self.assertEqual(released, [True])
        self.assertTrue(g.released)
        self.assertIsNone(x.grad)  # failed transaction committed nothing


def _try_release(g):
    try:
        g.release()
        return True
    except GraphReleasedError:
        return False


if __name__ == "__main__":
    unittest.main()
