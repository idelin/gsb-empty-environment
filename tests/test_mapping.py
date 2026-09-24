"""unittest suite for the multi-layer position mapping system.

Run from the repository root:  python3.11 -m unittest discover -s tests -v
"""
import random
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from posmap import lang
from posmap.intervals import Interval, Block, IntervalMap, combine_tags
from posmap.pipeline import (
    Pipeline,
    chained_source_intervals,
    composed_source_intervals,
)


def byte_span(source: str, needle: str) -> Interval:
    data = source.encode("utf-8")
    start = data.index(needle.encode("utf-8"))
    return Interval(start, start + len(needle.encode("utf-8")))


class IntervalTest(unittest.TestCase):
    def test_basics(self):
        a = Interval(0, 5)
        self.assertTrue(a.intersects(Interval(4, 9)))
        self.assertFalse(a.intersects(Interval(5, 9)))  # half-open
        self.assertTrue(a.contains(4))
        self.assertFalse(a.contains(5))
        self.assertEqual(a.shift(3), Interval(3, 8))
        self.assertEqual(a.union(Interval(2, 9)), Interval(0, 9))
        with self.assertRaises(ValueError):
            Interval(5, 4)


class ComposeAssociativityTest(unittest.TestCase):
    """Finite interval model: composition must be associative."""

    TAGS = ("source", "code", "macro_body", "generated")

    def random_map(self, rng, n, domain):
        blocks = []
        for _ in range(n):
            s1 = rng.randrange(domain)
            e1 = s1 + rng.randrange(1, 5)
            s2 = rng.randrange(domain)
            e2 = s2 + rng.randrange(1, 5)
            src = None if rng.random() < 0.25 else Interval(s1, e1)
            tag = rng.choice(self.TAGS)
            meta = (("k", rng.randrange(3)),) if rng.random() < 0.5 else ()
            blocks.append(Block(src, Interval(s2, e2), tag, meta))
        return IntervalMap(blocks)

    def test_associativity(self):
        rng = random.Random(20260924)
        for trial in range(500):
            m1 = self.random_map(rng, rng.randrange(0, 7), 12)
            m2 = self.random_map(rng, rng.randrange(0, 7), 12)
            m3 = self.random_map(rng, rng.randrange(0, 7), 12)
            left = m1.compose(m2).compose(m3)
            right = m1.compose(m2.compose(m3))
            self.assertEqual(
                left.blocks, right.blocks,
                f"associativity violated in trial {trial}",
            )

    def test_combine_tags_associative_and_commutative(self):
        for a in self.TAGS:
            for b in self.TAGS:
                self.assertEqual(combine_tags(a, b), combine_tags(b, a))
                for c in self.TAGS:
                    self.assertEqual(
                        combine_tags(combine_tags(a, b), c),
                        combine_tags(a, combine_tags(b, c)),
                    )


class ComposeRelationTest(unittest.TestCase):
    def test_one_to_many_preserved(self):
        # one source interval -> two expanded intervals -> two pcs
        m1 = IntervalMap([
            Block(Interval(0, 10), Interval(0, 5), "source"),
            Block(Interval(0, 10), Interval(5, 10), "source"),
        ])
        m2 = IntervalMap([
            Block(Interval(0, 5), Interval(0, 1), "code"),
            Block(Interval(5, 10), Interval(1, 2), "code"),
        ])
        composed = m1.compose(m2)
        dsts = sorted(b.dst for b in composed.forward(Interval(0, 10)))
        self.assertEqual(dsts, [Interval(0, 1), Interval(1, 2)])

    def test_overlapping_sources_preserved(self):
        m1 = IntervalMap([
            Block(Interval(0, 8), Interval(0, 4), "source"),
            Block(Interval(4, 12), Interval(2, 6), "source"),  # overlaps both sides
        ])
        hits = m1.forward(Interval(5, 6))
        self.assertEqual(len(hits), 2)  # both overlapping blocks returned

    def test_uncovered_becomes_generated(self):
        m1 = IntervalMap([Block(Interval(0, 4), Interval(0, 4), "source")])
        m2 = IntervalMap([
            Block(Interval(0, 4), Interval(0, 1), "code"),
            Block(Interval(10, 12), Interval(1, 2), "code"),  # no origin in A
        ])
        composed = m1.compose(m2)
        gen = [b for b in composed.blocks if b.src is None]
        self.assertEqual(len(gen), 1)
        self.assertEqual(gen[0].tag, "generated")
        self.assertEqual(gen[0].dst, Interval(1, 2))


