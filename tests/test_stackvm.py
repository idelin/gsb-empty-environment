import unittest

from stackvm import (
    BudgetExhausted,
    Ins,
    Machine,
    VMError,
    VerifyError,
    assemble,
    compile_program,
    isa,
    parse,
    run_source,
    tokenize,
    verify,
)


class ArithmeticTests(unittest.TestCase):
    def test_precedence_and_parens(self):
        self.assertEqual(run_source("print 2 + 3 * 4;")[0], [14])
        self.assertEqual(run_source("print (2 + 3) * 4;")[0], [20])
        self.assertEqual(run_source("print -5 + 2;")[0], [-3])
        self.assertEqual(run_source("print 7 / 2;")[0], [3])
        self.assertEqual(run_source("print -7 / 2;")[0], [-3])
        self.assertEqual(run_source("print not 0;")[0], [1])
        self.assertEqual(run_source("print not 3;")[0], [0])

    def test_comparisons(self):
        self.assertEqual(run_source("print 3 < 4;")[0], [1])
        self.assertEqual(run_source("print 3 <= 3;")[0], [1])
        self.assertEqual(run_source("print 4 != 4;")[0], [0])

    def test_division_by_zero(self):
        with self.assertRaises(VMError):
            run_source("print 1 / 0;")


class ControlFlowTests(unittest.TestCase):
    def test_if_else_both_branches(self):
        src_taken = "let x = 7; if x > 5 { print 1; } else { print 2; }"
        src_not = "let x = 3; if x > 5 { print 1; } else { print 2; }"
        self.assertEqual(run_source(src_taken)[0], [1])
        self.assertEqual(run_source(src_not)[0], [2])

    def test_while_sum(self):
        src = """
        let i = 1;
        let sum = 0;
        while i <= 10 {
            sum = sum + i;
            i = i + 1;
        }
        print sum;
        """
        self.assertEqual(run_source(src)[0], [55])

    def test_nested_break_hits_inner_loop_only(self):
        # 内层循环在 j==3 时跳出; 外层跑 5 次, 每次 j 都累加到 3。
        src = """
        let i = 0;
        let total = 0;
        while i < 5 {
            let j = 0;
            while true {
                j = j + 1;
                if j == 3 { break; }
            }
            total = total + j;
            i = i + 1;
        }
        print total;
        """
        self.assertEqual(run_source(src)[0], [15])

    def test_break_outer_loop(self):
        src = """
        let i = 0;
        while true {
            i = i + 1;
            if i == 4 { break; }
        }
        print i;
        """
        self.assertEqual(run_source(src)[0], [4])

    def test_break_outside_loop_is_compile_error(self):
        with self.assertRaises(Exception):
            compile_program(parse("break;"))


class ShortCircuitTests(unittest.TestCase):
    def test_and_skips_right_when_falsy(self):
        # 右支含除零: 被短路则不会执行, 也不会报错
        out, machine = run_source("print 0 and 1 / 0;")
        self.assertEqual(out, [0])

    def test_or_skips_right_when_truthy(self):
        out, _ = run_source("print 1 or 1 / 0;")
        self.assertEqual(out, [1])

    def test_logic_result_values(self):
        self.assertEqual(run_source("print 1 and 2;")[0], [2])
        self.assertEqual(run_source("print 0 or 7;")[0], [7])
        self.assertEqual(run_source("print 2 or 7;")[0], [2])
        self.assertEqual(run_source("print 0 and 7;")[0], [0])

    def test_right_branch_instructions_never_execute(self):
        src = "print 0 and 99;"
        code = compile_program(parse(src))
        verify(code)
        push99_pcs = {
            pc
            for pc, (info, operand, _nxt) in isa.decode_all(code).items()
            if info.name == "PUSH" and operand == 99
        }
        self.assertTrue(push99_pcs)  # 右支确实被编译出来了
        machine = Machine(code, trace=True)
        machine.run()
        self.assertEqual(machine.output, [0])
        self.assertTrue(push99_pcs.isdisjoint(set(machine.trace)))

    def test_right_branch_executes_when_not_short_circuited(self):
        src = "print 1 and 99;"
        code = compile_program(parse(src))
        machine = Machine(code, trace=True)
        machine.run()
        push99_pcs = {
            pc
            for pc, (info, operand, _nxt) in isa.decode_all(code).items()
            if info.name == "PUSH" and operand == 99
        }
        self.assertTrue(set(machine.trace) & push99_pcs)


