"""boolsearch 的 unittest 测试（仅标准库）。"""

import unittest

from boolsearch import (
    And,
    Evidence,
    Index,
    Not,
    Or,
    QuerySyntaxError,
    Term,
    parse,
    tokenize,
)


def build_index() -> Index:
    idx = Index()
    idx.add_document("d1", {
        "title": "Python 入门",
        "body": "入门很简单，易学。",
        "tags": "python tutorial",
    })
    idx.add_document("d2", {
        "title": "Rust 指南",
        "body": "Rust is fast and safe. 没有 python。",
        "tags": "rust guide",
    })
    idx.add_document("d3", {
        "title": "Python vs Rust",
        "body": "对比 Python 与 Rust 的性能。",
        "tags": "python rust",
        # 故意没有 summary 字段
    })
    idx.add_document("d4", {
        "title": "空摘要示例",
        "body": "only body text",
        "summary": "",   # 显式为空串，必须与“字段缺失”区分开
    })
    return idx


class TokenizerTests(unittest.TestCase):
    def test_basic_lowercase_and_offsets(self):
        toks = tokenize("Hello, World!")
        self.assertEqual([t[0] for t in toks], ["hello", "world"])
        self.assertEqual(toks[0][1:], (0, 5))
        self.assertEqual(toks[1][1:], (7, 12))

    def test_casefold_and_underscore_digits(self):
        self.assertEqual([t[0] for t in tokenize("HTTP_200 ABC")],
                         ["http_200", "abc"])
        # casefold 是 Unicode 全大小写折叠：ß → ss
        self.assertEqual(tokenize("Straße")[0][0], "strasse")

    def test_cjk_each_char_is_token(self):
        self.assertEqual([t[0] for t in tokenize("入门")], ["入", "门"])
        toks = tokenize("学Python")
        self.assertEqual([t[0] for t in toks], ["学", "python"])
        self.assertEqual(toks[1][1:], (1, 7))

    def test_punctuation_is_separator(self):
        self.assertEqual([t[0] for t in tokenize("a-b.c:d()e")],
                         ["a", "b", "c", "d", "e"])


class ParserPrecedenceTests(unittest.TestCase):
    def test_not_or_precedence(self):
        # NOT A OR B 必须解析为 (NOT A) OR B
        self.assertEqual(parse("NOT a OR b"), parse("(NOT a) OR b"))
        self.assertNotEqual(parse("NOT a OR b"), parse("NOT (a OR b)"))

    def test_and_binds_tighter_than_or(self):
        self.assertEqual(parse("a OR b AND c"), parse("a OR (b AND c)"))

    def test_not_binds_tighter_than_and(self):
        self.assertEqual(parse("a AND NOT b"), parse("a AND (NOT b)"))

    def test_parentheses_change_grouping(self):
        self.assertEqual(parse("NOT (a OR b)"),
                         Not(Or(Term("a"), Term("b"))))

    def test_double_not(self):
        self.assertEqual(parse("NOT NOT a"), Not(Not(Term("a"))))

    def test_field_qualification_ast(self):
        self.assertEqual(parse("title:Python"), Term("python", field="title"))
        self.assertEqual(parse("a AND title:b"),
                         And(Term("a"), Term("b", field="title")))

    def test_lowercase_keywords_are_terms(self):
        # AND/OR/NOT 仅大写为运算符；小写是普通词项
        self.assertEqual(parse("and"), Term("and"))
        self.assertEqual(parse("a AND and"), And(Term("a"), Term("and")))

    def test_syntax_errors(self):
        # 无隐式 AND：相邻词项是语法错误
        for bad in ["", "AND", "a AND", "a OR)", "field:", "((a)", "a b",
                    "and or not"]:
            with self.subTest(bad=bad):
                with self.assertRaises(QuerySyntaxError):
                    parse(bad)


