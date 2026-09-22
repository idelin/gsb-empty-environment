import pathlib
import sys
import unittest

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent.parent))

from stacklang import (
    compile_source, validate, VM, execute, disassemble,
    LexError, ParseError, CompileError, ValidationError, RuntimeErr,
    BudgetExceeded,
)
from stacklang.bytecode import (
    BytecodeModule, assemble, Instr, OP_BY_NAME,
)


def run_src(source, budget=None, env=None):
    machine = VM(compile_source(source), env=env)
    machine.run(budget=budget)
    return machine


class ExecutionTests(unittest.TestCase):
    def test_arithmetic_precedence(self):
        env, _ = execute("let a = 2 + 3 * 4;")
        self.assertEqual(env["a"], 14)
        env, _ = execute("let a = (2 + 3) * 4;")
        self.assertEqual(env["a"], 20)

    def test_integer_division_truncates_toward_zero(self):
        env, _ = execute("let a = -7 / 2; let b = 7 / -2; let c = -7 / -2;")
        self.assertEqual((env["a"], env["b"], env["c"]), (-3, -3, 3))
        env, _ = execute("let m = -7 % 2;")
        self.assertEqual(env["m"], -1)

    def test_unary_and_comparisons(self):
        env, _ = execute("let a = -(1 - 1); let b = !(3 < 2); let c = 5 != 6;")
        self.assertEqual((env["a"], env["b"], env["c"]), (0, True, True))
        with self.assertRaises(RuntimeErr):
            execute("let bad = -true;")

    def test_if_else_and_while(self):
        src = """
        let i = 0;
        let even = 0;
        while (i < 10) {
          if (i % 2 == 0) { even = even + 1; } else { }
          i = i + 1;
        }
        """
        env, _ = execute(src)
        self.assertEqual((env["i"], env["even"]), (10, 5))

    def test_reassignment(self):
        env, _ = execute("let x = 1; x = x + 41;")
        self.assertEqual(env["x"], 42)

    def test_nested_blocks_and_scope_is_flat(self):
        src = "let x = 1; { { x = x + 1; } }"
        env, _ = execute(src)
        self.assertEqual(env["x"], 2)


class BreakTests(unittest.TestCase):
    def test_single_break(self):
        src = """
        let i = 0; let acc = 0;
        while (i < 10) {
          if (i == 4) { break; }
          acc = acc + i;
          i = i + 1;
        }
        """
        env, _ = execute(src)
        self.assertEqual((env["i"], env["acc"]), (4, 6))

    def test_nested_break_lands_on_correct_level(self):
        src = """
        let sum = 0;
        let i = 0;
        while (i < 3) {
          let j = 0;
          while (j < 5) {
            if (j == 2) { break; }
            sum = sum + i * 10 + j;
            j = j + 1;
          }
          if (i == 1) { break; }
          i = i + 1;
        }
        """
        env, _ = execute(src)
        # i=0: 0+1; i=1: 10+11 then outer break
        self.assertEqual(env["sum"], 22)
        self.assertEqual(env["i"], 1)

    def test_break_outside_loop_is_compile_error(self):
        with self.assertRaises(CompileError):
            compile_source("break;")
        with self.assertRaises(CompileError):
            compile_source("if (true) { break; }")

    def test_break_inside_if_inside_loop_is_allowed(self):
        env, _ = execute("let i = 0; while (i < 3) { if (i == 1) { break; } i = i + 1; }")
        self.assertEqual(env["i"], 1)


class ShortCircuitTests(unittest.TestCase):
    def test_right_branch_not_evaluated_when_skipped(self):
        env, _ = execute("let a = false and (1 / 0 == 0);")
        self.assertIs(env["a"], False)
        env, _ = execute("let b = true or (1 / 0 == 0);")
        self.assertIs(env["b"], True)

    def test_right_branch_evaluated_when_needed(self):
        env, _ = execute("let c = true and 42; let d = false or 7;")
        self.assertEqual((env["c"], env["d"]), (42, 7))

    def test_dangerous_right_branch_still_runs_without_short_circuit(self):
        with self.assertRaises(RuntimeErr):
            execute("let e = true and (1 / 0 == 0);")
        with self.assertRaises(RuntimeErr):
            execute("let e = false or (1 / 0 == 0);")

    def test_nested_short_circuit(self):
        env, _ = execute("let v = false or (true and 5) or (1 / 0 == 0);")
        self.assertEqual(env["v"], 5)
        env, _ = execute("let v = true and false or 9;")
        self.assertEqual(env["v"], 9)


