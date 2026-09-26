"""search_engine 的单元测试（Python 3.11+，仅标准库）。"""

import unittest

from search_engine import (
    And,
    Field,
    Not,
    Or,
    QuerySyntaxError,
    SearchEngine,
    Term,
    parse_query,
    tokenize,
)


def make_engine() -> SearchEngine:
    engine = SearchEngine()
    engine.add_document("d1", {
        "title": "Python 编程入门",
        "body": "Python 是一种编程语言，适合初学者。",
        "author": "张三",
    })
    engine.add_document("d2", {
        "title": "Java 与 Python 对比",
        "body": "Java 和 Python 都是流行语言。",
        "author": "李四",
    })
    engine.add_document("d3", {
        "title": "数据结构",
        "body": "介绍链表、树与图。",
        "author": "张三",
    })
    engine.add_document("d4", {
        "title": "空白标题",
        "body": "",  # 字段存在但为空串
        "author": "王五",
    })
    engine.add_document("d5", {
        "title": "没有正文和作者的文档",
        # 缺失 body 与 author 字段
    })
    return engine


def result_ids(engine: SearchEngine, query: str) -> list[str]:
    return [r.doc_id for r in engine.search(query)]


class TestTokenize(unittest.TestCase):
    def test_ascii_lowercased(self):
        self.assertEqual(tokenize("Hello World"), ["hello", "world"])

    def test_cjk_unigrams(self):
        self.assertEqual(tokenize("编程语言"), ["编", "程", "语", "言"])

    def test_mixed_and_separators(self):
        self.assertEqual(
            tokenize("Python3.11_编程，语言!"),
            ["python3", "11", "编", "程", "语", "言"],
        )

    def test_utf8_japanese_korean(self):
        self.assertEqual(tokenize("こんにちは"), ["こ", "ん", "に", "ち", "は"])
        self.assertEqual(tokenize("한국어"), ["한", "국", "어"])

    def test_empty_and_punctuation_only(self):
        self.assertEqual(tokenize(""), [])
        self.assertEqual(tokenize("，。！ "), [])


class TestParse(unittest.TestCase):
    def test_not_binds_tighter_than_or(self):
        # NOT A OR B 按定义解析为 (NOT A) OR B
        self.assertEqual(
            parse_query("NOT a OR b"),
            Or(Not(Term("a")), Term("b")),
        )

    def test_and_binds_tighter_than_or(self):
        self.assertEqual(
            parse_query("a OR b AND c"),
            Or(Term("a"), And(Term("b"), Term("c"))),
        )

    def test_parentheses_override(self):
        self.assertEqual(
            parse_query("(a OR b) AND c"),
            And(Or(Term("a"), Term("b")), Term("c")),
        )

    def test_implicit_and(self):
        self.assertEqual(parse_query("a b"), And(Term("a"), Term("b")))

    def test_keywords_case_insensitive(self):
        # 关键字大小写不敏感；词项原文保留（大小写归一在分词阶段完成）
        self.assertEqual(parse_query("a AnD B"), And(Term("a"), Term("B")))

    def test_field_qualifier(self):
        self.assertEqual(parse_query("title:python"), Field("title", Term("python")))

    def test_field_group(self):
        self.assertEqual(
            parse_query("title:(a OR b)"),
            Field("title", Or(Term("a"), Term("b"))),
        )

    def test_double_not(self):
        self.assertEqual(parse_query("NOT NOT a"), Not(Not(Term("a"))))

    def test_syntax_errors(self):
        for bad in ["", "   ", "a AND", "OR a", "(a OR b", "a)", "title:", "NOT"]:
            with self.assertRaises(QuerySyntaxError, msg=bad):
                parse_query(bad)


class TestBooleanSemantics(unittest.TestCase):
    def setUp(self):
        self.engine = make_engine()

    def test_and(self):
        self.assertEqual(result_ids(self.engine, "python AND 编程"), ["d1"])

    def test_or(self):
        self.assertEqual(result_ids(self.engine, "java OR 链表"), ["d2", "d3"])

    def test_not_a_or_b_precedence(self):
        # (NOT python) OR java：d1 含 python 被 NOT 排除，d2 靠 java 命中
        self.assertEqual(result_ids(self.engine, "NOT python OR java"), ["d2", "d3", "d4", "d5"])

    def test_parentheses_change_result(self):
        # NOT (python OR java)
        self.assertEqual(result_ids(self.engine, "NOT (python OR java)"), ["d3", "d4", "d5"])

    def test_pure_negative_query(self):
        self.assertEqual(result_ids(self.engine, "NOT python"), ["d3", "d4", "d5"])

    def test_double_negative(self):
        self.assertEqual(result_ids(self.engine, "NOT NOT python"), ["d1", "d2"])

    def test_implicit_and_query(self):
        self.assertEqual(result_ids(self.engine, "python 编程"), ["d1"])

    def test_term_case_insensitive(self):
        self.assertEqual(result_ids(self.engine, "PYTHON"), ["d1", "d2"])