class Utf8Test(unittest.TestCase):
    def test_byte_spans_with_multibyte_chars(self):
        src = "# 注释：中文注释\nlet 甲 = 7;\nprint(甲);\n"
        toks = lang.lex(src)
        data = src.encode("utf-8")
        let_tok = next(t for t in toks if t.text == "let")
        self.assertEqual(let_tok.span.start, data.index(b"let"))
        jia = next(t for t in toks if t.text == "甲")
        self.assertEqual(jia.span.start, data.index("甲".encode("utf-8")))
        self.assertEqual(jia.span.end - jia.span.start, 3)  # 3 UTF-8 bytes

    def test_multibyte_program_runs_and_maps(self):
        src = "# 中文注释\nlet 甲 = 7;\nprint(甲);\n"
        p = Pipeline(src)
        out, diag = p.run()
        self.assertIsNone(diag)
        self.assertEqual(out, [7])
        ver = p.versions[0]
        name_span = byte_span(src, "甲")
        hits = ver.map1.forward(name_span)
        self.assertTrue(any(b.src == name_span and b.tag == "source" for b in hits))


class MacroMappingTest(unittest.TestCase):
    SRC = (
        "# 宏示例\n"
        "macro twice(e) { e + e }\n"
        "let a = 3;\n"
        "print(twice(a));\n"
        "print(twice(7));\n"
    )

    def setUp(self):
        self.p = Pipeline(self.SRC)
        self.ver = self.p.versions[0]

    def test_one_to_many_from_definition(self):
        # the single '+' in the definition maps to two expanded intervals
        def_plus = byte_span(self.SRC, "+")
        hits = self.ver.map1.forward(def_plus)
        self.assertEqual(len(hits), 2)
        self.assertEqual(len({b.dst for b in hits}), 2)
        # and through composition it reaches (among others) the two ADDs
        composed_hits = self.ver.composed.forward(def_plus)
        add_pcs = {i for i, ins in enumerate(self.ver.instrs) if ins.op == "ADD"}
        self.assertTrue(add_pcs)
        self.assertTrue(add_pcs.issubset({b.dst.start for b in composed_hits}))
        for pc in add_pcs:
            back = self.ver.composed.backward(Interval(pc, pc + 1))
            self.assertTrue(any(b.src == def_plus and b.tag == "macro_body"
                                for b in back))

    def test_call_maps_back_to_call_range_with_definition(self):
        call_span = byte_span(self.SRC, "twice(a)")
        def_span = self.ver.macros["twice"].span
        add_pc = next(i for i, ins in enumerate(self.ver.instrs) if ins.op == "ADD")
        diag = self.p.explain_raw(0, add_pc)
        macro_cands = [c for c in diag.candidates if c.tag == "macro_body"]
        self.assertTrue(macro_cands)
        for c in macro_cands:
            self.assertEqual(c.user_span, call_span)       # back to the call range
            self.assertEqual(c.def_span, def_span)         # definition attached
            self.assertEqual(c.macro, "twice")
        self.assertEqual(diag.best_span, call_span)
        self.assertEqual(diag.best_line, 4)

    def test_nested_macro_overlapping_sources(self):
        src = "macro twice(e) { e + e }\nlet a = 1;\nprint(twice(twice(a)));\n"
        p = Pipeline(src)
        ver = p.versions[0]
        self.assertEqual(ver.expanded,
                         "let a = 1 ;\nprint ( ( a ) + ( a ) ) + ( ( a ) + ( a ) ) ;")
        inner_call = byte_span(src, "twice(a)")
        outer_call = byte_span(src, "twice(twice(a))")
        self.assertTrue(inner_call.start > outer_call.start
                        and inner_call.end < outer_call.end)  # nested/overlapping
        # the single user token `a` fans out to four expanded fragments
        a_src = Interval(inner_call.start + len("twice("),
                         inner_call.start + len("twice(") + 1)
        self.assertEqual(src.encode("utf-8")[a_src.start:a_src.end], b"a")
        exact = [b for b in ver.map1.forward(a_src) if b.src == a_src]
        self.assertEqual(len(exact), 4)
        # overlapping source intervals are preserved by forward queries
        srcs = [b.src for b in ver.map1.forward(outer_call)]
        self.assertTrue(any(s1 != s2 and s1.intersects(s2)
                            for i, s1 in enumerate(srcs) for s2 in srcs[i + 1:]))
        # nested expansions: the composed map keeps both (overlapping) call
        # ranges for the outer ADD, and only the inner one for inner ADDs
        add_pcs = [i for i, ins in enumerate(ver.instrs) if ins.op == "ADD"]
        self.assertEqual(len(add_pcs), 3)

        def call_spans(pc):
            return {dict(b.meta).get("call")
                    for b in ver.composed.backward(Interval(pc, pc + 1))
                    if b.tag == "macro_body"}

        self.assertEqual(call_spans(add_pcs[-1]), {outer_call, inner_call})
        self.assertEqual(call_spans(add_pcs[0]), {inner_call})

    def test_generated_tokens_have_no_source(self):
        # the parens inserted around substituted arguments are generated
        expanded = self.ver.expanded.encode("utf-8")
        paren = expanded.index(b"(")
        hits = self.ver.map1.backward(Interval(paren, paren + 1))
        self.assertTrue(hits)
        self.assertTrue(all(b.src is None and b.tag == "generated" for b in hits))


