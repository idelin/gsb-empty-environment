"""Pipeline and differential-checker tests."""

import unittest

from minilang.compiler import compile
from minilang.diffcheck import DISCLAIMER, check_equivalent
from minilang.interp import run
from minilang.ir import Instr, IRProgram, validate
from minilang.parser import parse
from minilang.pipeline import run_pipeline
from minilang.transforms import (
    AlgebraicPeephole,
    ConstantFold,
    ConstProp,
    UnsafeZeroAddPeephole,
)

DEMO_SOURCE = """\
# 演示程序：累加 1..n；注释含多字节字符，验证字节区间
let n = input(0);
let zero = 0;
let sum = zero;
while n > 0 {
  sum = sum + n;
  n = n - 1;
}
let adjust = sum + (3 - 3);
if adjust > 0 {
  output adjust;
}
"""

DOMAIN = [(k,) for k in range(-2, 21)]


def demo_ir() -> IRProgram:
    return compile(parse(DEMO_SOURCE))


class PipelineTest(unittest.TestCase):
    def setUp(self):
        self.ir0 = demo_ir()
        self.result = run_pipeline(
            self.ir0,
            [ConstantFold(), UnsafeZeroAddPeephole(), ConstProp(), AlgebraicPeephole()],
            DOMAIN,
        )

    def test_every_step_records_before_and_after_ir(self):
        self.assertEqual(len(self.result.records), 4)
        for rec in self.result.records:
            self.assertIn("HALT", rec.before_ir)
            self.assertTrue(rec.after_ir)  # 坏步骤也保留了产生的前后 IR

    def test_branch_breaking_transform_is_intercepted(self):
        bad = self.result.records[1]
        self.assertIn("UNSAFE", bad.pass_name)
        self.assertFalse(bad.kept)
        self.assertTrue(any("out of range" in e for e in bad.structural_errors))
        self.assertIsNone(bad.diff_report)  # 结构失败则不再做差分

    def test_pipeline_continues_after_rollback(self):
        # 坏步骤被跳过后，后续安全步骤仍然运行并被保留
        self.assertEqual(
            self.result.kept_passes,
            ["constant-fold", "const-prop", "algebraic-peephole"],
        )
        self.assertEqual(len(self.result.rolled_back_passes), 1)

    def test_final_ir_is_valid_and_equivalent_on_domain(self):
        self.assertEqual(validate(self.result.final), [])
        report = check_equivalent(self.ir0, self.result.final, DOMAIN)
        self.assertTrue(report.equivalent, report.summary())

    def test_final_ir_actually_optimized(self):
        self.assertLess(len(self.result.final.instrs), len(self.ir0.instrs))

    def test_semantically_broken_transform_rolled_back_by_diffcheck(self):
        # 构造一个结构合法但语义错误的变换：把 ADD 换成 SUB
        class AddToSub:
            name = "add-to-sub (semantically wrong)"

            def apply(self, prog):
                return IRProgram(
                    [Instr("SUB") if i.op == "ADD" else i for i in prog.instrs],
                    prog.inputs,
                )

        result = run_pipeline(self.ir0, [AddToSub(), ConstantFold()], DOMAIN)
        bad = result.records[0]
        self.assertFalse(bad.kept)
        self.assertEqual(bad.structural_errors, [])  # 结构没问题
        self.assertIsNotNone(bad.diff_report)
        self.assertFalse(bad.diff_report.equivalent)
        # 后续步骤仍执行
        self.assertTrue(result.records[1].kept)


class DiffCheckTest(unittest.TestCase):
    def test_equivalent_programs_pass(self):
        ir0 = demo_ir()
        folded = ConstantFold().apply(ir0)
        report = check_equivalent(ir0, folded, DOMAIN)
        self.assertTrue(report.equivalent)
        self.assertEqual(report.domain_size, len(DOMAIN))

    def test_inequivalent_programs_fail_with_details(self):
        ir0 = demo_ir()
        broken = UnsafeZeroAddPeephole().apply(ConstantFold().apply(ir0))
        # 坏程序结构非法，但差分器仍能运行（解释器以 Trap 报告）
        report = check_equivalent(ir0, broken, DOMAIN)
        self.assertFalse(report.equivalent)
        self.assertTrue(report.mismatches)
        m = report.mismatches[0]
        self.assertNotEqual(m.before.observable(), m.after.observable())

    def test_report_does_not_claim_full_domain_proof(self):
        ir0 = demo_ir()
        report = check_equivalent(ir0, ConstantFold().apply(ir0), DOMAIN)
        summary = report.summary()
        self.assertIn(DISCLAIMER, summary)
        self.assertIn("有限测试域", summary)
        self.assertNotIn("全域证明成立", summary)

    def test_trap_states_are_compared(self):
        a = IRProgram([Instr("CONST", 1), Instr("CONST", 0), Instr("DIV"), Instr("HALT")], 0)
        b = IRProgram([Instr("CONST", 2), Instr("HALT")], 0)
        report = check_equivalent(a, b, [()])
        self.assertFalse(report.equivalent)  # 一个陷阱、一个正常，行为不同


if __name__ == "__main__":
    unittest.main()
