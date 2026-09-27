"""增量集合差异证明组件的 unittest 测试。

全部数据均为合成数据。运行：python3 -m unittest discover -s tests -v
"""

from __future__ import annotations

import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from setdiff import (  # noqa: E402
    ConstantHasher,
    DigestTree,
    Peer,
    Record,
    Sha256Hasher,
    VersionedStore,
    reconcile,
)
from setdiff.protocol import (  # noqa: E402
    DIFF_ONLY_LOCAL,
    DIFF_ONLY_REMOTE,
    DIFF_TOMBSTONE_VERSION,
    DIFF_VALUE,
    DIFF_VERSION,
)

REPO_ROOT = Path(__file__).resolve().parent.parent


def make_pair(local_records, remote_records, hasher=None, depth=2):
    """构造 (本地存储, 对端 Peer)，哈希器两端一致。"""
    hasher = hasher or Sha256Hasher()
    local = VersionedStore(local_records)
    remote = Peer(VersionedStore(remote_records), hasher=hasher, depth=depth, label="R")
    return local, remote, hasher


class MergeTests(unittest.TestCase):
    """验收边界 2：删除墓碑版本高于存活值时合并应删除。"""

    def test_tombstone_with_higher_version_wins(self):
        local = VersionedStore([Record("k", b"live", 3)])
        report = local.merge([Record("k", None, 5)])
        self.assertTrue(local.get("k").deleted)
        self.assertEqual(local.get("k").version, 5)
        self.assertEqual(report.updated, ["k"])

    def test_live_value_with_higher_version_wins(self):
        local = VersionedStore([Record("k", None, 5)])
        local.merge([Record("k", b"back", 7)])
        self.assertFalse(local.get("k").deleted)
        self.assertEqual(local.get("k").value, b"back")

    def test_lower_version_tombstone_loses(self):
        local = VersionedStore([Record("k", b"live", 9)])
        report = local.merge([Record("k", None, 4)])
        self.assertFalse(local.get("k").deleted)
        self.assertEqual(report.kept_local, ["k"])

    def test_equal_version_tombstone_wins_deterministically(self):
        local = VersionedStore([Record("k", b"live", 4)])
        local.merge([Record("k", None, 4)])
        self.assertTrue(local.get("k").deleted)

    def test_merge_inserts_missing_and_reports(self):
        local = VersionedStore([Record("a", b"1", 1)])
        report = local.merge([Record("b", b"2", 1), Record("a", b"1", 1)])
        self.assertEqual(report.inserted, ["b"])
        self.assertEqual(report.kept_local, ["a"])
        self.assertEqual(report.applied, 1)

    def test_merge_converges_both_directions(self):
        left = VersionedStore([Record("k", b"x", 2), Record("m", b"l", 1)])
        right = VersionedStore([Record("k", None, 3), Record("n", b"r", 1)])
        left.merge(right.records())
        right.merge(VersionedStore([Record("k", b"x", 2), Record("m", b"l", 1)]).records())
        self.assertEqual(left.records(), right.records())


class ProbabilisticModeTests(unittest.TestCase):
    def test_identical_stores_report_equal(self):
        records = [Record(f"k{i}", f"v{i}".encode(), i) for i in range(10)]
        local, remote, hasher = make_pair(records, list(records))
        result = reconcile(local, remote, mode="probabilistic", hasher=hasher)
        self.assertTrue(result.equal)
        self.assertEqual(result.diffs, [])
        self.assertEqual(result.stats.records_sent, 0)
        self.assertFalse(result.exact_verdict)  # 概率模式结论非确定性

    def test_finds_all_difference_kinds(self):
        local, remote, hasher = make_pair(
            [
                Record("same", b"a", 1),
                Record("val", b"old", 2),          # 同键同版本不同值
                Record("ver", b"x", 1),            # 版本不同
                Record("gone", b"g", 1),           # 仅本地
                Record("ts", None, 5),             # 墓碑 vs 低版本存活
            ],
            [
                Record("same", b"a", 1),
                Record("val", b"new", 2),
                Record("ver", b"x", 3),
                Record("fresh", b"f", 1),          # 仅对端
                Record("ts", b"alive", 2),
            ],
        )
        result = reconcile(local, remote, mode="probabilistic", hasher=hasher)
        self.assertFalse(result.equal)
        kinds = {entry.kind for entry in result.diffs}
        self.assertEqual(
            kinds,
            {DIFF_VALUE, DIFF_VERSION, DIFF_ONLY_LOCAL, DIFF_ONLY_REMOTE,
             DIFF_TOMBSTONE_VERSION},
        )
        by_key = {entry.key: entry for entry in result.diffs}
        self.assertEqual(by_key["val"].remote.value, "new")
        self.assertEqual(by_key["ts"].local.version, 5)
        self.assertEqual(by_key["ts"].remote.version, 2)

    def test_constant_hash_misses_differences(self):
        """恒定哈希下概率模式会漏报：展示摘要相等不等于集合相同。"""
        local, remote, hasher = make_pair(
            [Record("k", b"local-value", 1)],
            [Record("k", b"remote-value", 1)],
            hasher=ConstantHasher(0),
        )
        result = reconcile(local, remote, mode="probabilistic", hasher=hasher)
        self.assertTrue(result.equal)  # 漏报：摘要恒等导致零差异剪枝
        self.assertTrue(result.warnings)  # 但协议必须给出碰撞预警
        self.assertIn("恒定哈希", result.warnings[0])
        self.assertFalse(result.exact_verdict)

    def test_probabilistic_transfers_only_divergent_leaves(self):
        shared = [Record(f"shared:{i:03d}", b"s", 1) for i in range(200)]
        local, remote, hasher = make_pair(
            shared + [Record("diff", b"a", 1)],
            shared + [Record("diff", b"b", 1)],
        )
        result = reconcile(local, remote, mode="probabilistic", hasher=hasher)
        self.assertFalse(result.equal)
        self.assertEqual(len(result.diffs), 1)
        # 只传输的记录数应远小于全量（只下钻分歧子树）
        self.assertLess(result.stats.records_sent, 50)
        self.assertGreater(result.stats.digest_nodes_sent, 0)


