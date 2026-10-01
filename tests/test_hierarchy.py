import unittest

from k_anonymizer.hierarchy import Hierarchy, HierarchyError


def tree_hierarchy():
    parents = {}
    leaves = [str(a) for a in range(0, 40)]
    for a in range(0, 40):
        leaf = str(a)
        decade = f"{a // 10 * 10}-{a // 10 * 10 + 9}"
        parents.setdefault(leaf, []).append(decade)
        parents.setdefault(decade, []).append("*")
    return Hierarchy("age", leaves, parents)


def diamond_hierarchy():
    # d0 -> north -> metro 与 d0 -> metro 两条路径在 metro 收敛
    leaves = ["d0", "d1", "d2", "d3"]
    parents = {
        "d0": ["north", "metro"],
        "d1": ["north"],
        "d2": ["south"],
        "d3": ["south"],
        "north": ["metro"],
        "south": ["metro"],
    }
    return Hierarchy("city", leaves, parents)


class TestHierarchy(unittest.TestCase):
    def test_tree_generalization_levels(self):
        h = tree_hierarchy()
        self.assertEqual(h.generalize("23", 0), "23")
        self.assertEqual(h.generalize("23", 1), "20-29")
        self.assertEqual(h.generalize("23", 2), "*")
        self.assertEqual(h.max_level, 2)

    def test_diamond_converging_paths_deduped(self):
        h = diamond_hierarchy()
        # 两条路径收敛到 metro，去重后层级 2 祖先唯一
        self.assertEqual(h.generalize("d0", 1), "north")
        self.assertEqual(h.generalize("d0", 2), "metro")
        self.assertEqual(h.generalize("d3", 2), "metro")
        self.assertEqual(h.leaf_count["metro"], 4)
        self.assertEqual(h.leaf_count["north"], 2)

    def test_ambiguous_level_rejected(self):
        # x 在层级 1 有两个不同祖先 a、b，属歧义定义
        with self.assertRaises(HierarchyError):
            Hierarchy("f", ["x"], {"x": ["a", "b"], "a": ["*"], "b": ["*"]})

    def test_cycle_rejected(self):
        with self.assertRaises(HierarchyError):
            Hierarchy("f", ["x"], {"x": ["y"], "y": ["x"]})

    def test_unreachable_level_rejected(self):
        # y 没有父节点，叶子 x 只能到层级 1，但 y 作为叶子自身也要到层级 1
        with self.assertRaises(HierarchyError):
            Hierarchy("f", ["x", "y"], {"x": ["*"]})

    def test_unknown_value_and_bad_level(self):
        h = tree_hierarchy()
        with self.assertRaises(KeyError):
            h.generalize("999", 0)
        with self.assertRaises(ValueError):
            h.generalize("23", 3)
        with self.assertRaises(ValueError):
            h.generalize("23", -1)


if __name__ == "__main__":
    unittest.main()
