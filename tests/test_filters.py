import unittest

from topk import Document, Field


def doc(doc_id, **fields):
    return Document(doc_id=doc_id, fields=fields)


class TestFilters(unittest.TestCase):
    def test_comparison_ops(self):
        d = doc(1, score=80, lang="zh")
        self.assertTrue((Field("score") >= 80)(d))
        self.assertTrue((Field("score") < 90)(d))
        self.assertTrue((Field("lang") == "zh")(d))
        self.assertTrue((Field("lang") != "en")(d))
        self.assertFalse((Field("score") > 80)(d))

    def test_null_semantics(self):
        d = doc(1, score=None)
        self.assertTrue(Field("score").is_null()(d))
        self.assertTrue((Field("score") == None)(d))  # noqa: E711
        self.assertFalse(Field("score").not_null()(d))
        # null 不参与大小比较
        self.assertFalse((Field("score") > 0)(d))
        # 缺失字段视为 null
        self.assertTrue(Field("missing").is_null()(d))

    def test_isin(self):
        d = doc(1, lang="zh")
        self.assertTrue(Field("lang").isin(["zh", "en"])(d))
        self.assertFalse(Field("lang").isin(["en", "ja"])(d))

    def test_contains_token_with_unified_tokenization(self):
        d = doc(1, body="使用ＴＯＰＫ算法做归并排序")
        # 查询词与文档走同一分词流程：全角/大小写不敏感
        self.assertTrue(Field("body").contains_token("topk")(d))
        self.assertTrue(Field("body").contains_token("归")(d))
        self.assertFalse(Field("body").contains_token("检索")(d))
        self.assertFalse(Field("body").contains_token("！！")(d))

    def test_boolean_composition(self):
        d = doc(1, score=80, lang="zh")
        f = (Field("lang") == "zh") & (Field("score") >= 60)
        self.assertTrue(f(d))
        self.assertTrue((~(Field("lang") == "en"))(d))
        self.assertTrue(((Field("lang") == "en") | (Field("score") == 80))(d))


if __name__ == "__main__":
    unittest.main()
