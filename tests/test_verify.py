import unittest

from tensorfusion import Program, Tensor, verify_gradients
from tensorfusion.interp import Stats, _exec_reduce
from tensorfusion.program import Op
from tensorfusion.verify import Verdict, compare_tensors


def grad_program():
    program = Program(inputs=["a", "b", "c"])
    t1 = program.add("a", "b")
    t2 = program.mul(t1, "c")
    t3 = program.div(t2, "b")
    out = program.reduce_sum(t3)
    return program, out


def grad_env():
    return {
        "a": Tensor.from_values([1.0, 2.0, 3.0, 4.0, 5.0, 6.0], (2, 3), name="a"),
        "b": Tensor.from_values([0.5, 1.5, 2.0], (3,), name="b"),
        "c": Tensor.from_values([2.0, 3.0, 4.0, 5.0, 6.0, 7.0], (2, 3), name="c"),
    }


class TestGradientConsistency(unittest.TestCase):
    def test_forward_and_gradients_bitwise_after_fusion(self):
        program, out = grad_program()
        report = verify_gradients(program, grad_env(), ["a", "b", "c"], out)
        self.assertTrue(report.ok())
        for cmp in report.forward.values():
            self.assertIs(cmp.verdict, Verdict.BITWISE)
        for cmp in report.gradients.values():
            self.assertIs(cmp.verdict, Verdict.BITWISE)

    def test_finite_difference_independent_check(self):
        program, out = grad_program()
        report = verify_gradients(program, grad_env(), ["a", "b", "c"], out)
        for var, (ok, err) in report.finite_difference.items():
            self.assertTrue(ok, f"{var}: finite difference err {err}")


class TestReductionHonesty(unittest.TestCase):
    def test_reordered_reduction_is_close_not_bitwise(self):
        env = {"x": Tensor.from_values([1.0, 1e-16, -1.0, 1e-16], (4,), name="x")}
        op = Op(kind="reduce_sum", result="s", args=("x",))
        fwd, rev = dict(env), dict(env)
        _exec_reduce(op, fwd, Stats(), order="forward")
        _exec_reduce(op, rev, Stats(), order="reverse")
        cmp = compare_tensors(fwd["s"], rev["s"])
        self.assertIs(cmp.verdict, Verdict.CLOSE)
        self.assertGreater(cmp.max_abs, 0.0)
        self.assertNotEqual(cmp.verdict, Verdict.BITWISE)
        self.assertIn("not claiming bitwise", cmp.note)

    def test_same_order_reduction_is_bitwise(self):
        env = {"x": Tensor.from_values([1.0, 1e-16, -1.0, 1e-16], (4,), name="x")}
        op = Op(kind="reduce_sum", result="s", args=("x",))
        a, b = dict(env), dict(env)
        _exec_reduce(op, a, Stats())
        _exec_reduce(op, b, Stats())
        self.assertIs(compare_tensors(a["s"], b["s"]).verdict, Verdict.BITWISE)

    def test_negative_zero_and_nan_bitwise_distinction(self):
        x = Tensor.from_values([-0.0, float("nan")], (2,))
        y = Tensor.from_values([0.0, float("nan")], (2,))
        cmp = compare_tensors(x, y)
        self.assertIs(cmp.verdict, Verdict.CLOSE)
        z = Tensor.from_values([-0.0, float("nan")], (2,))
        self.assertIs(compare_tensors(x, z).verdict, Verdict.BITWISE)


if __name__ == "__main__":
    unittest.main()