class ExactModeTests(unittest.TestCase):
    """验收边界 1：同键不同值 + 恒定哈希，精确模式必须报告差异。"""

    def test_exact_mode_detects_difference_under_constant_hash(self):
        local, remote, hasher = make_pair(
            [Record("k", b"local-value", 1)],
            [Record("k", b"remote-value", 1)],
            hasher=ConstantHasher(0),
        )
        result = reconcile(local, remote, mode="exact", hasher=hasher)
        self.assertFalse(result.equal)
        self.assertEqual(len(result.diffs), 1)
        entry = result.diffs[0]
        self.assertEqual(entry.kind, DIFF_VALUE)
        self.assertEqual(entry.key, "k")
        self.assertEqual(entry.local.value, "local-value")
        self.assertEqual(entry.remote.value, "remote-value")
        self.assertTrue(result.exact_verdict is False)  # 有差异，非“确定相等”

    def test_exact_mode_confirms_equal_under_constant_hash(self):
        records = [Record("k", b"v", 1), Record("t", None, 2)]
        local, remote, hasher = make_pair(records, list(records), hasher=ConstantHasher(0))
        result = reconcile(local, remote, mode="exact", hasher=hasher)
        self.assertTrue(result.equal)
        self.assertTrue(result.exact_verdict)  # 确定相等
        self.assertEqual(result.stats.records_sent, 2)  # 全量核验

    def test_exact_mode_finds_all_differences_under_constant_hash(self):
        local, remote, hasher = make_pair(
            [Record("a", b"1", 1), Record("b", b"2", 1), Record("c", None, 9)],
            [Record("a", b"1", 1), Record("b", b"X", 1), Record("d", b"4", 1)],
            hasher=ConstantHasher(0),
        )
        result = reconcile(local, remote, mode="exact", hasher=hasher)
        keys = {entry.key for entry in result.diffs}
        self.assertEqual(keys, {"b", "c", "d"})

    def test_diff_list_is_reviewable_json(self):
        local, remote, hasher = make_pair(
            [Record("k", b"a", 1)],
            [Record("k", b"b", 1)],
        )
        result = reconcile(local, remote, mode="exact", hasher=hasher)
        payload = json.loads(result.dump_json())
        self.assertEqual(payload["diff_count"], 1)
        diff = payload["diffs"][0]
        self.assertEqual(diff["kind"], DIFF_VALUE)
        self.assertEqual(diff["local"]["value"], "a")
        self.assertEqual(diff["remote"]["value"], "b")
        self.assertIn("stats", payload)
        self.assertIn("warnings", payload)


class StatsTests(unittest.TestCase):
    def test_exact_mode_transfers_full_remote_set(self):
        remote_records = [Record(f"k{i}", b"v", 1) for i in range(7)]
        local, remote, hasher = make_pair([], remote_records)
        result = reconcile(local, remote, mode="exact", hasher=hasher)
        self.assertEqual(result.stats.records_sent, 7)
        self.assertEqual(result.stats.records_requests, 1)
        self.assertGreater(result.stats.bytes_sent, 0)

    def test_stats_json_shape(self):
        local, remote, hasher = make_pair([], [])
        result = reconcile(local, remote, mode="probabilistic", hasher=hasher)
        stats = result.stats.to_json()
        for field in (
            "mode", "digest_requests", "records_requests",
            "digest_nodes_sent", "records_sent", "bytes_sent",
        ):
            self.assertIn(field, stats)


