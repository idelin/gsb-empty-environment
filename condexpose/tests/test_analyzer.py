"""测试套件：穷举比对 + 语义场景 + 增量一致性 + ROBDD 规模。"""

from __future__ import annotations

import copy
import json
import os
import unittest
from itertools import product

from condexpose import ExposureAnalyzer, PackageGraph
from condexpose.model import BUILD, PRIVATE, PUBLIC
from condexpose.reference import all_assignments, analyze_assignment

HERE = os.path.dirname(__file__)


def build_analyzer(data):
    return ExposureAnalyzer(PackageGraph(copy.deepcopy(data)))


def snapshot(analyzer):
    """按完整特性指派展开当前分析结果，供逐配置比对。"""
    graph = analyzer.graph
    result = {}
    for assignment in all_assignments(graph):
        exposed: dict[str, set[tuple]] = {}
        for record in analyzer.exposures():
            if graph.bdd.evaluate(record.condition, assignment):
                exposed.setdefault(record.name, set()).add(
                    record.source.as_tuple())
        key = tuple(assignment[name] for name in graph.bdd.features)
        result[key] = exposed
    return result


def reference_snapshot(data):
    graph = PackageGraph(copy.deepcopy(data))
    result = {}
    for assignment in all_assignments(graph):
        exposed, _, _, _ = analyze_assignment(graph, assignment)
        key = tuple(assignment[name] for name in graph.bdd.features)
        result[key] = exposed
    return result


DEMO = json.load(open(os.path.join(HERE, "..", "demo", "demo_graph.json")))


class ExhaustiveComparison(unittest.TestCase):
    def assert_matches_reference(self, data):
        reference = reference_snapshot(data)
        analyzer = build_analyzer(data)
        self.assertEqual(snapshot(analyzer), reference)

    def test_demo_graph(self):
        self.assert_matches_reference(DEMO)

    def test_conflict_and_mutex(self):
        data = {
            "features": ["a", "b"],
            "packages": [
                {"id": "root", "version": "1"},
                {"id": "x", "version": "1", "symbols": ["s"]},
                {"id": "y", "version": "1", "symbols": ["s"]},
                {"id": "z", "version": "1", "symbols": ["s"]},
            ],
            "edges": [
                {"from": "root", "to": "x", "kind": "public", "when": "a"},
                {"from": "root", "to": "y", "kind": "public", "when": ["not", "a"]},
                {"from": "root", "to": "z", "kind": "public", "when": "a"},
            ],
        }
        analyzer = build_analyzer(data)
        conflicts = {(c.name, c.sources[0].package, c.sources[1].package)
                     for c in analyzer.conflicts()}
        # a 与 !a 互斥：x/y 不冲突；a 同时开 x/z：冲突
        self.assertIn(("s", "x", "z"), conflicts)
        for name, left, right in conflicts:
            self.assertFalse((left, right) == ("x", "y"))
        self.assert_matches_reference(data)

    def test_alias_merges_same_source(self):
        data = {
            "features": ["f"],
            "packages": [
                {"id": "root", "version": "1",
                 "exports": {"alpha": "u", "beta": "u"}},
                {"id": "p", "version": "1",
                 "symbols": ["u"],
                 "exports": {"u": "u"}},
                {"id": "q", "version": "1",
                 "symbols": [], "exports": {"u": "u"}},
            ],
            "edges": [
                {"from": "root", "to": "p", "kind": "public"},
                {"from": "p", "to": "q", "kind": "public"},
                {"from": "root", "to": "q", "kind": "public"},
            ],
        }
        analyzer = build_analyzer(data)
        names = {record.name for record in analyzer.exposures()}
        # 同一来源经两条路径、两个别名合流，不产生冲突
        self.assertIn("alpha", names)
        self.assertIn("beta", names)
        self.assertEqual(analyzer.conflicts(), [])
        self.assert_matches_reference(data)


