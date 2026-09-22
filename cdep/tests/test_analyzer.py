"""Exhaustive small-graph tests + required scenarios + incremental checks."""

import itertools
import random
import unittest

from cdep.model import GraphSpec, Edge, Source
from cdep.analyzer import Analyzer, PUBLIC, PRIVATE, BUILD
from cdep.bdd import TRUE, FALSE
from cdep.oracle import ReferenceOracle


def all_configs(features):
    return [dict(zip(features, v)) for v in
            itertools.product((False, True), repeat=len(features))]


def assert_matches_oracle(test: unittest.TestCase, a: Analyzer,
                          oracle: ReferenceOracle = None):
    spec = a.spec
    oracle = oracle or ReferenceOracle(spec, edges=a.edges, root=a.root)
    for config in oracle.configs:
        key = frozenset(config.items())
        # runtime visibility (packages)
        got_visible = set(a.reachable_under(config))
        test.assertEqual(
            got_visible, oracle.visible[key],
            msg=f"visible mismatch under {config}",
        )
        # build nodes
        got_build = set(n for n, c in a.build_packages().items()
                        if a.bdd.holds(c, config))
        test.assertEqual(got_build, oracle.build_nodes[key],
                         msg=f"build mismatch under {config}")
        # exposed names -> sources
        got = a.exposed_under(config)
        want = oracle.exposed[key]
        test.assertEqual(set(got), set(want), msg=f"names under {config}")
        for name in want:
            got_srcs = {s.as_tuple() for s in got[name]}
            test.assertEqual(got_srcs, want[name],
                             msg=f"sources of {name} under {config}")
        # build symbols
        gotb = a.exposed_under(config, build=True)
        wantb = oracle.build_exposed[key]
        test.assertEqual(set(gotb), set(wantb), msg=f"build names under {config}")
        for name in wantb:
            test.assertEqual({s.as_tuple() for s in gotb[name]}, wantb[name])
        # conflicts under this exact config
        have_conflict = {c.name for c in a.conflicts()
                         if a.bdd.holds(c.common, config)}
        want_conflict = {n for n, cfgs in oracle.conflict_configs.items()
                         if key in cfgs}
        test.assertEqual(have_conflict, want_conflict,
                         msg=f"conflicts under {config}")