class RuntimeErrorTests(unittest.TestCase):
    def test_division_by_zero_has_source_span(self):
        try:
            execute("let x = 1 / 0;")
        except RuntimeErr as exc:
            self.assertIsNotNone(exc.span)
            self.assertEqual((exc.span.start, exc.span.end), (8, 13))
        else:
            self.fail("expected RuntimeErr")

    def test_undefined_variable(self):
        with self.assertRaises(RuntimeErr):
            execute("missing")

    def test_division_by_zero_under_short_circuit_span(self):
        try:
            execute("let z = true and 1 / 0;")
        except RuntimeErr as exc:
            # "let z = true and " is 17 bytes, so `1` begins at byte 17
            self.assertEqual(exc.span.start, 17)
        else:
            self.fail("expected RuntimeErr")


class ValidatorTests(unittest.TestCase):
    def _balanced_branch_module(self):
        # 0 PUSH_TRUE | 1 JF 13 | 6 PUSH_TRUE | 7 POP | 8 JUMP 15 |
        # 13 PUSH_TRUE | 14 POP | 15 HALT
        return assemble([
            Instr(OP_BY_NAME["PUSH_TRUE"]),
            Instr(OP_BY_NAME["JF"], 13),
            Instr(OP_BY_NAME["PUSH_TRUE"]),
            Instr(OP_BY_NAME["POP"]),
            Instr(OP_BY_NAME["JUMP"], 15),
            Instr(OP_BY_NAME["PUSH_TRUE"]),
            Instr(OP_BY_NAME["POP"]),
            Instr(OP_BY_NAME["HALT"]),
        ])

    def test_valid_module_passes(self):
        self.assertIs(validate(self._balanced_branch_module()), True)
        self.assertIs(validate(compile_source("let x = 1; while (x < 3) { x = x + 1; }")), True)

    def test_jump_into_middle_of_instruction_rejected(self):
        module = self._balanced_branch_module()
        code = bytearray(module.code)
        code[2:6] = (2).to_bytes(4, "big")  # byte 2 is inside JF's operand
        with self.assertRaises(ValidationError) as ctx:
            validate(BytecodeModule(code, module.consts, module.names, module.spans))
        self.assertIn("middle of an instruction", str(ctx.exception))

    def test_jump_past_end_rejected(self):
        module = self._balanced_branch_module()
        code = bytearray(module.code)
        code[2:6] = (16).to_bytes(4, "big")
        with self.assertRaises(ValidationError):
            validate(BytecodeModule(code, module.consts, module.names, module.spans))

    def test_jump_to_operand_byte_in_compiled_program_rejected(self):
        module = compile_source("let x = 1; if (x == 1) { x = 2; } else { x = 3; } x")
        push_const = module.code.index(OP_BY_NAME["PUSH_CONST"])
        jf = module.code.index(OP_BY_NAME["JF"])
        code = bytearray(module.code)
        code[jf + 1:jf + 5] = (push_const + 1).to_bytes(4, "big")
        broken = BytecodeModule(code, module.consts, module.names, module.spans)
        with self.assertRaises(ValidationError):
            validate(broken)
        with self.assertRaises(ValidationError):
            VM(broken)

    def test_stack_depth_mismatch_at_join_rejected(self):
        # PUSH one value, then JUMP back to entry: join sees depth 0 and 1.
        module = assemble([
            Instr(OP_BY_NAME["PUSH_TRUE"]),
            Instr(OP_BY_NAME["JUMP"], 0),
            Instr(OP_BY_NAME["HALT"]),
        ])
        with self.assertRaises(ValidationError) as ctx:
            validate(module)
        self.assertIn("depth mismatch", str(ctx.exception))

    def test_stack_underflow_rejected(self):
        module = assemble([Instr(OP_BY_NAME["POP"]), Instr(OP_BY_NAME["HALT"])])
        with self.assertRaises(ValidationError) as ctx:
            validate(module)
        self.assertIn("underflow", str(ctx.exception))

    def test_terminating_depth_must_be_zero(self):
        module = assemble([Instr(OP_BY_NAME["PUSH_TRUE"]), Instr(OP_BY_NAME["HALT"])])
        with self.assertRaises(ValidationError):
            validate(module)

    def test_unknown_opcode_and_truncation_rejected(self):
        with self.assertRaises(ValidationError):
            validate(BytecodeModule(bytes([0xEE])))
        with self.assertRaises(ValidationError):
            validate(BytecodeModule(bytes([OP_BY_NAME["PUSH_CONST"], 0x00])))
        with self.assertRaises(ValidationError):
            validate(BytecodeModule(b""))

    def test_operand_index_out_of_range_rejected(self):
        module = assemble([Instr(OP_BY_NAME["PUSH_CONST"], 5),
                           Instr(OP_BY_NAME["POP"]),
                           Instr(OP_BY_NAME["HALT"])], consts=[1])
        with self.assertRaises(ValidationError):
            validate(module)

    def test_all_supported_programs_pass_validation(self):
        sources = pathlib.Path(__file__).resolve().parent.parent.joinpath("examples").glob("*.sl")
        for path in sources:
            with self.subTest(path=path.name):
                validate(compile_source(path.read_text(encoding="utf-8")))


