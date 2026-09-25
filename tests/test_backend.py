"""IR validation, interpreter and transform tests."""

import unittest

from minilang.compiler import compile
from minilang.interp import run
from minilang.ir import Instr, IRProgram, remove_instructions, validate
from minilang.parser import parse
from minilang.transforms import (
    AlgebraicPeephole,
    ConstantFold,
    ConstProp,
    UnsafeZeroAddPeephole,
)


def compile_source(source: str) -> IRProgram:
    return compile(parse(source))


class ValidateTest(unittest.TestCase):
    def test_out_of_range_jump_target_detected(self):
        prog = IRProgram([Instr("JZ", 5), Instr("HALT")], 0)
        errors = validate(prog)
        self.assertTrue(any("out of range" in e for e in errors))

    def test_negative_jump_target_detected(self):
        prog = IRProgram([Instr("JMP", -1), Instr("HALT")], 0)
        self.assertTrue(any("out of range" in e for e in validate(prog)))

    def test_missing_halt_detected(self):
        prog = IRProgram([Instr("CONST", 1), Instr("OUTPUT")], 0)
        errors = validate(prog)
        self.assertTrue(any("HALT" in e for e in errors))
        self.assertTrue(any("fall off the end" in e for e in errors))

    def test_wrong_arg_type_detected(self):
        prog = IRProgram([Instr("CONST", "x"), Instr("HALT")], 0)
        self.assertTrue(validate(prog))

    def test_compiled_program_is_valid(self):
        prog = compile_source("let x = input(0); while x > 0 { x = x - 1; } output x;")
        self.assertEqual(validate(prog), [])


class InterpTest(unittest.TestCase):
    def test_arithmetic(self):
        prog = compile_source("let x = 2 + 3 * 4; output x;")
        self.assertEqual(run(prog, ()).outputs, (14,))

    def test_while_loop_sum(self):
        prog = compile_source(
            "let n = input(0); let s = 0; while n > 0 { s = s + n; n = n - 1; } output s;"
        )
        self.assertEqual(run(prog, (10,)).outputs, (55,))
        self.assertEqual(run(prog, (0,)).outputs, (0,))
        self.assertEqual(run(prog, (-3,)).outputs, (0,))

    def test_if_else(self):
        prog = compile_source(
            "let x = input(0); if x < 0 { output 0 - x; } else { output x; }"
        )
        self.assertEqual(run(prog, (-7,)).outputs, (7,))
        self.assertEqual(run(prog, (7,)).outputs, (7,))

    def test_division_truncates_toward_zero(self):
        prog = compile_source("let a = input(0); let b = input(1); output a / b; output a % b;")
        self.assertEqual(run(prog, (-7, 2)).outputs, (-3, -1))
        self.assertEqual(run(prog, (7, -2)).outputs, (-3, 1))

    def test_division_by_zero_traps(self):
        prog = compile_source("output 1 / 0;")
        result = run(prog, ())
        self.assertEqual(result.error, "division by zero")

    def test_undefined_variable_traps(self):
        prog = compile_source("output missing;")
        self.assertIn("undefined variable", run(prog, ()).error)

    def test_input_out_of_range_traps(self):
        prog = compile_source("output input(3);")
        self.assertIn("out of range", run(prog, (1,)).error)

    def test_infinite_loop_stopped_by_fuel(self):
        prog = compile_source("while 1 { }")
        result = run(prog, (), fuel=1000)
        self.assertIn("fuel exhausted", result.error)

    def test_no_eval_or_exec_used(self):
        import pathlib

        root = pathlib.Path(__file__).resolve().parent.parent / "minilang"
        for path in root.glob("*.py"):
            text = path.read_text(encoding="utf-8")
            self.assertNotIn("eval(", text, path.name)
            self.assertNotIn("exec(", text, path.name)


class TransformTest(unittest.TestCase):
    def test_constant_fold_remaps_jump_targets(self):
        # JMP 目标在折叠点之后，折叠后必须重映射
        prog = IRProgram(
            [
                Instr("JMP", 4),          # 0 -> 跳到 CONST 1
                Instr("CONST", 2),        # 1 ┐ 被折叠
                Instr("CONST", 3),        # 2 ┘
                Instr("ADD"),             # 3
                Instr("CONST", 1),        # 4 <- 跳转目标
                Instr("OUTPUT"),          # 5
                Instr("HALT"),            # 6
            ],
            0,
        )
        folded = ConstantFold().apply(prog)
        self.assertEqual(validate(folded), [])
        self.assertEqual(folded.instrs[0], Instr("JMP", 2))
        self.assertEqual(run(folded, ()).outputs, (1,))

    def test_constant_fold_skips_division_by_zero(self):
        prog = IRProgram(
            [Instr("CONST", 1), Instr("CONST", 0), Instr("DIV"), Instr("OUTPUT"), Instr("HALT")],
            0,
        )
        folded = ConstantFold().apply(prog)
        self.assertEqual(len(folded.instrs), 5)  # 未折叠，保留运行时陷阱
        self.assertEqual(run(folded, ()).error, "division by zero")

    def test_unsafe_peephole_breaks_branch_targets(self):
        prog = compile_source(
            "let n = input(0); let s = 0; while n > 0 { s = s + n; n = n - 1; }"
            "let a = s + (3 - 3); if a > 0 { output a; }"
        )
        prog = ConstantFold().apply(prog)  # 制造 CONST 0, ADD 模式
        broken = UnsafeZeroAddPeephole().apply(prog)
        errors = validate(broken)
        self.assertTrue(any("out of range" in e for e in errors))

    def test_safe_peephole_keeps_program_valid(self):
        prog = compile_source(
            "let n = input(0); let s = 0; while n > 0 { s = s + n; n = n - 1; }"
            "let a = s + (3 - 3); if a > 0 { output a; }"
        )
        prog = ConstantFold().apply(prog)
        fixed = AlgebraicPeephole().apply(prog)
        self.assertEqual(validate(fixed), [])
        for n in range(-2, 21):
            self.assertEqual(
                run(prog, (n,)).observable(), run(fixed, (n,)).observable()
            )

    def test_const_prop_replaces_load_with_const(self):
        prog = compile_source("let a = 5; let b = a + 1; output b;")
        out = ConstProp().apply(prog)
        self.assertEqual(validate(out), [])
        self.assertEqual(run(out, ()).outputs, (6,))
        # 一对一替换，指令数不变
        self.assertEqual(len(out.instrs), len(prog.instrs))

    def test_const_prop_does_not_cross_jump_targets(self):
        # 循环内 LOAD 不得被循环外的常量替换
        prog = compile_source(
            "let k = 1; let n = input(0); while n > 0 { k = k + 1; n = n - 1; } output k;"
        )
        out = ConstProp().apply(prog)
        self.assertEqual(run(out, (3,)).outputs, (4,))

    def test_remove_instructions_refuses_jump_targets(self):
        prog = IRProgram([Instr("JMP", 1), Instr("HALT")], 0)
        with self.assertRaises(ValueError):
            remove_instructions(prog.instrs, {1})


if __name__ == "__main__":
    unittest.main()
