"""unittest 测试套件：解析器/字节区间、验证器、解释器、变换、流水线回滚、差分验证。"""
import unittest

from diffcheck import DEFAULT_DOMAIN, differential
from interp import run
from ir import format_program, parse, validate
from main import SAMPLE, TRANSFORMS
from pipeline import run_pipeline
from transforms import buggy_label_cleanup, fold_constants


class TestParserSpans(unittest.TestCase):
    def test_utf8_byte_spans(self):
        source = "# 注释：多字节字符\nconst x, 5\nprint x\n"
        prog = parse(source)
        data = source.encode("utf-8")
        self.assertEqual(len(prog), 2)
        for ins, text in zip(prog, ("const x, 5", "print x")):
            self.assertIsNotNone(ins.span)
            self.assertEqual(data[ins.span.start:ins.span.end].decode("utf-8"), text)

    def test_span_accounts_for_multibyte_prefix(self):
        source = "# 中文注释\nnop\n"
        (ins,) = parse(source)
        prefix = "# 中文注释\n".encode("utf-8")
        self.assertEqual(ins.span.start, len(prefix))
        self.assertEqual(ins.span.end, len(prefix) + len("nop".encode("utf-8")))

    def test_roundtrip(self):
        prog = parse(SAMPLE)
        self.assertEqual([i.text() for i in parse(format_program(prog))],
                         [i.text() for i in prog])


class TestValidator(unittest.TestCase):
    def test_unknown_branch_target_rejected(self):
        prog = parse("brnz x, nowhere\nhalt\n")
        errors = validate(prog)
        self.assertTrue(any("nowhere" in e and "不存在" in e for e in errors))

    def test_duplicate_label_rejected(self):
        prog = parse("loop:\nnop\nloop:\nhalt\n")
        self.assertTrue(any("重复标签" in e for e in validate(prog)))

    def test_arity_and_int_checked(self):
        prog = parse("const x\nadd y, z, nope, extra\nconst k, abc\n")
        errors = validate(prog)
        self.assertTrue(any("操作数个数" in e for e in errors))
        self.assertTrue(any("整数字面量" in e for e in errors))

    def test_sample_program_valid(self):
        self.assertEqual(validate(parse(SAMPLE)), [])


class TestInterpreter(unittest.TestCase):
    def test_sample_outputs(self):
        out, _ = run(parse(SAMPLE), inputs=(3, 4))
        self.assertEqual(out, [14, 9, 3, 2, 1])

    def test_loop_and_branch(self):
        prog = parse("const c, 5\nconst one, 1\nloop:\nsub c, c, one\nbrnz c, loop\nprint c\nhalt\n")
        out, regs = run(prog)
        self.assertEqual(out, [0])
        self.assertEqual(regs["c"], 0)


class TestTransforms(unittest.TestCase):
    def test_fold_constants(self):
        prog = parse("const a, 2\nconst b, 3\nadd c, a, b\nprint c\nhalt\n")
        folded = fold_constants(prog)
        self.assertIn(("const", ("c", "5")), [(i.op, i.args) for i in folded])
        out, _ = run(folded)
        self.assertEqual(out, [5])

    def test_buggy_transform_breaks_branch_target(self):
        prog = parse(SAMPLE)
        broken = buggy_label_cleanup(prog)
        errors = validate(broken)
        self.assertTrue(any("分支目标" in e and "loop" in e for e in errors))


class TestPipeline(unittest.TestCase):
    def setUp(self):
        self.original = parse(SAMPLE)
        self.final, self.reports = run_pipeline(self.original, TRANSFORMS)

    def test_every_step_records_before_after_and_check(self):
        self.assertEqual(len(self.reports), len(TRANSFORMS))
        for rep in self.reports:
            self.assertIsNotNone(rep.before)
            self.assertIsNotNone(rep.after)
            self.assertIsNotNone(rep.candidate)
            self.assertIsInstance(rep.errors, list)

    def test_bad_step_intercepted_and_rolled_back(self):
        bad = self.reports[3]
        self.assertFalse(bad.applied)
        self.assertTrue(any("分支目标" in e for e in bad.errors))
        self.assertEqual([i.text() for i in bad.after],
                         [i.text() for i in bad.before])  # 回滚到变换前

    def test_later_safe_steps_still_run(self):
        last = self.reports[-1]
        self.assertTrue(last.applied)
        self.assertLess(len(last.after), len(last.before))  # 不可达代码被删除
        self.assertNotIn("print junk", [i.text() for i in self.final])

    def test_final_program_valid(self):
        self.assertEqual(validate(self.final), [])


class TestDifferential(unittest.TestCase):
    def test_pipeline_result_equivalent_on_finite_domain(self):
        original = parse(SAMPLE)
        final, _ = run_pipeline(original, TRANSFORMS)
        result = differential(original, final)
        self.assertTrue(result.equivalent_on_domain)
        self.assertEqual(result.total, len(DEFAULT_DOMAIN) ** 2)

    def test_detects_semantic_mismatch(self):
        a = parse("input x, 0\nprint x\nhalt\n")
        b = parse("input x, 0\nconst one, 1\nadd x, x, one\nprint x\nhalt\n")
        result = differential(a, b)
        self.assertFalse(result.equivalent_on_domain)
        self.assertGreater(len(result.mismatches), 0)

    def test_report_does_not_claim_universal_proof(self):
        original = parse(SAMPLE)
        final, _ = run_pipeline(original, TRANSFORMS)
        report = differential(original, final).report()
        self.assertIn("有限测试域", report)
        self.assertIn("并非全域等价证明", report)


if __name__ == "__main__":
    unittest.main()