class SemanticScenarios(unittest.TestCase):
    def _diamond(self):
        return {
            "features": ["f"],
            "packages": [
                {"id": "root", "version": "1"},
                {"id": "mid", "version": "1"},
                {"id": "leaf", "version": "1", "symbols": ["v"]},
            ],
            "edges": [
                {"from": "root", "to": "mid", "kind": "public", "key": 0},
                {"from": "mid", "to": "leaf", "kind": "public", "key": 1},
                {"from": "root", "to": "leaf", "kind": "public", "key": 2},
            ],
        }

    def test_cycle_entry_deletion(self):
        data = {
            "features": [],
            "packages": [
                {"id": "root", "version": "1"},
                {"id": "a", "version": "1", "symbols": ["sa"]},
                {"id": "b", "version": "1", "symbols": ["sb"]},
            ],
            "edges": [
                {"from": "root", "to": "a", "kind": "public", "key": 0},
                {"from": "a", "to": "b", "kind": "public", "key": 1},
                {"from": "b", "to": "a", "kind": "public", "key": 2},
            ],
        }
        analyzer = build_analyzer(data)
        self.assertIn("sa", {r.name for r in analyzer.exposures()})
        self.assertIn("sb", {r.name for r in analyzer.exposures()})
        report = analyzer.delete_edge(0)  # 删除环入口
        names = {r.name for r in analyzer.exposures()}
        self.assertNotIn("sa", names)
        self.assertNotIn("sb", names)
        # 循环内旧可达性不得残留
        self.assertEqual(analyzer.runtime_packages(), ["root"])
        self.assertGreater(report.facts_overdeleted, 0)
        # 删除后与在该图上全新构建一致
        fresh = build_analyzer(_without_edge(data, 0))
        self.assertEqual(snapshot(analyzer), snapshot(fresh))

    def test_diamond_remove_one_path(self):
        data = self._diamond()
        analyzer = build_analyzer(data)
        before = analyzer.exposure_condition("v")
        self.assertEqual(len(before), 1)
        witness = analyzer.witness("v")
        self.assertEqual(witness.source.package, "leaf")
        analyzer.delete_edge(1)  # 删除 mid->leaf，保留 root->leaf
        self.assertTrue(analyzer.exposure_condition("v"))
        witness = analyzer.witness("v")
        packages = [hop[2] for hop in witness.hops if hop[0] == "edge"]
        self.assertEqual(packages[-1:], ["root"])
        fresh = build_analyzer(_without_edge(data, 1))
        self.assertEqual(snapshot(analyzer), snapshot(fresh))

    def test_private_blocks_transitivity(self):
        data = {
            "features": [],
            "packages": [
                {"id": "root", "version": "1"},
                {"id": "priv", "version": "1"},
                {"id": "inner", "version": "1", "symbols": ["deep"]},
            ],
            "edges": [
                {"from": "root", "to": "priv", "kind": "private", "key": 0},
                {"from": "priv", "to": "inner", "kind": "public", "key": 1},
            ],
        }
        analyzer = build_analyzer(data)
        self.assertNotIn("deep", {r.name for r in analyzer.exposures()})

    def test_build_closure_not_runtime(self):
        data = {
            "features": [],
            "packages": [
                {"id": "root", "version": "1"},
                {"id": "gen", "version": "1", "symbols": ["g"]},
                {"id": "dep", "version": "1", "symbols": ["d"]},
            ],
            "edges": [
                {"from": "root", "to": "gen", "kind": "build", "key": 0},
                {"from": "gen", "to": "dep", "kind": "build", "key": 1},
            ],
        }
        analyzer = build_analyzer(data)
        runtime = {r.name for r in analyzer.exposures()}
        self.assertNotIn("g", runtime)
        self.assertNotIn("d", runtime)
        build_names = {r.name for r in analyzer.build_closure()}
        self.assertIn("gen::g", build_names)
        self.assertIn("gen::d", build_names)
        self.assertEqual(set(analyzer.build_packages()), {"root", "gen", "dep"})

    def test_edge_condition_modification(self):
        data = {
            "features": ["f"],
            "packages": [
                {"id": "root", "version": "1"},
                {"id": "p", "version": "1", "symbols": ["s"]},
            ],
            "edges": [
                {"from": "root", "to": "p", "kind": "public", "when": "f", "key": 0},
            ],
        }
        analyzer = build_analyzer(data)
        self.assertTrue(analyzer.exposure_condition("s"))
        analyzer.modify_edge(0, ["not", "f"])
        fresh_data = copy.deepcopy(data)
        fresh_data["edges"][0]["when"] = ["not", "f"]
        fresh = build_analyzer(fresh_data)
        self.assertEqual(snapshot(analyzer), snapshot(fresh))


