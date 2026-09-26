import unittest

from syno import QueryExpander, SynonymStore
from syno.query_graph import _build_adj, enumerate_simple_paths


class QueryGraphTest(unittest.TestCase):
    def test_original_keeps_highest_weight(self):
        st = SynonymStore()
        st.add_word_pair("a", "b", 0.5)
        g = QueryExpander(st, max_word_hops=2).expand("a")
        self.assertEqual(g.nodes["a"].weights_by_root[0], 1.0)
        self.assertLess(g.nodes["b"].weights_by_root[0], 1.0)
        self.assertTrue(g.nodes["a"].is_original)
        self.assertFalse(g.nodes["b"].is_original)

    def test_cycle_expanded_once(self):
        st = SynonymStore()
        st.add_word_group(["a", "b"], 0.8)
        g = QueryExpander(st, max_word_hops=5).expand("a")
        # 只创建 a、b 两个节点；b->a 是唯一一次回边，无 a->b->a... 展开
        self.assertEqual(set(g.nodes), {"a", "b"})
        self.assertEqual(g.back_edges, 1)
        self.assertFalse(g.truncated)

    def test_clique_back_edges_counted(self):
        st = SynonymStore()
        st.add_word_group(["a", "b", "c"], 0.8)
        g = QueryExpander(st, max_word_hops=5).expand("a")
        # 三角形无向 BFS：3 条无向边，其中 1 条非树边会从两端各遍历一次，
        # 加上第三个节点对根的回看，共 3 次被剪枝的回边遍历（无节点重复创建）
        self.assertEqual(len(g.edges), 3)
        self.assertEqual(g.back_edges, 3)
        self.assertEqual(set(g.nodes), {"a", "b", "c"})

    def test_hop_limit(self):
        st = SynonymStore()
        st.add_word_pair("a", "b", 0.8)
        st.add_word_pair("b", "c", 0.8)
        st.add_word_pair("c", "d", 0.8)
        g1 = QueryExpander(st, max_word_hops=1).expand("a")
        self.assertEqual(set(g1.nodes), {"a", "b"})
        g2 = QueryExpander(st, max_word_hops=2).expand("a")
        self.assertEqual(set(g2.nodes), {"a", "b", "c"})
        self.assertAlmostEqual(g2.nodes["c"].weights_by_root[0], 0.64, places=6)

    def test_weight_is_product_and_best_path_chosen(self):
        st = SynonymStore()
        st.add_word_pair("a", "x", 0.2)
        st.add_word_pair("x", "c", 0.2)
        st.add_word_pair("a", "b", 0.9)
        st.add_word_pair("b", "c", 0.9)
        g = QueryExpander(st, max_word_hops=2).expand("a")
        # a->c 有两条路径，取最大权 a->b->c = 0.81
        self.assertAlmostEqual(g.nodes["c"].weights_by_root[0], 0.81, places=6)
        self.assertEqual(g.nodes["c"].paths_by_root[0], ("a", "b", "c"))

    def test_shared_expansion_attributed_to_both_roots(self):
        st = SynonymStore()
        st.add_word_pair("a", "s", 0.8)
        st.add_word_pair("c", "s", 0.6)
        g = QueryExpander(st, max_word_hops=1).expand("a c")
        self.assertIn("s", g.shared)
        info = g.nodes["s"]
        self.assertEqual(sorted(info.roots), [0, 1])
        self.assertAlmostEqual(info.weights_by_root[0], 0.8)
        self.assertAlmostEqual(info.weights_by_root[1], 0.6)

    def test_node_budget_truncation_marked(self):
        st = SynonymStore()
        for i in range(6):
            st.add_word_pair(f"x{i}", f"x{i+1}", 0.9)
        g = QueryExpander(st, max_word_hops=5, node_budget=3, edge_budget=999).expand("x0")
        self.assertTrue(g.truncated)
        self.assertEqual(g.truncation, "node_budget")
        self.assertEqual(len(g.nodes), 3)

    def test_edge_budget_truncation_marked(self):
        st = SynonymStore()
        st.add_word_group(["a", "b", "c", "d"], 0.8)
        g = QueryExpander(st, max_word_hops=1, node_budget=64, edge_budget=2).expand("a")
        self.assertTrue(g.truncated)
        self.assertEqual(g.truncation, "edge_budget")

    def test_phrase_alternatives_not_split_into_word_edges(self):
        st = SynonymStore()
        st.add_phrase_group(["kicked the bucket", "died"], 0.8)
        g = QueryExpander(st).expand("kicked the bucket")
        # 单词层只有查询原词，没有 died 之类扩展词
        self.assertEqual(set(g.nodes), {"kicked", "the", "bucket"})
        self.assertEqual(len(g.phrases), 1)
        pa = g.phrases[0]
        self.assertEqual(pa.alternatives, [("kicked", "the", "bucket"), ("died",)])
        self.assertEqual(pa.is_original, [True, False])
        self.assertEqual([len(a) for a in pa.alternatives], [3, 1])

    def test_enumerate_simple_paths_rejects_large_graph(self):
        adj = {str(i): {} for i in range(65)}
        with self.assertRaises(ValueError):
            enumerate_simple_paths(adj, "0", "1", max_hops=3)

    def test_enumerate_simple_paths_on_small_cycle(self):
        st = SynonymStore()
        st.add_word_group(["a", "b"], 0.8)
        g = QueryExpander(st, max_word_hops=2).expand("a")
        adj = _build_adj(g.nodes, st)
        paths = enumerate_simple_paths(adj, "a", "b", max_hops=2)
        # 仅一条简单路径，没有 a->b->a->b 之类重复路径
        self.assertEqual(paths, [(("a", "b"), 0.8)])


if __name__ == "__main__":
    unittest.main()
