"""CLI 冒烟测试：通过子进程运行 python -m sparse_intervals。"""

import json
import os
import subprocess
import sys
import tempfile
import unittest

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


def run_cli(args, payload):
    with tempfile.NamedTemporaryFile(
            "w", suffix=".json", delete=False, encoding="utf-8") as f:
        json.dump(payload, f)
        path = f.name
    try:
        proc = subprocess.run(
            [sys.executable, "-m", "sparse_intervals", *args, "-i", path],
            capture_output=True, text=True, cwd=ROOT, timeout=60)
    finally:
        os.unlink(path)
    assert proc.returncode == 0, proc.stderr
    return json.loads(proc.stdout)


class TimelineCliTest(unittest.TestCase):
    def test_acceptance_via_cli(self):
        payload = {"rules": [
            {"id": "a", "priority": 1, "version": 1,
             "start": 0, "end": 5, "value": "X"},
            {"id": "b", "priority": 1, "version": 1,
             "start": 5, "end": 10, "value": "X"},
        ]}
        out = run_cli(["timeline", "--explain", "--revoke", "a"], payload)
        # 初始：合并为一段
        self.assertEqual(out["segments"],
                         [{"start": 0, "end": 10, "value": "X",
                           "winners": ["a", "b"], "shadowed": []}])
        rev = out["revocations"][0]
        self.assertEqual(rev["rid"], "a")
        self.assertEqual(rev["affected"],
                         [{"start": 0, "end": 5,
                           "old_value": "X", "new_value": None}])
        # 撤销后仅剩 [5,10)
        self.assertEqual(len(rev["segments"]), 1)
        self.assertEqual(rev["segments"][0]["start"], 5)
        self.assertEqual(rev["segments"][0]["end"], 10)


class RectCliTest(unittest.TestCase):
    PAYLOAD = {
        "query": {"t0": 0, "t1": 10, "v0": 0, "v1": 4},
        "rules": [
            {"id": "base", "priority": 1, "version": 1,
             "t0": 0, "t1": 10, "v0": 0, "v1": 4, "value": "BASE"},
            {"id": "hot", "priority": 2, "version": 1,
             "t0": 2, "t1": 6, "v0": 1, "v1": 3, "value": "HOT"},
        ],
    }

    def test_rect_partition_and_verify(self):
        out = run_cli(["rect", "--exact", "--verify"], self.PAYLOAD)
        self.assertEqual(out["verification"], {"errors": []})
        self.assertEqual(out["exact"]["status"], "optimal")
        self.assertGreaterEqual(out["rect_count"],
                                out["exact"]["rect_count"])
        hot = [r for r in out["rects"] if r["value"] == "HOT"]
        self.assertEqual(hot, [{"t0": 2, "v0": 1, "t1": 6, "v1": 3,
                                "value": "HOT"}])

    def test_rect_revoke(self):
        out = run_cli(["rect", "--revoke", "hot", "--verify"], self.PAYLOAD)
        rev = out["revocations"][0]
        self.assertEqual(rev["verification"]["mismatches"], [])
        self.assertEqual(rev["affected"],
                         [{"t0": 2, "v0": 1, "t1": 6, "v1": 3,
                           "old_value": "HOT", "new_value": "BASE"}])
        # 撤销后整个查询矩形应为一段 BASE
        self.assertEqual(out["rects"],
                         [{"t0": 0, "v0": 0, "t1": 10, "v1": 4,
                           "value": "BASE"}])
        self.assertEqual(out["verification"], {"errors": []})


if __name__ == "__main__":
    unittest.main()
