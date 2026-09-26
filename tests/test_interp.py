import unittest

from tensorfusion import (ElementwiseError, Program, Tensor, run_fused,
                          run_unfused)
from tensorfusion.verify import Verdict, compare_tensors


def boundary_env():
    return {
        "a": Tensor.from_values([0.0, -0.0, 1.0, -1.0, 1e-300, 1e300], (2, 3), name="a"),
        "b": Tensor.from_values([2.5, -3.75, 0.5], (3,), name="b"),
        "c": Tensor.from_values([float("inf"), float("-inf"), float("nan"),
                                 1.0, -1.0, 4.0], (2, 3), name="c"),
    }


class TestFusedMatchesUnfused(unittest.TestCase):
    def test_bitwise_on_boundary_values(self):
        program = Program(inputs=["a", "b", "c"])
        t1 = program.add("a", "b")
        t2 = program.mul(t1, "c")
        t3 = program.neg(t2)
        out = program.sub(t3, "b")
        env = boundary_env()
        values_u, _ = run_unfused(program, env)
        values_f, _ = run_fused(program, env)
        cmp = compare_tensors(values_u[out], values_f[out])
        self.assertIs(cmp.verdict, Verdict.BITWISE)

    def test_stats_buffer_reduction(self):
        program = Program(inputs=["a", "b", "c"])
        t1 = program.add("a", "b")
        t2 = program.mul(t1, "c")
        t3 = program.div(t2, "b")
        out = program.reduce_sum(t3)
        env = boundary_env()
        _, stats_u = run_unfused(program, env)
        _, stats_f = run_fused(program, env)
        self.assertEqual(stats_u.buffers_allocated, 4)
        self.assertEqual(stats_f.buffers_allocated, 2)
        self.assertLess(stats_f.element_reads, stats_u.element_reads)
        self.assertLess(stats_f.element_writes, stats_u.element_writes)

    def test_mutate_and_store_semantics_match(self):
        program = Program(inputs=["x", "b", "d"])
        t1 = program.add("x", "b")
        program.mutate("x", "d")
        t2 = program.mul(t1, "x")
        env1 = {"x": Tensor.from_values([1, 2, 3], (3,), name="x"),
                "b": Tensor.from_values([10, 20, 30], (3,), name="b"),
                "d": Tensor.from_values([100, 200, 300], (3,), name="d")}
        env2 = {k: Tensor.from_values(v.to_flat(), v.shape, name=k)
                for k, v in env1.items()}
        values_u, _ = run_unfused(program, env1)
        values_f, _ = run_fused(program, env2)
        cmp = compare_tensors(values_u[t2], values_f[t2])
        self.assertIs(cmp.verdict, Verdict.BITWISE)
        self.assertEqual(env1["x"].to_flat(), env2["x"].to_flat())
        self.assertEqual(env1["x"].version, env2["x"].version)


class TestErrorLocation(unittest.TestCase):
    def test_div_by_zero_located_to_original_expression(self):
        program = Program(inputs=["a", "b"])
        t1 = program.add("a", "b")
        t2 = program.div(t1, "b")
        env = {
            "a": Tensor.from_values([1.0, 2.0, 3.0, 4.0], (2, 2), name="a"),
            "b": Tensor.from_values([1.0, 0.0], (2,), name="b"),
        }
        errors = []
        for runner in (run_unfused, run_fused):
            with self.assertRaises(ElementwiseError) as ctx:
                runner(program, env)
            errors.append(ctx.exception)
        err_u, err_f = errors
        self.assertEqual(err_u.key(), err_f.key())
        self.assertEqual(err_f.op_label, "t2 = div(t1, b)")
        self.assertEqual(err_f.out_index, (0, 1))
        self.assertEqual(err_f.operand_indices["b"], (1,))
        self.assertIn("float division by zero", str(err_f))
        self.assertIn("t2 = div(t1, b)", str(err_f))

    def test_short_circuit_first_op_wins(self):
        program = Program(inputs=["a", "b", "c"])
        t1 = program.div("a", "b")
        t2 = program.div(t1, "c")
        env = {
            "a": Tensor.from_values([1.0, 2.0], (2,), name="a"),
            "b": Tensor.from_values([1.0, 0.0], (2,), name="b"),
            "c": Tensor.from_values([0.0, 1.0], (2,), name="c"),
        }
        keys = []
        for runner in (run_unfused, run_fused):
            with self.assertRaises(ElementwiseError) as ctx:
                runner(program, env)
            keys.append(ctx.exception.key())
        self.assertEqual(keys[0], keys[1])
        self.assertIn("t1 = div(a, b)", keys[1][0])
        self.assertEqual(keys[1][1], (1,))


if __name__ == "__main__":
    unittest.main()
