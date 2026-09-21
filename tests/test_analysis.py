import unittest

from abicompat.analysis import analyze
from abicompat.diff import REMOVED, PARAM_NARROWED, PARAM_REQUIRED, diff_releases
from abicompat.model import BuildInput, Catalog
from abicompat.typesys import TypeLattice


PACKAGES = {
    "packages": {
        "root1": {
            "releases": {
                "1.0.0": {
                    "symbols": {
                        "entry": {"kind": "function"},
                        "other": {"kind": "function"},
                    }
                }
            }
        },
        "root2": {
            "releases": {
                "1.0.0": {"symbols": {"entry": {"kind": "function"}}}
            }
        },
        "mid": {
            "releases": {
                "1.0.0": {
                    "symbols": {
                        "wrap": {"kind": "function"},
                        "branch": {"kind": "function"},
                    }
                },
                "2.0.0": {
                    "symbols": {
                        "wrap": {"kind": "function"},
                        "branch": {
                            "kind": "function",
                            "params": [{"name": "x", "type": "int"}],
                        },
                    }
                },
            }
        },
        "leaf": {
            "releases": {
                "1.0.0": {
                    "symbols": {
                        "used": {"kind": "function"},
                        "unused": {"kind": "function"},
                        "_private": {"kind": "function"},
                    }
                },
                "2.0.0": {"symbols": {}},
            }
        },
    }
}


def run_analysis(nodes, edges, roots=None):
    catalog = Catalog.from_json(PACKAGES)
    data = {"nodes": nodes, "edges": edges}
    if roots is not None:
        data["roots"] = roots
    build = BuildInput.from_json(data)
    lattice = TypeLattice()
    changes = []
    for node in build.nodes:
        package = catalog.packages[node.package]
        changes.extend(
            diff_releases(
                node.package,
                package.release(node.old_version),
                package.release(node.new_version),
                lattice,
            )
        )
    return catalog, build, changes, analyze(catalog, build, changes, lattice)


ALL_NODES = {
    "root1": {"old": "1.0.0", "new": "1.0.0"},
    "root2": {"old": "1.0.0", "new": "1.0.0"},
    "mid": {"old": "1.0.0", "new": "2.0.0"},
    "leaf": {"old": "1.0.0", "new": "2.0.0"},
}


class PropagationTest(unittest.TestCase):
    def test_transitive_path_and_callsites(self):
        edges = [
            {
                "from": "root1.entry",
                "to": "mid.wrap",
                "callsites": [{"file": "r1.py", "line": 1}],
            },
            {
                "from": "mid.wrap",
                "to": "leaf.used",
                "callsites": [{"file": "m.py", "line": 9}],
            },
        ]
        _, _, changes, result = run_analysis(ALL_NODES, edges)
        removed = [f for f in result.findings if f.change.reason == REMOVED]
        self.assertEqual(len(removed), 1)
        finding = removed[0]
        self.assertEqual(finding.root, "root1")
        self.assertEqual(
            finding.paths[0][0].dst_symbol, "wrap"
        )
        chain = [finding.paths[0][0].src_symbol, "wrap", "used"]
        self.assertEqual(
            [finding.paths[0][0].src_symbol]
            + [hop.dst_symbol for hop in finding.paths[0]],
            ["entry", "wrap", "used"],
        )
        self.assertEqual(
            {(s.file, s.line) for s in finding.affected_callsites},
            {("r1.py", 1), ("m.py", 9)},
        )

    def test_uncalled_deletion_is_unreached_not_a_finding(self):
        edges = [
            {
                "from": "root1.entry",
                "to": "mid.wrap",
                "callsites": [{"file": "r1.py", "line": 1}],
            }
        ]
        _, _, _, result = run_analysis(ALL_NODES, edges, roots=["root1"])
        unreached_symbols = {c.symbol for c in result.unreached}
        self.assertIn("unused", unreached_symbols)
        self.assertIn("used", unreached_symbols)
        self.assertEqual(result.findings, [])

    def test_diamond_multiple_paths_to_same_root(self):
        edges = [
            {"from": "root1.entry", "to": "mid.wrap"},
            {"from": "root1.other", "to": "mid.branch"},
            {"from": "mid.wrap", "to": "leaf.used"},
            {"from": "mid.branch", "to": "leaf.used"},
        ]
        _, _, _, result = run_analysis(ALL_NODES, edges)
        removed = [f for f in result.findings if f.change.symbol == "used"]
        self.assertEqual(len(removed), 1)
        self.assertEqual(len(removed[0].paths), 2)

    def test_each_root_gets_own_finding(self):
        edges = [
            {"from": "root1.entry", "to": "mid.wrap"},
            {"from": "root2.entry", "to": "mid.wrap"},
            {"from": "mid.wrap", "to": "leaf.used"},
        ]
        _, _, _, result = run_analysis(ALL_NODES, edges)
        roots = {
            f.root
            for f in result.findings
            if f.change.symbol == "used"
        }
        self.assertEqual(roots, {"root1", "root2"})

    def test_private_target_edges_do_not_propagate(self):
        edges = [
            {"from": "root1.entry", "to": "leaf._private"},
        ]
        _, _, _, result = run_analysis(ALL_NODES, edges)
        self.assertEqual(result.findings, [])
        # The removal of the private symbol is not even a public change.
        self.assertNotIn("_private", {c.symbol for c in result.unreached})

    def test_cycles_do_not_infinite_loop(self):
        edges = [
            {"from": "root1.entry", "to": "mid.wrap"},
            {"from": "mid.wrap", "to": "mid.branch"},
            {"from": "mid.branch", "to": "mid.wrap"},
            {"from": "mid.wrap", "to": "leaf.used"},
        ]
        _, _, _, result = run_analysis(ALL_NODES, edges)
        self.assertTrue(
            any(f.change.symbol == "used" for f in result.findings)
        )
        for finding in result.findings:
            for path in finding.paths:
                refs = [(hop.src_package, hop.src_symbol) for hop in path]
                self.assertEqual(len(refs), len(set(refs)))

    def test_explicit_roots_and_direct_dependency_change(self):
        nodes = dict(ALL_NODES)
        edges = [{"from": "root1.entry", "to": "mid.branch"}]
        _, _, _, result = run_analysis(nodes, edges, roots=["root1", "root2"])
        self.assertEqual(set(result.roots), {"root1", "root2"})
        narrowed = [
            f for f in result.findings if f.change.reason == PARAM_REQUIRED
        ]
        self.assertEqual(len(narrowed), 1)
        self.assertEqual(narrowed[0].root, "root1")
        self.assertEqual(len(narrowed[0].paths[0]), 1)


if __name__ == "__main__":
    unittest.main()