class DiagnosticTest(unittest.TestCase):
    def test_division_by_zero_backtrace(self):
        src = (
            "macro safe_div(x, y) { x / y }\n"
            "let a = 10;\n"
            "let b = 0;\n"
            "print(safe_div(a, b));\n"
        )
        p = Pipeline(src)
        out, diag = p.run()
        self.assertEqual(out, [])
        self.assertEqual(diag.message, "division by zero")
        d = p.explain(diag)
        # all candidates returned, each with a reason
        tags = {c.tag for c in d.candidates}
        self.assertEqual(tags, {"macro_body", "source", "generated"})
        self.assertTrue(all(c.reason for c in d.candidates))
        # the most appropriate user fragment is the call range, def attached
        self.assertEqual(d.best_span, byte_span(src, "safe_div(a, b)"))
        self.assertEqual(d.best_line, 4)
        macro_cand = next(c for c in d.candidates if c.tag == "macro_body")
        self.assertEqual(macro_cand.def_span, p.versions[0].macros["safe_div"].span)
        self.assertEqual(macro_cand.snippet, "safe_div(a, b)")
        # generated candidates never carry a line number
        gen = next(c for c in d.candidates if c.tag == "generated")
        self.assertIsNone(gen.line)
        self.assertIsNone(gen.user_span)

    def test_plain_source_error(self):
        src = "let a = 1;\nprint(a / 0);\n"
        p = Pipeline(src)
        _, diag = p.run()
        d = p.explain(diag)
        self.assertEqual(d.best_span, byte_span(src, "a / 0"))
        self.assertEqual(d.best_line, 2)
        self.assertTrue(all(c.tag == "source" for c in d.candidates))

    def test_generated_instruction_has_no_fabricated_line(self):
        src = "let a = 1;\n1 + 2;\nprint(a);\n"
        p = Pipeline(src)
        ver = p.versions[0]
        out, diag = p.run()
        self.assertIsNone(diag)
        # HALT and the expression-statement POP are compiler-generated
        for pc, ins in enumerate(ver.instrs):
            if ins.op in ("HALT", "POP"):
                d = p.explain_raw(0, pc)
                self.assertIsNone(d.best_span)
                self.assertIsNone(d.best_line)
                self.assertTrue(all(c.tag == "generated" for c in d.candidates))
                self.assertTrue(all(c.line is None for c in d.candidates))
                self.assertIn("no line number is fabricated", d.best_reason)


