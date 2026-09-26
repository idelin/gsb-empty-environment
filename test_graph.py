"""Tests for the key-owning graph executor: order independence, resume,
strict key-collision detection, and error handling."""

import unittest

from keyrng import (
    Executor,
    KeyCollisionError,
    Node,
    Tensor,
    dropout_checkpoint_forward,
    key,
    normal,
    topo_sort,
    uniform,
)


def make_nodes():
    def op_x(k, inputs):
        return Tensor([float(i) for i in range(8)], (8,))

    def op_noise(k, inputs):
        return normal(k, (8,))

    def op_dropped(k, inputs):
        x, noise = inputs
        y, _token = dropout_checkpoint_forward(x + noise, k, 0.5)
        return y

    def op_out(k, inputs):
        (dropped,) = inputs
        return Tensor([dropped.sum()], (1,))

    return [
        Node("x", (), op_x),
        Node("noise", (), op_noise),
        Node("dropped", ("x", "noise"), op_dropped),
        Node("out", ("dropped",), op_out),
    ]


class TestExecutionOrderIndependence(unittest.TestCase):
    def test_node_listing_order_does_not_change_results(self):
        nodes = make_nodes()
        forward = Executor(key(1)).run(nodes)
        reversed_listing = Executor(key(1)).run(list(reversed(nodes)))
        for name in ("x", "noise", "dropped", "out"):
            self.assertTrue(forward[name].exact_equal(reversed_listing[name]))

    def test_root_key_determines_everything(self):
        a = Executor(key(7)).run(make_nodes())
        b = Executor(key(7)).run(make_nodes())
        c = Executor(key(8)).run(make_nodes())
        self.assertTrue(a["out"].exact_equal(b["out"]))
        self.assertFalse(a["noise"].exact_equal(c["noise"]))

    def test_node_keys_are_stable_and_distinct(self):
        ex = Executor(key(1), strict=True)
        keys = {n.name: ex.node_key(n.name) for n in make_nodes()}
        self.assertEqual(len({k.data for k in keys.values()}), 4)
        ex2 = Executor(key(1))
        for name, k in keys.items():
            self.assertEqual(ex2.node_key(name), k)


class TestResume(unittest.TestCase):
    def test_resume_does_not_reconsume_keys(self):
        nodes = make_nodes()
        full = Executor(key(3))
        full_cache = full.run(nodes)
        self.assertEqual(full.keys_consumed, 4)

        # Simulate a crash after the first two nodes: keep their results.
        partial_cache = {"x": full_cache["x"], "noise": full_cache["noise"]}
        resumed = Executor(key(3))
        final_cache = resumed.run(nodes, cache=dict(partial_cache))

        # Only the two remaining nodes consumed keys on resume.
        self.assertEqual(resumed.keys_consumed, 2)
        for name in ("x", "noise", "dropped", "out"):
            self.assertTrue(final_cache[name].exact_equal(full_cache[name]))

    def test_fully_cached_run_consumes_no_keys(self):
        nodes = make_nodes()
        cache = Executor(key(4)).run(nodes)
        resumed = Executor(key(4))
        again = resumed.run(nodes, cache=dict(cache))
        self.assertEqual(resumed.keys_consumed, 0)
        self.assertTrue(again["out"].exact_equal(cache["out"]))

    def test_resume_under_strict_mode(self):
        nodes = make_nodes()
        full_cache = Executor(key(5), strict=True).run(nodes)
        partial = {"x": full_cache["x"]}
        resumed = Executor(key(5), strict=True)
        final = resumed.run(nodes, cache=dict(partial))
        self.assertEqual(resumed.keys_consumed, 3)
        self.assertTrue(final["out"].exact_equal(full_cache["out"]))


class TestStrictKeyCollision(unittest.TestCase):
    def test_consuming_same_key_twice_raises(self):
        ex = Executor(key(6), strict=True)
        ex.node_key("alpha")
        with self.assertRaises(KeyCollisionError):
            ex.node_key("alpha")

    def test_non_strict_mode_allows_reuse(self):
        ex = Executor(key(6), strict=False)
        ex.node_key("alpha")
        ex.node_key("alpha")  # no error
        self.assertEqual(ex.keys_consumed, 2)

    def test_duplicate_node_names_rejected(self):
        nodes = make_nodes() + [Node("x", (), lambda k, i: Tensor.zeros((1,)))]
        with self.assertRaises(ValueError):
            topo_sort(nodes)

    def test_unknown_dependency_rejected(self):
        nodes = [Node("a", ("missing",), lambda k, i: None)]
        with self.assertRaises(ValueError):
            topo_sort(nodes)

    def test_cycle_rejected(self):
        nodes = [
            Node("a", ("b",), lambda k, i: None),
            Node("b", ("a",), lambda k, i: None),
        ]
        with self.assertRaises(ValueError):
            topo_sort(nodes)


class TestGraphEdgeCases(unittest.TestCase):
    def test_empty_tensor_node(self):
        nodes = [
            Node("empty", (), lambda k, i: uniform(k, (0,))),
            Node("n", ("empty",), lambda k, i: Tensor([i[0].size], (1,))),
        ]
        result = Executor(key(9)).run(nodes)
        self.assertEqual(result["empty"].data, [])
        self.assertEqual(result["n"].data, [0.0])

    def test_invalid_sampling_parameters_propagate(self):
        nodes = [Node("bad", (), lambda k, i: normal(k, (4,), std=-1.0))]
        with self.assertRaises(ValueError):
            Executor(key(10)).run(nodes)


if __name__ == "__main__":
    unittest.main()
