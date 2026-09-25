"""Randomized cross-checks: production branch-and-bound vs the independent
reference enumerator on small consistent instances."""
import random
import unittest

from cardian import analyze, verify_witness
from reference.enumerator import brute_force


def _random_instance(rng, ncols, domain, rows, allow_nulls):
    out = []
    for _ in range(rows):
        row = []
        for _ in range(ncols):
            if allow_nulls and rng.random() < 0.25:
                row.append(None)
            else:
                row.append(rng.choice(domain))
        out.append(row)
    return out


def _stats_from_instance(name, instance, domain, with_joint, rng):
    ncols = len(instance[0])
    columns = {}
    for ci in range(ncols):
        hist = {}
        nulls = 0
        for row in instance:
            v = row[ci]
            if v is None:
                nulls += 1
            else:
                hist[v] = hist.get(v, 0) + 1
        columns[f"c{ci}"] = {
            "domain": list(domain),
            "null_count": nulls,
            "histogram": {str(v): n for v, n in hist.items()},
        }
    table = {"row_count": len(instance), "columns": columns}
    if with_joint and ncols >= 2:
        counts = {}
        for row in instance:
            key = ",".join("null" if v is None else str(v) for v in row[:2])
            counts[key] = counts.get(key, 0) + 1
        table["joint_counts"] = [{"columns": ["c0", "c1"], "counts": counts}]
    return {"tables": {name: table}}


def _random_pred(rng, cols, domain, depth=0):
    if depth >= 2 or rng.random() < 0.5:
        col = rng.choice(cols)
        if rng.random() < 0.5:
            return {"eq": [col, rng.choice(domain)]}
        lo = rng.choice(domain)
        hi = rng.choice(domain)
        return {"range": [col, min(lo, hi), max(lo, hi)]}
    op = rng.choice(["and", "or"])
    return {op: [_random_pred(rng, cols, domain, depth + 1)
                 for _ in range(2)]}


class TestSelectFuzz(unittest.TestCase):
    def test_random_selects_match_enumerator(self):
        rng = random.Random(20260926)
        for trial in range(25):
            domain = [0, 1, 2][: rng.randint(2, 3)]
            rows = rng.randint(1, 4)
            instance = _random_instance(rng, 2, domain, rows, allow_nulls=True)
            stats = _stats_from_instance(
                "T", instance, domain, with_joint=rng.random() < 0.5, rng=rng)
            query = {"select": {"table": "T",
                                "where": _random_pred(rng, ["c0", "c1"], domain)}}
            res = analyze(stats, query)
            ref = brute_force(stats, query)
            self.assertTrue(res["exact"], f"trial {trial}")
            self.assertEqual(
                (res["min"]["lower"], res["max"]["upper"]),
                (ref["min"], ref["max"]),
                f"trial {trial}: query={query}",
            )
            for side in ("min", "max"):
                verdict = verify_witness(stats, query, res[side]["witness"])
                self.assertTrue(verdict["ok"], f"trial {trial}: {verdict['errors']}")


class TestJoinFuzz(unittest.TestCase):
    def test_random_joins_match_enumerator(self):
        rng = random.Random(20260927)
        for trial in range(15):
            domain = [0, 1]
            inst_l = _random_instance(rng, 2, domain, rng.randint(1, 4), False)
            inst_r = _random_instance(rng, 1, domain, rng.randint(1, 4), False)
            stats = _stats_from_instance("L", inst_l, domain, False, rng)
            stats["tables"].update(
                _stats_from_instance("R", inst_r, domain, False, rng)["tables"])
            query = {"join": {
                "left": {"table": "L",
                         "where": _random_pred(rng, ["c0", "c1"], domain)},
                "right": {"table": "R"},
                "on": ["c0", "c0"],
            }}
            res = analyze(stats, query)
            ref = brute_force(stats, query)
            self.assertTrue(res["exact"], f"trial {trial}")
            self.assertEqual(
                (res["min"]["lower"], res["max"]["upper"]),
                (ref["min"], ref["max"]),
                f"trial {trial}",
            )
            for side in ("min", "max"):
                verdict = verify_witness(stats, query, res[side]["witness"])
                self.assertTrue(verdict["ok"], f"trial {trial}: {verdict['errors']}")


if __name__ == "__main__":
    unittest.main()
