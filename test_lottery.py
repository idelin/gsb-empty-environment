"""lottery.py 的 unittest 测试。所有数据均为合成数据。"""

import json
import os
import random
import subprocess
import sys
import tempfile
import unittest

import lottery


def brute_force_lexmin(candidate_ids, group_of, quotas, edges, rank):
    """暴力枚举所有匹配，返回 (最大基数, 最大匹配中字典序最小的 rank 序列)。

    仅用于小规模对拍。字典序定义：匹配表示为按 rank 升序的元组后比较。
    """
    cands = sorted(candidate_ids)
    award_ids = sorted(quotas)
    best_size = -1
    best_key = None

    def rec(i, remaining, chosen):
        nonlocal best_size, best_key
        if i == len(cands):
            key = tuple(sorted(rank[e] for e in chosen))
            if len(chosen) > best_size or (len(chosen) == best_size and key < best_key):
                best_size = len(chosen)
                best_key = key
            return
        # 上界剪枝
        if len(chosen) + (len(cands) - i) < best_size:
            return
        c = cands[i]
        rec(i + 1, remaining, chosen)
        for a in award_ids:
            e = (c, a)
            if e in edges and remaining[a].get(group_of[c], 0) > 0:
                rem2 = {k: dict(v) for k, v in remaining.items()}
                rem2[a][group_of[c]] -= 1
                rec(i + 1, rem2, chosen + [e])

    rec(0, {k: dict(v) for k, v in quotas.items()}, [])
    return best_size, best_key


def spec_edges(spec):
    forbidden = {tuple(p) for p in spec.get("forbidden", [])}
    quotas = {a["id"]: a["quotas"] for a in spec["awards"]}
    edges = set()
    for c in spec["candidates"]:
        for a in c["awards"]:
            if (c["id"], a) not in forbidden and quotas[a].get(c["group"], 0) > 0:
                edges.add((c["id"], a))
    return edges


def make_random_spec(rng, n_cands=6, n_awards=4, n_groups=2):
    groups = [f"G{i}" for i in range(n_groups)]
    awards = [f"award{j}" for j in range(n_awards)]
    cands = [f"cand{i}" for i in range(n_cands)]
    spec = {
        "seed": rng.randrange(1 << 30),
        "candidates": [
            {
                "id": c,
                "group": rng.choice(groups),
                "awards": [a for a in awards if rng.random() < 0.5],
            }
            for c in cands
        ],
        "awards": [
            {"id": a, "quotas": {g: rng.randrange(0, 3) for g in groups}}
            for a in awards
        ],
        "forbidden": [],
    }
    for c in cands:
        for a in awards:
            if rng.random() < 0.15:
                spec["forbidden"].append([c, a])
    return spec


class TestBoundaryCase(unittest.TestCase):
    """验收边界：a 仅能拿 x，b 能拿 x 或 y，x/y 配额各 1。

    唯一最大匹配是 a-x, b-y。朴素按边序贪心在 b-x 排在 a-x 之前时会
    错选 b-x 导致少发一个奖；本算法对任意种子都必须给出 a-x, b-y。
    """

    SPEC = {
        "seed": 0,
        "candidates": [
            {"id": "a", "group": "G", "awards": ["x"]},
            {"id": "b", "group": "G", "awards": ["x", "y"]},
        ],
        "awards": [
            {"id": "x", "quotas": {"G": 1}},
            {"id": "y", "quotas": {"G": 1}},
        ],
        "forbidden": [],
    }

    def test_all_seeds_give_optimal(self):
        greedy_would_fail_seeds = 0
        for seed in range(128):
            spec = dict(self.SPEC, seed=seed)
            result = lottery.solve(spec)
            self.assertEqual(result["matching_size"], 2, f"seed={seed}")
            self.assertEqual(
                sorted(map(tuple, result["matching"])),
                [("a", "x"), ("b", "y")],
                f"seed={seed}",
            )
            self.assertTrue(result["is_maximum"])
            # 统计：该种子下朴素贪心（按全序先见先选）是否会错选 b-x
            order = [tuple(e) for e in result["edge_order"]]
            if order.index(("b", "x")) < order.index(("a", "x")):
                greedy_would_fail_seeds += 1
        # 确认测试确实覆盖了"贪心会失败"的种子，而非空转
        self.assertGreater(greedy_would_fail_seeds, 0)

    def test_naive_greedy_actually_fails(self):
        """佐证边界有效性：存在种子使朴素按序贪心只发 1 个奖。"""
        spec = dict(self.SPEC, seed=0)
        edges = spec_edges(spec)
        for seed in range(128):
            order = lottery.edge_total_order(edges, seed)
            used_c, used_slot, picked = set(), set(), []
            for c, a in order:
                slot = (a, "G")
                if c not in used_c and slot not in used_slot:
                    picked.append((c, a))
                    used_c.add(c)
                    used_slot.add(slot)
            if len(picked) == 1:
                return
        self.fail("未找到使朴素贪心失败的种子，边界测试无效")