class DigestTreeTests(unittest.TestCase):
    def test_same_records_same_root(self):
        records = [Record("a", b"1", 1), Record("b", None, 2)]
        tree1 = DigestTree(records, Sha256Hasher(), depth=2)
        tree2 = DigestTree(list(reversed(records)), Sha256Hasher(), depth=2)
        self.assertEqual(tree1.root_digest(), tree2.root_digest())

    def test_value_change_changes_root(self):
        tree1 = DigestTree([Record("a", b"1", 1)], Sha256Hasher())
        tree2 = DigestTree([Record("a", b"2", 1)], Sha256Hasher())
        self.assertNotEqual(tree1.root_digest(), tree2.root_digest())

    def test_tombstone_differs_from_live(self):
        tree1 = DigestTree([Record("a", b"1", 1)], Sha256Hasher())
        tree2 = DigestTree([Record("a", None, 1)], Sha256Hasher())
        self.assertNotEqual(tree1.root_digest(), tree2.root_digest())

    def test_constant_hash_collapses_tree(self):
        tree1 = DigestTree([Record("a", b"1", 1)], ConstantHasher(0))
        tree2 = DigestTree([Record("zzz", b"9", 9)], ConstantHasher(0))
        self.assertEqual(tree1.root_digest(), tree2.root_digest())

    def test_same_key_routes_to_same_leaf_regardless_of_hasher(self):
        tree = DigestTree([], ConstantHasher(0), depth=3)
        tree2 = DigestTree([], Sha256Hasher(), depth=3)
        self.assertEqual(tree.leaf_prefix("some-key"), tree2.leaf_prefix("some-key"))


class StoreCodecTests(unittest.TestCase):
    def test_json_roundtrip_with_binary_value(self):
        store = VersionedStore([
            Record("bin", b"\x00\xff\xfe", 1),
            Record("txt", "你好".encode("utf-8"), 2),
            Record("gone", None, 3),
        ])
        restored = VersionedStore.load_json(store.dump_json())
        self.assertEqual(restored.records(), store.records())
        self.assertEqual(restored.get("bin").value, b"\x00\xff\xfe")


class CliTests(unittest.TestCase):
    def _write_store(self, directory: str, name: str, store: VersionedStore) -> str:
        path = Path(directory) / name
        path.write_text(store.dump_json(), encoding="utf-8")
        return str(path)

    def test_cli_reconcile_exact_constant_hash(self):
        with tempfile.TemporaryDirectory() as tmp:
            local_path = self._write_store(
                tmp, "a.json", VersionedStore([Record("k", b"v1", 1)]))
            remote_path = self._write_store(
                tmp, "b.json", VersionedStore([Record("k", b"v2", 1)]))
            proc = subprocess.run(
                [sys.executable, "-m", "setdiff", "reconcile",
                 "--local", local_path, "--remote", remote_path,
                 "--mode", "exact", "--hash", "constant", "--check"],
                cwd=REPO_ROOT, capture_output=True, text=True,
            )
            self.assertEqual(proc.returncode, 1, proc.stderr)
            report = json.loads(proc.stdout)
            self.assertFalse(report["equal"])
            self.assertEqual(report["diffs"][0]["kind"], DIFF_VALUE)

    def test_cli_reconcile_check_passes_when_equal(self):
        with tempfile.TemporaryDirectory() as tmp:
            store = VersionedStore([Record("k", b"v", 1)])
            path_a = self._write_store(tmp, "a.json", store)
            path_b = self._write_store(tmp, "b.json", store)
            proc = subprocess.run(
                [sys.executable, "-m", "setdiff", "reconcile",
                 "--local", path_a, "--remote", path_b, "--check"],
                cwd=REPO_ROOT, capture_output=True, text=True,
            )
            self.assertEqual(proc.returncode, 0, proc.stderr)
            self.assertTrue(json.loads(proc.stdout)["equal"])

    def test_cli_merge_tombstone_wins(self):
        with tempfile.TemporaryDirectory() as tmp:
            local_path = self._write_store(
                tmp, "a.json", VersionedStore([Record("k", b"live", 3)]))
            remote_path = self._write_store(
                tmp, "b.json", VersionedStore([Record("k", None, 5)]))
            proc = subprocess.run(
                [sys.executable, "-m", "setdiff", "merge",
                 "--local", local_path, "--remote", remote_path],
                cwd=REPO_ROOT, capture_output=True, text=True,
            )
            self.assertEqual(proc.returncode, 0, proc.stderr)
            payload = json.loads(proc.stdout)
            merged = {r["key"]: r for r in payload["merged_store"]["records"]}
            self.assertTrue(merged["k"]["deleted"])
            self.assertEqual(merged["k"]["version"], 5)

    def test_cli_demo_runs(self):
        proc = subprocess.run(
            [sys.executable, "-m", "setdiff", "demo", "--hash", "constant"],
            cwd=REPO_ROOT, capture_output=True, text=True,
        )
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertIn("概率模式", proc.stdout)
        self.assertIn("精确模式", proc.stdout)


if __name__ == "__main__":
    unittest.main()
