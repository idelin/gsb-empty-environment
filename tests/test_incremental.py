"""宏更新后的局部重编译与旧诊断查询。"""
import unittest

from posmap.intervals import Span
from posmap.session import Session


PROGRAM = """let a = 2;
let b = DOUBLE(a);
let c = SQUARE(a);
print b + c / (a - 2);
"""

MACROS = {
    "DOUBLE": "DOUBLE(x) = x + x",
    "SQUARE": "SQUARE(x) = x * x",
}


def slash_span():
    start = PROGRAM.encode("utf-8").index(b"/")
    return Span("program", start, start + 1)


class TestIncremental(unittest.TestCase):
    def test_local_recompile_and_old_diagnostic(self):
        s = Session(PROGRAM, MACROS)
        _, diag = s.run()
        self.assertIsNotNone(diag)
        self.assertEqual(diag.message, "division by zero")
        ex0 = s.explain(diag)
        self.assertEqual(ex0.best.span, slash_span())
        old_halt_pc = len(s._snapshots[0].instrs) - 1

        report = s.update_macro("SQUARE", "SQUARE(x) = (x + 1) * (x + 1)")
        self.assertTrue(report.changed)
        self.assertEqual(report.recompiled_statements, [2])
        self.assertGreater(report.touched_blocks, 0)
        self.assertGreater(report.shifted_blocks, 0)
        new_halt_pc = len(s._snapshots[1].instrs) - 1
        self.assertNotEqual(old_halt_pc, new_halt_pc)

        # 旧诊断仍按旧版本快照解释：新坐标不套旧源文
        ex_old = s.explain(diag)
        self.assertEqual(ex_old.version, 0)
        self.assertEqual(ex_old.best.span, ex0.best.span)

        # 新版本重新运行：诊断版本号更新，定位仍正确
        _, diag2 = s.run()
        self.assertEqual(diag2.version, 1)
        ex_new = s.explain(diag2)
        self.assertEqual(ex_new.version, 1)
        self.assertEqual(ex_new.best.span, slash_span())

    def test_touched_blocks_exact(self):
        s = Session(PROGRAM, MACROS)
        report = s.update_macro("SQUARE", "SQUARE(x) = (x + 1) * (x + 1)")
        chunk = s._chunks[2]
        self.assertEqual(report.touched_blocks,
                         len(chunk.m1) + len(chunk.m2))

    def test_noop_update(self):
        s = Session(PROGRAM, MACROS)
        report = s.update_macro("DOUBLE", "DOUBLE(x) = x + x")
        self.assertFalse(report.changed)
        self.assertEqual(report.touched_blocks, 0)
        self.assertEqual(s.version, 0)

    def test_transitive_dependency(self):
        program = "let a = 3;\nlet b = WRAP(a);\nprint b;\n"
        macros = {
            "WRAP": "WRAP(x) = DOUBLE(x) + 1",
            "DOUBLE": "DOUBLE(x) = x + x",
        }
        s = Session(program, macros)
        out, _ = s.run()
        self.assertEqual(out, [7])
        report = s.update_macro("DOUBLE", "DOUBLE(x) = x * 10")
        self.assertEqual(report.recompiled_statements, [1])
        out2, _ = s.run()
        self.assertEqual(out2, [31])

    def test_unknown_version_rejected(self):
        s = Session(PROGRAM, MACROS)
        with self.assertRaises(KeyError):
            s.explain_pc(0, version=99)

    def test_failed_update_rolls_back(self):
        s = Session(PROGRAM, MACROS)
        with self.assertRaises(Exception):
            s.update_macro("SQUARE", "SQUARE(x) = x *")
        self.assertEqual(s.version, 0)
        _, diag = s.run()
        self.assertIsNotNone(diag)
        self.assertEqual(s.explain(diag).best.span, slash_span())


if __name__ == "__main__":
    unittest.main()