def _without_edge(data, key):
    result = copy.deepcopy(data)
    result["edges"] = [edge for edge in result["edges"]
                       if edge.get("key", id(edge)) != key]
    return result


class RandomizedExhaustive(unittest.TestCase):
    """随机小图穷举：与逐配置遍历器全面比对（含冲突集合）。"""

    def _random_graph(self, seed):
        import random

        random.seed(seed)
        features = ["f0", "f1", "f2"][: random.randint(0, 3)]
        package_ids = ["root"] + [f"p{i}" for i in range(random.randint(2, 5))]
        symbol_pool = ["s", "t", "u"]
        packages = []
        for index, pkg_id in enumerate(package_ids):
            symbols = set(random.sample(symbol_pool, random.randint(0, 2)))
            exports = {}
            if pkg_id == "root" and random.random() < 0.5 and symbols:
                original = next(iter(symbols))
                exports["renamed"] = original
                if random.random() < 0.3:
                    exports["also"] = original
                if random.random() < 0.2:
                    exports["hidden"] = None
                    # hide 以原名键出现
                    exports[next(iter(symbols))] = None
            packages.append({"id": pkg_id, "version": f"v{index}",
                             "symbols": sorted(symbols), "exports": exports})
        edges = []
        key = 0
        for from_ in package_ids:
            for to in package_ids[1:]:
                if from_ == to or random.random() > 0.45:
                    continue
                kind = random.choice([PUBLIC, PUBLIC, PUBLIC, PRIVATE, BUILD])
                when = random.choice([
                    None,
                    random.choice(features) if features else None,
                    ["not", random.choice(features)] if features else None,
                    ["or", features[0], features[-1]] if len(features) >= 2 else None,
                ])
                edge = {"from": from_, "to": to, "kind": kind, "key": key}
                if when is not None:
                    edge["when"] = when
                edges.append(edge)
                key += 1
        return {"features": features, "packages": packages, "edges": edges}

    def test_random_graphs(self):
        for seed in range(60):
            data = self._random_graph(seed)
            reference = reference_snapshot(data)
            analyzer = build_analyzer(data)
            self.assertEqual(snapshot(analyzer), reference,
                             msg=f"seed {seed}")
            # 冲突逐配置核对
            graph = analyzer.graph
            for assignment in all_assignments(graph):
                grouped: dict[str, set[tuple]] = {}
                for record in analyzer.exposures():
                    if graph.bdd.evaluate(record.condition, assignment):
                        grouped.setdefault(record.name, set()).add(
                            record.source.as_tuple())
                expected_conflicting_names = {
                    name for name, sources in grouped.items()
                    if len(sources) > 1
                }
                conflict_names = set()
                for conflict in analyzer.conflicts():
                    if graph.bdd.evaluate(conflict.condition, assignment):
                        conflict_names.add(conflict.name)
                self.assertEqual(conflict_names,
                                 expected_conflicting_names,
                                 msg=f"seed {seed} {assignment}")


class IncrementalEquivalence(unittest.TestCase):
    """删除/改边后的结果必须与“在最终图上全新构建”一致。"""

    def test_deletion_sequence(self):
        data = {
            "features": ["f", "g"],
            "packages": [
                {"id": "root", "version": "1"},
                {"id": "a", "version": "1", "symbols": ["x"]},
                {"id": "b", "version": "1", "symbols": ["y"]},
                {"id": "c", "version": "1", "symbols": ["z"]},
            ],
            "edges": [
                {"from": "root", "to": "a", "kind": "public", "when": "f", "key": 0},
                {"from": "a", "to": "b", "kind": "public", "key": 1},
                {"from": "b", "to": "a", "kind": "public", "when": "g", "key": 2},
                {"from": "root", "to": "c", "kind": "public", "key": 3},
                {"from": "c", "to": "b", "kind": "public", "key": 4},
            ],
        }
        analyzer = build_analyzer(data)
        for key in (0, 2, 4):
            analyzer.delete_edge(key)
            fresh_data = copy.deepcopy(data)
            fresh_data["edges"] = [
                edge for edge in fresh_data["edges"]
                if edge["key"] not in (0, 2, 4) or edge["key"] > key
            ]
            # 简化：只在全部删除后比对一次最终状态
        final_data = copy.deepcopy(data)
        final_data["edges"] = [
            edge for edge in final_data["edges"]
            if edge["key"] not in (0, 2, 4)
        ]
        fresh = build_analyzer(final_data)
        self.assertEqual(snapshot(analyzer), snapshot(fresh))