class TestFieldQualifier(unittest.TestCase):
    def setUp(self):
        self.engine = make_engine()

    def test_field_match(self):
        self.assertEqual(result_ids(self.engine, "author:张三"), ["d1", "d3"])

    def test_field_no_cross_field_leak(self):
        # “编程”只出现在 d1 的 title/body，不在任何 author 字段
        self.assertEqual(result_ids(self.engine, "author:编程"), [])

    def test_field_group(self):
        self.assertEqual(result_ids(self.engine, "title:(java OR 数据)"), ["d2", "d3"])

    def test_missing_field_is_not_empty_string(self):
        # d5 缺失 author 字段：正向不匹配
        self.assertNotIn("d5", result_ids(self.engine, "author:张三"))
        # d4 的 body 是空串、d5 缺失 body：正向都不匹配
        self.assertEqual(result_ids(self.engine, "body:python"), ["d1", "d2"])
        # 但 NOT body:python 同时包含空串字段(d4)与缺失字段(d5)的文档
        self.assertEqual(
            result_ids(self.engine, "NOT body:python"), ["d3", "d4", "d5"]
        )

    def test_not_on_missing_field(self):
        # d5 没有 author 字段，NOT author:张三 应命中 d5
        self.assertEqual(
            result_ids(self.engine, "NOT author:张三"), ["d2", "d4", "d5"]
        )

    def test_unknown_field_matches_nothing(self):
        self.assertEqual(result_ids(self.engine, "nosuchfield:x"), [])
        self.assertEqual(
            result_ids(self.engine, "NOT nosuchfield:x"),
            ["d1", "d2", "d3", "d4", "d5"],
        )


class TestUniverseAndDelete(unittest.TestCase):
    def test_delete_removes_from_universe(self):
        engine = make_engine()
        self.assertEqual(engine.document_count(), 5)
        engine.delete_document("d3")
        self.assertEqual(engine.document_count(), 4)
        # 纯否定查询的补集不再包含已删除文档
        self.assertEqual(result_ids(engine, "NOT python"), ["d4", "d5"])
        # 已删除文档的正向命中也被移除
        self.assertEqual(result_ids(engine, "链表"), [])

    def test_delete_missing_raises(self):
        engine = make_engine()
        with self.assertRaises(KeyError):
            engine.delete_document("nope")

    def test_readd_replaces_document(self):
        engine = make_engine()
        engine.add_document("d1", {"title": "rust 编程"})
        self.assertEqual(result_ids(engine, "python"), ["d2"])
        self.assertEqual(result_ids(engine, "rust"), ["d1"])


class TestEvidence(unittest.TestCase):
    def setUp(self):
        self.engine = make_engine()

    def test_evidence_reports_field_term_count(self):
        (result,) = self.engine.search("title:python AND author:张三")
        self.assertEqual(result.doc_id, "d1")
        evidence = {(e.field, e.term): e.count for e in result.evidence}
        # “张三”按分词规则切成 张/三 两个单字词元
        self.assertEqual(
            evidence,
            {("title", "python"): 1, ("author", "张"): 1, ("author", "三"): 1},
        )

    def test_evidence_unfielded_lists_all_fields(self):
        results = {r.doc_id: r for r in self.engine.search("python")}
        fields_d1 = {(e.field, e.term) for e in results["d1"].evidence}
        self.assertEqual(fields_d1, {("title", "python"), ("body", "python")})

    def test_evidence_count_reflects_occurrences(self):
        results = {r.doc_id: r for r in self.engine.search("body:python")}
        counts = {(e.field, e.term): e.count for e in results["d2"].evidence}
        self.assertEqual(counts, {("body", "python"): 1})

    def test_pure_negative_has_no_positive_evidence(self):
        results = {r.doc_id: r for r in self.engine.search("NOT python")}
        self.assertEqual(results["d3"].evidence, [])

    def test_mixed_query_evidence_only_from_positive_branches(self):
        results = {r.doc_id: r for r in self.engine.search("NOT python OR java")}
        evidence = {(e.field, e.term) for e in results["d2"].evidence}
        self.assertEqual(evidence, {("title", "java"), ("body", "java")})


class TestCjkQueries(unittest.TestCase):
    def setUp(self):
        self.engine = make_engine()

    def test_cjk_multi_char_term_is_unigram_and(self):
        # “编程”切成 编/程 两个单字词元，按 AND 处理
        self.assertEqual(result_ids(self.engine, "编程"), ["d1"])

    def test_cjk_with_field(self):
        self.assertEqual(result_ids(self.engine, "author:张三"), ["d1", "d3"])
        self.assertEqual(result_ids(self.engine, "title:数据"), ["d3"])


if __name__ == "__main__":
    unittest.main(verbosity=2)