class VerifierTests(unittest.TestCase):
    def test_compiled_programs_report_heights(self):
        code = compile_program(parse("let x = 1; if x { print x; }"))
        heights = verify(code)
        # 每条指令入口栈高度都非负, 入口和 HALT 处为 0
        self.assertEqual(heights[0], 0)
        self.assertTrue(all(h >= 0 for h in heights.values()))

    def test_jump_into_middle_of_instruction_rejected(self):
        code = compile_program(
            parse("if 1 { print 1; } print 2;")
        )
        decoded = isa.decode_all(code)
        jz_pc = next(pc for pc, (i, _, _) in decoded.items() if i.name == "JZ")
        push_pc = next(
            pc for pc, (i, _, _) in decoded.items() if i.name == "PUSH"
        )
        bad = bytearray(code)
        target = push_pc + 1  # 落在 PUSH(5 字节)指令的操作数中间
        bad[jz_pc + 1 : jz_pc + 3] = target.to_bytes(2, "big")
        with self.assertRaises(VerifyError):
            verify(bytes(bad))

    def test_jump_out_of_range_rejected(self):
        code = compile_program(parse("if 1 { print 1; }"))
        decoded = isa.decode_all(code)
        jz_pc = next(pc for pc, (i, _, _) in decoded.items() if i.name == "JZ")
        bad = bytearray(code)
        target = len(code) + 10
        bad[jz_pc + 1 : jz_pc + 3] = target.to_bytes(2, "big")
        with self.assertRaises(VerifyError):
            verify(bytes(bad))

    def test_inconsistent_stack_height_rejected(self):
        # PUSH 0; JZ L; PUSH 1; L: HALT
        # L 经 JZ 到达时栈高 0, 顺序到达时栈高 1 -> 不一致
        instructions = [
            Ins("PUSH", 0),
            Ins("JZ", "L"),
            Ins("PUSH", 1),
            Ins("LABEL", "L"),
            Ins("HALT"),
        ]
        code, _ = assemble(instructions)
        with self.assertRaises(VerifyError):
            verify(code)

    def test_stack_underflow_rejected(self):
        # 只有 ADD: 需要两个值, 栈却是空的
        code = isa.encode("ADD") + isa.encode("HALT")
        with self.assertRaises(VerifyError):
            verify(code)

    def test_falling_off_the_end_rejected(self):
        code = isa.encode("PUSH", 1)  # 没有 HALT
        with self.assertRaises(VerifyError):
            verify(code)

    def test_bad_opcode_rejected(self):
        with self.assertRaises(VerifyError):
            verify(bytes([0xFF]))


class MachineTests(unittest.TestCase):
    def test_single_stepping(self):
        code = compile_program(parse("print 1 + 2;"))
        machine = Machine(code)
        self.assertTrue(machine.step())  # PUSH 1
        self.assertEqual(machine.stack, [1])
        self.assertTrue(machine.step())  # PUSH 2
        self.assertEqual(machine.stack, [1, 2])
        self.assertTrue(machine.step())  # ADD
        self.assertEqual(machine.stack, [3])
        self.assertTrue(machine.step())  # PRINT
        self.assertEqual(machine.output, [3])
        self.assertFalse(machine.step())  # HALT
        self.assertTrue(machine.halted)

    def test_budget_stops_infinite_loop(self):
        code = compile_program(parse("while true { }"))
        machine = Machine(code, budget=100)
        with self.assertRaises(BudgetExhausted):
            machine.run()
        self.assertEqual(machine.steps, 100)
        self.assertFalse(machine.halted)

    def test_budget_allows_completion(self):
        code = compile_program(parse("print 40 + 2;"))
        machine = Machine(code, budget=1_000)
        self.assertEqual(machine.run(), [42])
        self.assertTrue(machine.steps < 1_000)


class SourceSpanTests(unittest.TestCase):
    def test_tokens_use_utf8_byte_ranges(self):
        src = "# 中文注释\nlet 甲 = 1;\nprint 甲;"
        raw = src.encode("utf-8")
        for token in tokenize(src):
            self.assertEqual(
                raw[token.start : token.end].decode("utf-8"), token.text
            )

    def test_multibyte_identifier_runs(self):
        src = "let 变量 = 5;\nprint 变量;"
        self.assertEqual(run_source(src)[0], [5])
        # “变量” 每个字符 3 字节; 验证区间是字节而非字符计数
        ident = next(t for t in tokenize(src) if t.text == "变量")
        self.assertEqual(ident.end - ident.start, 6)


if __name__ == "__main__":
    unittest.main(verbosity=2)
