import unittest

from tensorfusion import (IllegalFusion, Program, Tensor, attempt_fuse,
                          compile_program, format_plan)
from tensorfusion.fuser import Kernel, ReduceUnit, StoreUnit, alias_evidence


def env_of(**tensors):
    for name, tensor in tensors.items():
        tensor.name = name
    return tensors


class TestLegalFusion(unittest.TestCase):
    def test_chain_fuses_into_single_kernel_with_broadcast_plan(self):
        program = Program(inputs=["a", "b"])
        t1 = program.add("a", "b")
        t2 = program.mul(t1, "b")
        program.neg(t2)
        env = env_of(a=Tensor.from_values([1, 2, 3, 4, 5, 6], (2, 3)),
                     b=Tensor.from_values([1, 2, 3], (3,)))
        fused = compile_program(program, env)
        self.assertEqual(len(fused.units), 1)
        kernel = fused.units[0]
        self.assertIsInstance(kernel, Kernel)
        self.assertEqual(len(kernel.steps), 3)
        self.assertEqual(kernel.shape, (2, 3))
        self.assertEqual(kernel.in_strides["b"], (0, 1))
        plan = format_plan(kernel)
        self.assertIn("for i0 in range(2):", plan)
        self.assertIn("for i1 in range(3):", plan)
        self.assertIn("b[1*i1]", plan)

    def test_reduction_is_a_barrier(self):
        program = Program(inputs=["a"])
        t1 = program.add("a", "a")
        s = program.reduce_sum(t1)
        program.mul(s, "a")
        env = env_of(a=Tensor.from_values([1, 2], (2,)))
        fused = compile_program(program, env)
        kinds = [u.kind for u in fused.units]
        self.assertEqual(kinds, ["kernel", "reduce_sum", "kernel"])

    def test_escaping_intermediate_splits_group(self):
        program = Program(inputs=["a", "b"])
        t1 = program.add("a", "b")
        t2 = program.mul(t1, "b")
        program.sub(t2, t1)
        env = env_of(a=Tensor.from_values([1, 2], (2,)),
                     b=Tensor.from_values([3, 4], (2,)))
        fused = compile_program(program, env)
        self.assertEqual(len(fused.kernels()), 2)
        self.assertEqual(fused.kernels()[0].result_name, t1)

    def test_safe_inplace_store_fuses(self):
        x = Tensor.from_values([1, 2, 3, 4], (2, 2), name="x")
        c = Tensor.from_values([10, 20, 30, 40], (2, 2), name="c")
        program = Program(inputs=["x", "c"])
        t1 = program.add("x", "c")
        program.store(t1, x)
        fused = compile_program(program, {"x": x, "c": c})
        self.assertEqual(len(fused.units), 1)
        self.assertIsNotNone(fused.units[0].store)


class TestIllegalFusionEvidence(unittest.TestCase):
    def test_alias_hazard_positions(self):
        x = Tensor.from_values([1, 2, 3, 4], (2, 2), name="x")
        xt = x.transpose()
        ev = alias_evidence(xt, x, (2, 2))
        self.assertIsNotNone(ev)
        self.assertEqual(ev.kind, "alias")
        self.assertEqual(ev.positions, (0, 1, 2, 3))
        self.assertEqual(alias_evidence(x, x, (2, 2)), None)

    def test_store_alias_raises_with_evidence(self):
        x = Tensor.from_values([1, 2, 3, 4], (2, 2), name="x")
        xt = x.transpose()
        c = Tensor.from_values([10, 20, 30, 40], (2, 2), name="c")
        program = Program(inputs=["x", "c"])
        t1 = program.add("x", "c")
        program.store(t1, xt)
        with self.assertRaises(IllegalFusion) as ctx:
            attempt_fuse(program, {"x": x, "c": c})
        kinds = [e.kind for e in ctx.exception.evidences]
        self.assertIn("alias", kinds)
        fused = compile_program(program, {"x": x, "c": c})
        self.assertIsInstance(fused.units[-1], StoreUnit)

    def test_version_dependency_evidence(self):
        x = Tensor.from_values([1, 2, 3], (3,), name="x")
        b = Tensor.from_values([4, 5, 6], (3,), name="b")
        d = Tensor.from_values([7, 8, 9], (3,), name="d")
        program = Program(inputs=["x", "b", "d"])
        t1 = program.add("x", "b")
        program.mutate("x", "d")
        program.mul(t1, "x")
        with self.assertRaises(IllegalFusion) as ctx:
            attempt_fuse(program, {"x": x, "b": b, "d": d})
        ev = ctx.exception.evidences[0]
        self.assertEqual(ev.kind, "version")
        self.assertIn("version 0 to 1", ev.detail)
        self.assertIn("t1 = add(x, b)", ev.detail)

    def test_reduction_barrier_evidence(self):
        a = Tensor.from_values([1, 2, 3, 4], (2, 2), name="a")
        program = Program(inputs=["a"])
        t1 = program.add("a", "a")
        s = program.reduce_sum(t1)
        program.mul(s, "a")
        with self.assertRaises(IllegalFusion) as ctx:
            attempt_fuse(program, {"a": a})
        self.assertEqual(ctx.exception.evidences[0].kind, "reduction")


if __name__ == "__main__":
    unittest.main()
