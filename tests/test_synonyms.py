import unittest

from syno import SynonymStore


class SynonymStoreTest(unittest.TestCase):
    def test_word_group_is_clique_undirected(self):
        st = SynonymStore()
        st.add_word_group(["car", "automobile", "vehicle"], 0.7)
        self.assertEqual(set(st.neighbors("car")), {"automobile", "vehicle"})
        self.assertEqual(st.edge_weight("automobile", "car"), 0.7)
        self.assertEqual(st.edge_weight("vehicle", "automobile"), 0.7)

    def test_pair_is_bidirectional(self):
        st = SynonymStore()
        st.add_word_pair("fast", "quick", 0.6)
        self.assertEqual(st.edge_weight("fast", "quick"), 0.6)
        self.assertEqual(st.edge_weight("quick", "fast"), 0.6)

    def test_duplicate_pair_keeps_single_max_edge(self):
        st = SynonymStore()
        st.add_word_pair("a", "b", 0.3)
        st.add_word_group(["a", "b"], 0.9)
        self.assertEqual(st.edge_weight("a", "b"), 0.9)
        self.assertEqual(len(st.neighbors("a")), 1)

    def test_invalid_weight(self):
        st = SynonymStore()
        with self.assertRaises(ValueError):
            st.add_word_pair("x", "y", 0.0)
        with self.assertRaises(ValueError):
            st.add_word_pair("x", "y", 1.2)

    def test_phrase_group_kept_separate_from_word_layer(self):
        st = SynonymStore()
        st.add_phrase_group(["kicked the bucket", "died"], 0.8)
        # died 是短语组中的单词成员，但不会产生任何单词层边
        self.assertEqual(st.neighbors("died"), {})
        self.assertEqual(st.neighbors("bucket"), {})
        groups = st.phrase_groups()
        self.assertEqual(len(groups), 1)
        self.assertEqual(
            [p.tokens for p in groups[0].phrases],
            [("kicked", "the", "bucket"), ("died",)],
        )

    def test_phrase_group_requires_multiword_member(self):
        st = SynonymStore()
        with self.assertRaises(ValueError):
            st.add_phrase_group(["died", "passed"], 0.8)

    def test_phrase_iter_longest_first_left_to_right(self):
        st = SynonymStore()
        st.add_phrase_group(["new york", "nyc"], 0.9)
        st.add_phrase_group(["york city", "yc"], 0.9)
        hits = list(st.iter_phrase_alternatives(["new", "york", "city"]))
        # 位置 0 最长匹配 new york；位置 2 的 york 已被覆盖，york city 不再命中
        self.assertEqual(len(hits), 1)
        start, end, gid, w, plist = hits[0]
        self.assertEqual((start, end), (0, 2))

    def test_cjk_phrase(self):
        st = SynonymStore()
        st.add_phrase_group(["机器学习", "人工智能"], 0.75)
        hits = list(st.iter_phrase_alternatives(list("我在机器学习")))
        self.assertEqual(len(hits), 1)
        start, end, *_ = hits[0]
        self.assertEqual((start, end), (2, 6))


if __name__ == "__main__":
    unittest.main()