class UTF8SpanTests(unittest.TestCase):
    def test_lex_error_offset_is_utf8_byte_offset(self):
        source = "# 中文注释\nlet @ = 1;"
        # comment "# 中文注释" = 1 + 1 + 4*3 = 14 bytes, then "\n" + "let " = 5 => '@' at 19
        with self.assertRaises(LexError) as ctx:
            compile_source(source)
        self.assertEqual(ctx.exception.span.start, 19)

    def test_parse_error_has_byte_span(self):
        with self.assertRaises(ParseError) as ctx:
            compile_source("let = 3;")
        self.assertEqual(ctx.exception.span.start, 4)

    def test_identifier_byte_span_in_source(self):
        source = "# 中文\nanswer"
        # "#"=1, space=1, 2 CJK chars = 6 bytes, newline=1 -> name begins at 9
        try:
            execute(source)
        except RuntimeErr as exc:
            self.assertEqual((exc.span.start, exc.span.end), (9, 15))
        else:
            self.fail("expected undefined variable RuntimeErr")


class SingleStepAndBudgetTests(unittest.TestCase):
    def test_step_trace(self):
        vm = VM(compile_source("let x = 3; x = x * x;"))
        names = []
        while not vm.halted:
            names.append(vm.step())
        self.assertEqual(names[-1], "HALT")
        self.assertEqual(vm.env["x"], 9)
        self.assertEqual(vm.steps, len(names) - 1)

    def test_step_after_halt_is_error(self):
        vm = VM(compile_source(""))
        vm.run()
        with self.assertRaises(RuntimeErr):
            vm.step()

    def test_budget_pauses_and_resumes(self):
        vm = VM(compile_source("let n = 0; while (true) { n = n + 1; }"))
        with self.assertRaises(BudgetExceeded):
            vm.run(budget=10)
        self.assertEqual(vm.steps, 10)
        self.assertFalse(vm.halted)
        with self.assertRaises(BudgetExceeded):
            vm.run(budget=10)
        self.assertEqual(vm.steps, 20)
        self.assertEqual(vm.env["n"], 2)

    def test_budget_allows_completion(self):
        env, steps = execute("let x = 0; while (x < 50) { x = x + 1; }", budget=100000)
        self.assertEqual(env["x"], 50)
        self.assertGreater(steps, 50)


class DisassemblyTests(unittest.TestCase):
    def test_disassemble_lists_offsets(self):
        text = disassemble(compile_source("let x = 1;"))
        self.assertIn("HALT", text)
        self.assertIn("STORE", text)
        self.assertIn("; x", text)


if __name__ == "__main__":
    unittest.main(verbosity=2)
