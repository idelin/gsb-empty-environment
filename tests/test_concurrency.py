import threading
import unittest

import mini_autograd as ma


class TestConcurrentBackward(unittest.TestCase):
    def test_independent_graphs_backward_in_parallel(self):
        with ma.Graph("g1").use():
            x1 = ma.tensor([1.0, 2.0, 3.0], requires_grad=True)
            l1 = ma.sum(ma.mul(x1, x1))
        with ma.Graph("g2").use():
            x2 = ma.tensor([4.0, 5.0], requires_grad=True)
            l2 = ma.sum(ma.add(x2, x2))

        errors = []

        def run1():
            try:
                for _ in range(25):
                    l1.backward(retain_graph=True)
            except Exception as exc:
                errors.append(exc)

        def run2():
            try:
                for _ in range(25):
                    l2.backward(retain_graph=True)
            except Exception as exc:
                errors.append(exc)

        t1 = threading.Thread(target=run1)
        t2 = threading.Thread(target=run2)
        t1.start()
        t2.start()
        t1.join()
        t2.join()
        self.assertEqual(errors, [])
        self.assertEqual(x1.grad.data, [50.0, 100.0, 150.0])
        self.assertEqual(x2.grad.data, [50.0, 50.0])

    def test_concurrent_release_and_backward_is_serialized(self):
        outcomes = []

        def worker(use_fault):
            for _ in range(30):
                with ma.Graph("local").use():
                    q = ma.tensor([1.0, 1.0], requires_grad=True)
                    if use_fault:
                        loss = ma.sum(ma.faulty(q, token="race"))
                    else:
                        loss = ma.sum(ma.mul(q, q))
                try:
                    if use_fault:
                        with ma.faulty_backward("race"):
                            loss.backward()
                    else:
                        loss.backward()
                except ma.InjectedBackwardError:
                    outcomes.append("failed")
                except ma.GraphReleasedError:
                    outcomes.append("released")
                else:
                    outcomes.append("ok")

        threads = [
            threading.Thread(target=worker, args=(i % 2 == 0,))
            for i in range(4)
        ]
        for t in threads:
            t.start()
        for t in threads:
            t.join()

        self.assertEqual(len(outcomes), 120)
        self.assertIn("ok", outcomes)
        self.assertIn("failed", outcomes)


if __name__ == "__main__":
    unittest.main()