class TestPrngDeterminism(unittest.TestCase):
    def test_known_vector(self):
        """SplitMix64 固定测试向量（种子 0 的前三个输出，取自公开参考值）。"""
        prng = lottery.SplitMix64(0)
        self.assertEqual(prng.next(), 0xE220A8397B1DCDAF)
        self.assertEqual(prng.next(), 0x6E789E6AA1B965F4)
        self.assertEqual(prng.next(), 0x06C45D188009454F)

    def test_edge_order_reproducible(self):
        edges = {("a", "x"), ("b", "x"), ("b", "y"), ("c", "y")}
        o1 = lottery.edge_total_order(edges, 42)
        o2 = lottery.edge_total_order(edges, 42)
        self.assertEqual(o1, o2)
        self.assertEqual(sorted(o1), sorted(edges))  # 是全序排列

    def test_seed_changes_order(self):
        edges = {(f"c{i}", f"a{j}") for i in range(4) for j in range(4)}
        orders = {tuple(lottery.edge_total_order(edges, s)) for s in range(8)}
        self.assertGreater(len(orders), 1)

    def test_no_bool_seed(self):
        with self.assertRaises(TypeError):
            lottery.SplitMix64(True)


class TestCrossProcessReproducibility(unittest.TestCase):
    """跨重启/跨 hash 种子复现：不同 PYTHONHASHSEED 下 CLI 输出逐字节一致。"""

    def test_cli_output_stable_across_hash_seeds(self):
        rng = random.Random(2024)
        spec = make_random_spec(rng, n_cands=8, n_awards=5, n_groups=3)
        outputs = []
        for hash_seed in ("0", "1", "12345"):
            env = dict(os.environ, PYTHONHASHSEED=hash_seed)
            proc = subprocess.run(
                [sys.executable, "lottery.py"],
                input=json.dumps(spec),
                capture_output=True,
                text=True,
                env=env,
                cwd=os.path.dirname(os.path.abspath(__file__)),
            )
            self.assertEqual(proc.returncode, 0, proc.stderr)
            outputs.append(proc.stdout)
        self.assertEqual(outputs[0], outputs[1])
        self.assertEqual(outputs[1], outputs[2])

    def test_cli_file_io(self):
        spec = TestBoundaryCase.SPEC
        with tempfile.TemporaryDirectory() as tmp:
            inp = os.path.join(tmp, "in.json")
            out = os.path.join(tmp, "out.json")
            with open(inp, "w", encoding="utf-8") as fh:
                json.dump(spec, fh)
            proc = subprocess.run(
                [sys.executable, "lottery.py", inp, "-o", out],
                capture_output=True,
                text=True,
                cwd=os.path.dirname(os.path.abspath(__file__)),
            )
            self.assertEqual(proc.returncode, 0, proc.stderr)
            with open(out, encoding="utf-8") as fh:
                result = json.load(fh)
            self.assertEqual(result["matching_size"], 2)
            self.assertEqual(
                sorted(map(tuple, result["matching"])), [("a", "x"), ("b", "y")]
            )


class TestConstraints(unittest.TestCase):
    def test_forbidden_edges_respected(self):
        spec = {
            "seed": 7,
            "candidates": [
                {"id": "a", "group": "G", "awards": ["x", "y"]},
                {"id": "b", "group": "G", "awards": ["x", "y"]},
            ],
            "awards": [
                {"id": "x", "quotas": {"G": 1}},
                {"id": "y", "quotas": {"G": 1}},
            ],
            "forbidden": [["a", "x"], ["b", "y"]],
        }
        result = lottery.solve(spec)
        self.assertEqual(
            sorted(map(tuple, result["matching"])), [("a", "y"), ("b", "x")]
        )

    def test_group_quota_limits(self):
        # 奖项 x 对 G1 配额 1、对 G2 配额 0：G2 的人不能拿 x
        spec = {
            "seed": 3,
            "candidates": [
                {"id": "p", "group": "G1", "awards": ["x"]},
                {"id": "q", "group": "G2", "awards": ["x"]},
            ],
            "awards": [{"id": "x", "quotas": {"G1": 1, "G2": 0}}],
            "forbidden": [],
        }
        result = lottery.solve(spec)
        self.assertEqual(result["matching"], [["p", "x"]])
        self.assertEqual(result["matching_size"], 1)

    def test_one_award_per_person(self):
        spec = {
            "seed": 9,
            "candidates": [{"id": "a", "group": "G", "awards": ["x", "y"]}],
            "awards": [
                {"id": "x", "quotas": {"G": 1}},
                {"id": "y", "quotas": {"G": 1}},
            ],
        }
        result = lottery.solve(spec)
        self.assertEqual(result["matching_size"], 1)

    def test_unknown_award_rejected(self):
        spec = {
            "seed": 1,
            "candidates": [{"id": "a", "group": "G", "awards": ["nope"]}],
            "awards": [],
        }
        with self.assertRaises(ValueError):
            lottery.solve(spec)


class TestOptimalityAgainstBruteForce(unittest.TestCase):
    """小规模随机合成实例上与暴力枚举对拍：基数最大且字典序最小。"""

    def test_matches_brute_force(self):
        rng = random.Random(987654321)
        for case in range(60):
            spec = make_random_spec(rng)
            result = lottery.solve(spec)
            edges = spec_edges(spec)
            order = [tuple(e) for e in result["edge_order"]]
            rank = {e: i for i, e in enumerate(order)}
            group_of = {c["id"]: c["group"] for c in spec["candidates"]}
            quotas = {a["id"]: a["quotas"] for a in spec["awards"]}
            best_size, best_key = brute_force_lexmin(
                [c["id"] for c in spec["candidates"]], group_of, quotas, edges, rank
            )
            got_key = tuple(sorted(rank[tuple(e)] for e in result["matching"]))
            self.assertEqual(result["matching_size"], best_size, f"case={case}")
            self.assertEqual(got_key, best_key, f"case={case}")
            self.assertTrue(result["is_maximum"])


if __name__ == "__main__":
    unittest.main()