class ExhaustiveCrossCheck(unittest.TestCase):
    def test_handcrafted_all_configs(self):
        spec = GraphSpec({
            "root": "app@1",
            "features": ["ssl", "opt"],
            "packages": [
                {"id": "app@1", "provides": ["main"],
                 "exports": {
                     "private": [{"to": "priv@2", "when": "opt"}],
                     "build": [{"to": "tool@9", "when": "ssl"}]},
                 "dependencies": {"public": [
                     {"to": "a@2", "when": "ssl | opt"},
                     {"to": "b@3", "when": "!ssl"}]}},
                {"id": "a@2", "provides": ["x", "secret"], "hide": ["secret"],
                 "rename": {"x": "renamed_x"},
                 "dependencies": {"public": [{"to": "c@4", "when": "opt"}]}},
                {"id": "b@3", "provides": ["x"],
                 "dependencies": {"public": [{"to": "c@4", "when": "ssl"}]}},
                {"id": "c@4", "provides": ["shared"]},
                {"id": "priv@2", "provides": ["p"],
                 "dependencies": {"public": [{"to": "leak@8"}]}},
                {"id": "leak@8", "provides": ["leaksym"]},
                {"id": "tool@9", "provides": ["t"],
                 "dependencies": {"public": [{"to": "td@10"}]}},
                {"id": "td@10", "provides": ["td"]},
            ],
        })
        a = Analyzer(spec)
        assert_matches_oracle(self, a)
        # every satisfiable symbol condition yields a valid witness
        for name in a.exposed_names():
            w = a.witness_for(name)
            self.assertIsNotNone(w, name)
            # witness path obeys runtime edge rules
            for i, step in enumerate(w.path):
                self.assertNotEqual(step.kind, BUILD)
                if i > 0:
                    self.assertNotEqual(step.kind, PRIVATE)

    def test_random_graphs_fuzz(self):
        rng = random.Random(20260921)
        for trial in range(40):
            nfeatures = rng.randint(0, 3)
            features = [f"f{i}" for i in range(nfeatures)]
            npkgs = rng.randint(2, 6)
            conds = ["1", "0"] + features + [
                f"!{features[rng.randrange(nfeatures)]}" for _ in range(2)
            ] if features else ["1", "0"]
            packages = []
            for i in range(npkgs):
                provides = []
                if rng.random() < 0.8:
                    provides = [f"s{rng.randrange(4)}" for _ in range(rng.randint(1, 3))]
                p = {"id": f"p{i}@1", "provides": provides}
                buckets = {"public": [], "private": [], "build": []}
                for j in range(npkgs):
                    if i == j:
                        continue
                    if rng.random() < 0.35:
                        kind = rng.choice(["public", "public", "private", "build"])
                        entry = {"to": f"p{j}@1", "when": rng.choice(conds)}
                        if kind == "public":
                            p.setdefault("dependencies", {}).setdefault("public", []).append(entry)
                        else:
                            p.setdefault("exports", {}).setdefault(kind, []).append(entry)
                if rng.random() < 0.2 and provides:
                    p["rename"] = {provides[0]: rng.choice(provides + ["alias"])}
                if rng.random() < 0.2 and provides:
                    p["hide"] = [provides[0]]
                packages.append(p)
            data = {"root": "p0@1", "features": features, "packages": packages}
            spec = GraphSpec(data)
            a = Analyzer(spec)
            assert_matches_oracle(self, a)


def cycle_spec():
    # app -> a <-> b -> c ; deleting the cycle-entry edge must remove all
    return GraphSpec({
        "root": "app@1", "features": ["f"],
        "packages": [
            {"id": "app@1", "provides": ["m"],
             "dependencies": {"public": [{"to": "a@2", "when": "f"}]}},
            {"id": "a@2", "provides": ["sa"],
             "dependencies": {"public": [{"to": "b@3", "when": "1"}]}},
            {"id": "b@3", "provides": ["sb"],
             "dependencies": {"public": [{"to": "a@2", "when": "1"},
                                        {"to": "c@4", "when": "1"}]}},
            {"id": "c@4", "provides": ["sc"]},
        ],
    })


def diamond_spec():
    # app -> a -> c, app -> b -> c ; c supplies shared on two paths
    return GraphSpec({
        "root": "app@1", "features": ["x", "y"],
        "packages": [
            {"id": "app@1", "provides": ["m"],
             "dependencies": {"public": [
                 {"to": "a@2", "when": "x"}, {"to": "b@3", "when": "y"}]}},
            {"id": "a@2", "provides": [],
             "dependencies": {"public": [{"to": "c@4", "when": "1"}]}},
            {"id": "b@3", "provides": [],
             "dependencies": {"public": [{"to": "c@4", "when": "1"}]}},
            {"id": "c@4", "provides": ["shared"]},
        ],
    })


