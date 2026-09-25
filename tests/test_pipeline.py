"""端到端流水线：原文 -> 宏展开 -> 字节码 -> 诊断回溯。"""
import unittest

from posmap.intervals import Span
from posmap.session import Session


PROGRAM = """# 注释：宏演示
let 甲 = 3;
let b = SQUARE(甲) + 1;
print b / (甲 - 3);
"""

MACROS = {
    "SQUARE": "SQUARE(x) = x * x",
    "DOUBLE": "DOUBLE(x) = x + x",
}


def byte_span_of(program, needle):
    data = program.encode("utf-8")
    start = data.index(needle.encode("utf-8"))
    return Span("program", start, start + len(needle.encode("utf-8")))


class TestPipeline(unittest.TestCase):
    def setUp(self):
        self.session = Session(PROGRAM, MACROS)

    def test_expanded_text(self):
        self.assertIn("let b = 甲 * 甲 + 1 ;", self.session.expanded_text)

    def test_run_output(self):
        s = Session("let x = 2;\nprint x * 3 + 1;\nprint -x + 5;\n", {})
        out, diag = s.run()
        self.assertIsNone(diag)
        self.assertEqual(out, [7, 3])

    def test_division_by_zero_diagnostic(self):
        _, diag = self.session.run()
        self.assertIsNotNone(diag)
        self.assertEqual(diag.message, "division by zero")
        ex = self.session.explain(diag)
        self.assertIsNotNone(ex.best)
        self.assertEqual(ex.best.role, "user")
        # UTF-8 字节区间：'/' 的字节偏移（前面有多字节字符与注释）
        self.assertEqual(ex.best.span, byte_span_of(PROGRAM, "/"))

    def test_macro_call_maps_multiple_instructions(self):
        snap = self.session._snapshots[0]
        call_span = byte_span_of(PROGRAM, "SQUARE(甲)")
        data = PROGRAM.encode("utf-8")
        arg_start = data.index("甲".encode("utf-8"), data.index(b"SQUARE"))
        arg_span = Span("program", arg_start, arg_start + 3)

        mul_pcs = [i for i, ins in enumerate(snap.instrs) if ins.op == "MUL"]
        self.assertEqual(len(mul_pcs), 1)
        ex_mul = self.session.explain_pc(mul_pcs[0])
        # 宏体生成的 '*'：最合适用户片段是调用处，且附定义
        self.assertEqual(ex_mul.best.role, "call_site")
        self.assertEqual(ex_mul.best.span, call_span)
        roles = {c.role for c in ex_mul.candidates}
        self.assertIn("definition", roles)
        def_cands = [c for c in ex_mul.candidates if c.role == "definition"]
        self.assertEqual(def_cands[0].span.doc, "macro:SQUARE")

        # 同一宏调用展开出的多条指令都回到调用范围内
        load_pcs = [
            i for i, ins in enumerate(snap.instrs)
            if ins.op == "LOAD" and ins.arg == "甲"
        ]
        self.assertEqual(len(load_pcs), 2 + 1)  # 宏内两次 + print 中一次
        for pc in load_pcs[:2]:
            ex = self.session.explain_pc(pc)
            self.assertEqual(ex.best.role, "user")
            self.assertEqual(ex.best.span, arg_span)
            self.assertTrue(call_span.contains(ex.best.span))

    def test_halt_has_no_source(self):
        snap = self.session._snapshots[0]
        halt_pc = len(snap.instrs) - 1
        self.assertEqual(snap.instrs[halt_pc].op, "HALT")
        ex = self.session.explain_pc(halt_pc)
        self.assertEqual(ex.candidates, [])
        self.assertIsNone(ex.best)
        self.assertIn("不伪造", ex.best_reason)

    def test_composed_matches_layered_for_every_pc(self):
        snap = self.session._snapshots[0]
        for pc in range(len(snap.instrs)):
            composed = {
                (b.dst, b.tag)
                for b in snap.composed.targets(Span("code", pc, pc + 1))
            }
            layered = {
                (b.dst, b.tag) for b in self.session.layered_lookup(pc)
            }
            self.assertEqual(composed, layered, f"pc={pc}")

    def test_undefined_variable(self):
        src = "print 不存在;"
        s = Session(src, {})
        _, diag = s.run()
        self.assertIsNotNone(diag)
        ex = s.explain(diag)
        self.assertEqual(ex.best.role, "user")
        data = src.encode("utf-8")
        self.assertEqual(data[ex.best.span.start:ex.best.span.end],
                         "不存在".encode("utf-8"))

    def test_candidates_have_reasons(self):
        _, diag = self.session.run()
        ex = self.session.explain(diag)
        self.assertTrue(ex.candidates)
        for cand in ex.candidates:
            self.assertTrue(cand.reason)
        self.assertTrue(ex.best_reason)


if __name__ == "__main__":
    unittest.main()