class ConsistencyTest(unittest.TestCase):
    """Composed-map queries must agree with per-layer queries."""

    SOURCES = [
        "macro twice(e) { e + e }\nlet a = 3;\nprint(twice(a));\nprint(twice(7));\n",
        "macro safe_div(x, y) { x / y }\nlet a = 10;\nprint(safe_div(a, 0));\n",
        "macro twice(e) { e + e }\nprint(twice(twice(2)));\n",
        "let x = 5;\nx * x;\nprint(x);\n",
    ]

    def check_version(self, ver):
        for pc in range(len(ver.instrs)):
            self.assertEqual(
                chained_source_intervals(ver, pc),
                composed_source_intervals(ver, pc),
                f"version {ver.vid} pc {pc}",
            )

    def test_composed_matches_chained(self):
        for src in self.SOURCES:
            p = Pipeline(src)
            for ver in p.versions.values():
                self.check_version(ver)

    def test_composed_matches_chained_after_update(self):
        p = Pipeline(self.SOURCES[0])
        p.update_macro("twice", "e * 2 + e")
        for ver in p.versions.values():
            self.check_version(ver)


class LocalUpdateTest(unittest.TestCase):
    SRC = (
        "macro inc(x) { x + 1 }\n"
        "let a = 41;\n"
        "print(inc(a));\n"
        "print(a);\n"
    )

    def test_local_recompile_and_touched_blocks(self):
        p = Pipeline(self.SRC)
        out, diag = p.run()
        self.assertEqual((out, diag), ([42, 41], None))
        report = p.update_macro("inc", "x + 2")
        # only the statement that used `inc` was recompiled
        self.assertEqual(report.recompiled_units, [2])
        # touched blocks = old unit blocks + new unit blocks (map1 + map2)
        self.assertEqual(report.touched_blocks, (7 + 4) + (7 + 4))
        out, diag = p.run()
        self.assertEqual((out, diag), ([43, 41], None))
        # old version still runs with old semantics
        out0, diag0 = p.run(0)
        self.assertEqual((out0, diag0), ([42, 41], None))

    def test_unaffected_units_reused(self):
        p = Pipeline(self.SRC)
        before = p.versions[0].units
        p.update_macro("inc", "x + 2")
        after = p.versions[1].units
        # unaffected units keep their compiled payloads (no recompilation)
        self.assertIs(before[1].instrs, after[1].instrs)     # `let a = 41;`
        self.assertIs(before[1].map1_rel, after[1].map1_rel)
        self.assertIs(before[3].instrs, after[3].instrs)     # `print(a);`
        self.assertIs(before[3].expanded, after[3].expanded)
        self.assertIsNot(before[2].instrs, after[2].instrs)  # `print(inc(a));`

    def test_old_diagnostic_survives_and_new_coords_rejected(self):
        src = "macro d(x) { x / q }\nprint(d(1));\n"
        p = Pipeline(src)
        _, diag0 = p.run()
        self.assertEqual(diag0.message, "undefined variable 'q'")
        d0 = p.explain(diag0)
        self.assertEqual(d0.best_span, byte_span(src, "d(1)"))
        self.assertEqual(d0.best_line, 2)
        # update makes the bytecode longer; new pcs are invalid for v0
        p.update_macro("d", "x + 1 + 2 + 3 / q")
        _, diag1 = p.run()
        self.assertGreaterEqual(diag1.pc, len(p.versions[0].instrs))
        # old diagnostic still resolves against the old source, unchanged
        d0_again = p.explain(diag0)
        self.assertEqual(d0_again.best_span, d0.best_span)
        self.assertEqual(d0_again.best_line, 2)
        # new bytecode coordinates are never applied to the old source
        with self.assertRaises(ValueError):
            p.explain_raw(0, diag1.pc)

    def test_unknown_macro_update_rejected(self):
        p = Pipeline(self.SRC)
        with self.assertRaises(KeyError):
            p.update_macro("nope", "x")


if __name__ == "__main__":
    unittest.main()