class RequiredScenarios(unittest.TestCase):
    def test_cycle_entry_removal(self):
        spec = cycle_spec()
        a = Analyzer(spec)
        for n in ("a@2", "b@3", "c@4"):
            self.assertEqual(a.condition_text(a.rt_reach[n]), "f", n)
        entry = a._find_edge(PUBLIC, "app@1", "a@2")
        report = a.remove_edge(entry)
        self.assertIn("a@2", report.touched_nodes)
        for n in ("a@2", "b@3", "c@4"):
            self.assertEqual(a.rt_reach[n], FALSE, f"{n} must not linger in cycle")
            self.assertNotIn("s" + n[0], a.exposure)
        self.assertEqual(a.exposed_names(), ["m"])
        # fresh-build equivalence
        assert_matches_oracle(self, a)

    def test_diamond_two_paths_delete_one(self):
        spec = diamond_spec()
        a = Analyzer(spec)
        self.assertEqual(set(a.sources_of("shared")).__len__(), 1)
        cond = a.condition_of("shared")
        # reachable iff x or y
        self.assertTrue(a.bdd.holds(cond, {"x": True, "y": False}))
        self.assertTrue(a.bdd.holds(cond, {"x": False, "y": True}))
        self.assertFalse(a.bdd.holds(cond, {"x": False, "y": False}))
        edge = a._find_edge(PUBLIC, "app@1", "a@2")
        a.remove_edge(edge)
        cond = a.condition_of("shared")
        self.assertFalse(a.bdd.holds(cond, {"x": True, "y": False}))
        self.assertTrue(a.bdd.holds(cond, {"x": False, "y": True}))
        assert_matches_oracle(self, a)

    def test_alias_confluence(self):
        # two packages export the same exposed name "svc" via rename,
        # under overlapping conditions -> conflict with independent paths;
        # one source alone reached twice must not conflict.
        spec = GraphSpec({
            "root": "app@1", "features": ["p", "q"],
            "packages": [
                {"id": "app@1", "provides": [],
                 "dependencies": {"public": [
                     {"to": "left@2", "when": "p"},
                     {"to": "right@3", "when": "q"}]}},
                {"id": "left@2", "provides": ["foo"], "rename": {"foo": "svc"},
                 "dependencies": {"public": [{"to": "alt@4", "when": "q"}]}},
                {"id": "right@3", "provides": ["bar"], "rename": {"bar": "svc"}},
                # alt supplies foo as left@2? no—different id, but same name
                # "svc" only when p; harmless duplicate-source test too
                {"id": "alt@4", "provides": ["baz"], "rename": {"baz": "svc"}},
            ],
        })
        a = Analyzer(spec)
        conflicts = a.conflicts()
        names = {c.name for c in conflicts}
        self.assertIn("svc", names)
        c = next(c for c in conflicts if c.name == "svc")
        # common feasible config exists
        self.assertIsNotNone(c.config)
        self.assertTrue(a.bdd.holds(c.common, c.config))
        # the two witness paths are independent (distinct first edges)
        firsts = {tuple((st.src, st.dst, st.kind) for st in w.path)
                  for w in c.witnesses}
        self.assertEqual(len(firsts), 2)
        assert_matches_oracle(self, a)

    def test_private_transitive_block(self):
        spec = GraphSpec({
            "root": "app@1", "features": [],
            "packages": [
                {"id": "app@1", "provides": ["m"],
                 "exports": {"private": [{"to": "p@2"}]}},
                {"id": "p@2", "provides": ["direct"],
                 "dependencies": {"public": [{"to": "deep@3"}]}},
                {"id": "deep@3", "provides": ["blocked"]},
            ],
        })
        a = Analyzer(spec)
        self.assertIn("direct", a.exposure)
        self.assertNotIn("blocked", a.exposure)
        self.assertEqual(a.rt_reach["deep@3"], FALSE)
        assert_matches_oracle(self, a)

    def test_build_closure_excluded_from_runtime(self):
        spec = GraphSpec({
            "root": "app@1", "features": ["t"],
            "packages": [
                {"id": "app@1", "provides": ["m"],
                 "exports": {"build": [{"to": "bld@2", "when": "t"}]}},
                {"id": "bld@2", "provides": ["tool"],
                 "dependencies": {"public": [{"to": "blddeep@3", "when": "1"}]},
                 "exports": {"build": [{"to": "btool@4", "when": "1"}]}},
                {"id": "blddeep@3", "provides": ["deep"]},
                {"id": "btool@4", "provides": ["bt"]},
            ],
        })
        a = Analyzer(spec)
        for cfg in all_configs(["t"]):
            rt = a.exposed_under(cfg)
            self.assertNotIn("tool", rt)
            self.assertNotIn("deep", rt)
            self.assertNotIn("bt", rt)
            bx = a.exposed_under(cfg, build=True)
            if cfg["t"]:
                self.assertEqual(set(bx), {"m", "tool", "deep", "bt"})
            else:
                self.assertEqual(set(bx), {"m"})
        assert_matches_oracle(self, a)

    def test_mutually_exclusive_conditions_no_false_conflict(self):
        spec = GraphSpec({
            "root": "app@1", "features": ["flag"],
            "packages": [
                {"id": "app@1", "provides": [],
                 "dependencies": {"public": [
                     {"to": "on@2", "when": "flag"},
                     {"to": "off@3", "when": "!flag"}]}},
                {"id": "on@2", "provides": ["svc"]},
                {"id": "off@3", "provides": ["svc"]},
            ],
        })
        a = Analyzer(spec)
        self.assertEqual(a.conflicts(), [])
        # but the symbol is always exposed
        self.assertEqual(a.condition_of("svc"), TRUE)
        assert_matches_oracle(self, a)