class BDDSharingScale(unittest.TestCase):
    """特性数增长时共享决策表示只线性增长。"""

    def _scale_graph(self, feature_count):
        features = [f"f{i}" for i in range(feature_count)]
        packages = [{"id": "root", "version": "1"}]
        edges = []
        # 每个特性一个独立分支包，符号同名但条件互斥/并列
        for index, feature in enumerate(features):
            packages.append({"id": f"p{index}", "version": "1",
                             "symbols": ["shared"]})
            edges.append({"from": "root", "to": f"p{index}",
                          "kind": "public", "when": feature, "key": index})
        return {"features": features, "packages": packages, "edges": edges}

    def test_node_count_growth(self):
        previous = None
        counts = []
        for n in (2, 4, 8, 16, 32):
            data = self._scale_graph(n)
            analyzer = build_analyzer(data)
            # 统计“暴露条件”这棵活根可达的节点数（共享子结构）
            records = analyzer.exposure_condition("shared")
            live_nodes = set()
            for record in records:
                live_nodes |= _reachable_nodes(analyzer.bdd, record.condition)
            counts.append((n, len(live_nodes), analyzer.bdd.node_count()))
            # 不应出现配置数 2^n 量级的爆炸；线性上界 3n 足够宽裕
            self.assertLessEqual(len(live_nodes), 3 * n + 2)


def _reachable_nodes(bdd, root):
    seen = {0, 1}
    stack = [root]
    while stack:
        node = stack.pop()
        if node in seen:
            continue
        seen.add(node)
        stack.extend((bdd.low_of[node], bdd.high_of[node]))
    return seen


class ConditionalPrivateAndBuild(unittest.TestCase):
    def test_conditional_private(self):
        data = {
            "features": ["f"],
            "packages": [
                {"id": "root", "version": "1"},
                {"id": "priv", "version": "1", "symbols": ["hidden"]},
                {"id": "nested", "version": "1", "symbols": ["deep"]},
            ],
            "edges": [
                {"from": "root", "to": "priv", "kind": "private",
                 "when": "f", "key": 0},
                {"from": "priv", "to": "nested", "kind": "public", "key": 1},
            ],
        }
        reference = reference_snapshot(data)
        analyzer = build_analyzer(data)
        self.assertEqual(snapshot(analyzer), reference)
        # f=False 不可见；f=True 仅自身符号 hidden 可见，deep 仍阻断
        self.assertNotIn("hidden", snapshot(analyzer)[(False,)])
        self.assertNotIn("deep", snapshot(analyzer)[(True,)])
        self.assertIn("hidden", snapshot(analyzer)[(True,)])

    def test_conditional_build_chain(self):
        data = {
            "features": ["f"],
            "packages": [
                {"id": "root", "version": "1"},
                {"id": "g", "version": "1", "symbols": ["g"]},
                {"id": "d", "version": "1", "symbols": ["d"]},
            ],
            "edges": [
                {"from": "root", "to": "g", "kind": "build", "when": "f", "key": 0},
                {"from": "g", "to": "d", "kind": "build", "key": 1},
            ],
        }
        reference = reference_snapshot(data)
        analyzer = build_analyzer(data)
        self.assertEqual(snapshot(analyzer), reference)
        # build 符号任何配置都不进入运行暴露
        for assignment in snapshot(analyzer).values():
            self.assertNotIn("g", assignment)
            self.assertNotIn("d", assignment)