class SearchTests(unittest.TestCase):
    def setUp(self):
        self.idx = build_index()

    def test_and_or_not_basic(self):
        self.assertEqual(self.idx.search("python AND rust").doc_ids, ["d2", "d3"])
        self.assertEqual(self.idx.search("python OR rust").doc_ids,
                         ["d1", "d2", "d3"])
        self.assertEqual(self.idx.search("NOT python").doc_ids, ["d4"])

    def test_not_a_or_b_defined_precedence(self):
        # (NOT python) OR rust：d4 由否定命中，d2/d3 由 rust 命中
        self.assertEqual(self.idx.search("NOT python OR rust").doc_ids,
                         ["d2", "d3", "d4"])

    def test_not_parens(self):
        # NOT (python OR rust)：两者都不含的文档
        self.assertEqual(self.idx.search("NOT (python OR rust)").doc_ids, ["d4"])

    def test_field_qualification(self):
        self.assertEqual(self.idx.search("title:python").doc_ids, ["d1", "d3"])
        self.assertEqual(self.idx.search("tags:python").doc_ids, ["d1", "d3"])
        self.assertEqual(self.idx.search("body:python").doc_ids, ["d2", "d3"])

    def test_unqualified_term_matches_any_field(self):
        self.assertEqual(self.idx.search("python").doc_ids, ["d1", "d2", "d3"])

    def test_field_name_case_sensitive(self):
        # 字段名精确匹配：文档没有 "Title" 字段 → 空结果（不是错误）
        self.assertEqual(self.idx.search("Title:python").doc_ids, [])

    def test_missing_field_is_not_empty_string(self):
        # d3 缺 summary；d4 的 summary 是空串。两者都不命中任何词项查询
        self.assertEqual(self.idx.search("summary:body").doc_ids, [])
        doc_d3 = self.idx._docs["d3"]
        doc_d4 = self.idx._docs["d4"]
        self.assertNotIn("summary", doc_d3.fields)       # 字段缺失
        self.assertIn("summary", doc_d4.fields)          # 字段存在
        self.assertEqual(doc_d4.fields["summary"], "")   # 但值为空串
        # 缺失与空串是两种状态，且都不产生分词、不参与命中
        self.assertIsNone(self.idx._tokens["d3"].get("summary"))
        self.assertEqual(self.idx._tokens["d4"]["summary"], [])

    def test_pure_negation(self):
        # 纯否定查询：全集减去含词项文档
        self.assertEqual(self.idx.search("NOT 入").doc_ids, ["d2", "d3", "d4"])

    def test_delete_removes_from_universe(self):
        self.idx.delete_document("d2")
        self.assertNotIn("d2", self.idx.universe)
        self.assertEqual(self.idx.search("rust").doc_ids, ["d3"])
        # 删除后 NOT 的补集也不再包含 d2
        self.assertEqual(self.idx.search("NOT python").doc_ids, ["d4"])
        self.assertEqual(self.idx.search("NOT (python OR rust)").doc_ids, ["d4"])
        with self.assertRaises(KeyError):
            self.idx.delete_document("d2")

    def test_duplicate_doc_id_rejected(self):
        with self.assertRaises(ValueError):
            self.idx.add_document("d1", {"body": "dup"})


class EvidenceTests(unittest.TestCase):
    def setUp(self):
        self.idx = build_index()

    def test_evidence_points_at_source(self):
        result = self.idx.search("title:python")
        ev = result.evidence["d1"]
        self.assertEqual(len(ev), 1)
        e = ev[0]
        self.assertIsInstance(e, Evidence)
        self.assertEqual((e.field, e.term), ("title", "python"))
        self.assertEqual("Python 入门"[e.start:e.end], "Python")
        self.assertIn("【Python】", e.snippet)

    def test_evidence_cjk_snippet(self):
        result = self.idx.search("入")
        evs = [e for e in result.evidence["d1"] if e.field == "title"]
        self.assertEqual(evs[0].term, "入")
        self.assertEqual("Python 入门"[evs[0].start:evs[0].end], "入")

    def test_evidence_and_collects_both_sides(self):
        result = self.idx.search("python AND rust")
        terms = {(e.field, e.term) for e in result.evidence["d3"]}
        self.assertIn(("tags", "python"), terms)
        self.assertIn(("tags", "rust"), terms)

    def test_negation_match_has_no_positive_evidence(self):
        # d4 仅靠 NOT 命中，证据列表为空，但文档确实出现在结果中
        result = self.idx.search("NOT python")
        self.assertIn("d4", result.doc_ids)
        self.assertEqual(result.evidence["d4"], [])

    def test_or_evidence_only_from_matching_branch(self):
        result = self.idx.search("python OR rust")
        d1_terms = {e.term for e in result.evidence["d1"]}
        self.assertIn("python", d1_terms)
        self.assertNotIn("rust", d1_terms)


if __name__ == "__main__":
    unittest.main(verbosity=2)