class IncrementalUpdates(unittest.TestCase):
    def test_add_then_matches_full(self):
        spec = GraphSpec({
            "root": "app@1", "features": ["f"],
            "packages": [
                {"id": "app@1", "provides": ["m"]},
                {"id": "a@2", "provides": ["x"],
                 "dependencies": {"public": [{"to": "b@3", "when": "f"}]}},
                {"id": "b@3", "provides": ["y"]},
            ],
        })
        a = Analyzer(spec)
        self.assertNotIn("x", a.exposure)
        eid = a.add_edge(Edge(PUBLIC, "app@1", "a@2", TRUE, "1")).edge_id
        self.assertIn("x", a.exposure)
        self.assertFalse(a.bdd.holds(a.condition_of("y"), {"f": False}))
        self.assertTrue(a.bdd.holds(a.condition_of("y"), {"f": True}))
        a.remove_edge(eid)
        self.assertNotIn("x", a.exposure)
        self.assertNotIn("y", a.exposure)
        assert_matches_oracle(self, a)

    def test_modify_edge_condition(self):
        spec = GraphSpec({
            "root": "app@1", "features": ["f", "g"],
            "packages": [
                {"id": "app@1", "provides": [],
                 "dependencies": {"public": [{"to": "a@2", "when": "f"}]}},
                {"id": "a@2", "provides": ["x"]},
            ],
        })
        a = Analyzer(spec)
        eid = a._find_edge(PUBLIC, "app@1", "a@2")
        self.assertTrue(a.bdd.holds(a.condition_of("x"), {"f": True, "g": False}))
        self.assertFalse(a.bdd.holds(a.condition_of("x"), {"f": False, "g": True}))
        a.modify_edge(eid, when="g")
        self.assertFalse(a.bdd.holds(a.condition_of("x"), {"f": True, "g": False}))
        self.assertTrue(a.bdd.holds(a.condition_of("x"), {"f": False, "g": True}))
        assert_matches_oracle(self, a)

    def test_incremental_matches_full_fuzz(self):
        rng = random.Random(77)
        base = {
            "root": "p0@1", "features": ["f1", "f2"],
            "packages": [
                {"id": f"p{i}@1", "provides": ([f"s{i % 3}"] if i else ["root"]),
                 "dependencies": {"public": []},
                 "exports": {"private": [], "build": []}}
                for i in range(6)
            ],
        }
        for trial in range(30):
            spec = GraphSpec(base)
            a = Analyzer(spec)
            full = Analyzer(GraphSpec(base))
            pool = [("f1",), ("!f2",), ("f1 | f2",), ("f1 & !f2",), ("1",)]
            applied = []
            for step in range(8):
                kind = rng.choice([PUBLIC, PUBLIC, PRIVATE, BUILD])
                s, d = rng.sample(range(6), 2)
                when = rng.choice(pool)[0]
                edge = Edge(kind, f"p{s}@1", f"p{d}@1",
                            spec.expr.parse(when), when)
                # mirror on the "full" instance by full rebuild after mutation
                action = rng.choice(["add", "remove", "modify"])
                existing = a._find_edge(kind, f"p{s}@1", f"p{d}@1")
                if action == "add" or existing is None:
                    if existing is None:
                        rep = a.add_edge(edge)
                        applied.append((kind, f"p{s}@1", f"p{d}@1", when))
                elif action == "remove":
                    a.remove_edge(existing)
                    applied = [e for e in applied
                               if (e[0], e[1], e[2]) != (kind, f"p{s}@1", f"p{d}@1")]
                else:
                    a.modify_edge(existing, when=when)
                    applied = [e if (e[0], e[1], e[2]) != (kind, f"p{s}@1", f"p{d}@1")
                               else (kind, f"p{s}@1", f"p{d}@1", when) for e in applied]
                # rebuild a fresh reference spec from applied edges
                mut = {
                    "root": base["root"], "features": base["features"],
                    "packages": [
                        {"id": f"p{i}@1",
                         "provides": [s for s in (["root"] if i == 0 else [f"s{i % 3}"])],
                         "dependencies": {"public": []},
                         "exports": {"private": [], "build": []}}
                        for i in range(6)],
                }
                for k, ss, dd, w in applied:
                    pkg = mut["packages"][int(ss[1])]
                    entry = {"to": dd, "when": w}
                    if k == PUBLIC:
                        pkg["dependencies"]["public"].append(entry)
                    else:
                        pkg["exports"][k].append(entry)
                oracle = ReferenceOracle(GraphSpec(mut))
                assert_matches_oracle(self, a, oracle)


class DecisionDiagramSharing(unittest.TestCase):
    @staticmethod
    def _build(n: int):
        # n independent (f_{2i}, f_{2i+1}) gates OR-joined into one
        # condition; ROBDD sharing keeps node count linear.
        features = [f"f{i}" for i in range(2 * n)]
        packages = [{"id": "root@1", "provides": [],
                     "dependencies": {"public": []}}]
        for i in range(n):
            packages.append({"id": f"d{i}@1", "provides": ["sym"]})
            packages[0]["dependencies"]["public"].append(
                {"to": f"d{i}@1", "when": f"f{2*i} | f{2*i+1}"})
        spec = GraphSpec({"root": "root@1", "features": features,
                          "packages": packages})
        return Analyzer(spec)

    def test_node_count_grows_linear_not_exponential(self):
        sizes = {}
        for n in (2, 4, 8, 12):
            a = self._build(n)
            sizes[n] = a.bdd.size()
        # 2*2^n configurations, but BDD nodes stay O(n): <= ~4n+1
        for n in (4, 8, 12):
            self.assertLess(sizes[n], 4 * n + 4,
                            msg=f"n={n}, size={sizes[n]}")
        # no duplicate nodes interned (canonical table)
        a = self._build(8)
        self.assertEqual(len(a.bdd.unique), a.bdd.size())

    def test_incremental_touches_only_suffix(self):
        n = 10
        a = self._build(n)
        before = a.bdd.size()
        eid = a._find_edge(PUBLIC, "root@1", "d0@1")
        rep = a.remove_edge(eid)
        # only the one disconnected leaf moves; all other leaves untouched
        self.assertEqual(set(rep.touched_nodes), {"d0@1"})
        # BDD representation stays shared: total does not explode
        self.assertLessEqual(a.bdd.size(), before + 2)
        self.assertEqual(len(a.exposure.get("sym", {})), n - 1)


if __name__ == "__main__":
    unittest.main()
