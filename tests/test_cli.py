import json
import subprocess
import sys
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent


def run_cli(*args, stdin=None):
    return subprocess.run(
        [sys.executable, "-m", "sparse_intervals", *args],
        cwd=ROOT, input=stdin, capture_output=True, text=True, check=True,
    )


class TestCli(unittest.TestCase):
    def test_timeline_acceptance_flow(self):
        # 同值相邻合并 -> 撤销 a 后仅剩 [5,10)
        out = run_cli("timeline", "examples/sample1d.json")
        data = json.loads(out.stdout)
        self.assertIn("rate", data["segments"])
        # c 优先级最高，[3,7) 为 hot；两侧为 x
        segs = data["segments"]["rate"]
        self.assertEqual(
            [(s["start"], s["end"], s["value"]) for s in segs],
            [(0, 3, "x"), (3, 7, "hot"), (7, 10, "x")],
        )

        out = run_cli("revoke", "examples/sample1d.json", "--config-id", "c")
        data = json.loads(out.stdout)
        # 撤销 c 后 a、b 同值合并为 [0,10)
        self.assertEqual(
            [(s["start"], s["end"], s["value"]) for s in data["segments"]["rate"]],
            [(0, 10, "x")],
        )
        affected = data["operations"][0]["affected"]
        self.assertEqual(len(affected), 1)
        self.assertEqual((affected[0]["start"], affected[0]["end"]), (3, 7))

        out = run_cli("revoke", "examples/sample1d.json",
                      "--config-id", "c", "--config-id", "a")
        data = json.loads(out.stdout)
        # 再撤销 a：仅剩 [5,10)，不能保留缓存前半段
        self.assertEqual(
            [(s["start"], s["end"], s["value"]) for s in data["segments"]["rate"]],
            [(5, 10, "x")],
        )

    def test_explain_output(self):
        out = run_cli("timeline", "examples/sample1d.json", "--explain")
        data = json.loads(out.stdout)
        explained = data["explain"]["rate"]
        hot = [e for e in explained if e["winner"] == "c"]
        # c 在 [3,5) 遮蔽 a，在 [5,7) 遮蔽 b
        self.assertEqual(len(hot), 2)
        self.assertEqual(
            {(e["start"], e["end"], tuple(e["shadowed"])) for e in hot},
            {(3, 5, ("a",)), (5, 7, ("b",))},
        )

    def test_rect_both_solvers(self):
        out = run_cli("rect", "examples/sample2d.json", "--solver", "both")
        data = json.loads(out.stdout)
        self.assertEqual(data["audit_errors"], [])
        self.assertEqual(data["greedy"]["verify_errors"], [])
        self.assertTrue(data["exact"]["optimal"])
        self.assertEqual(data["exact"]["verify_errors"], [])
        self.assertLessEqual(data["exact"]["count"], data["greedy"]["count"])

    def test_rect_revoke(self):
        out = run_cli("rect-revoke", "examples/sample2d.json",
                      "--config-id", "promo", "--config-id", "promo2")
        data = json.loads(out.stdout)
        self.assertEqual(data["audit_errors"], [])
        self.assertEqual(len(data["operations"]), 2)
        for op in data["operations"]:
            self.assertTrue(op["affected"], "撤销应有受影响区域")

    def test_stdin_input(self):
        doc = json.dumps({"configs": [
            {"id": "a", "key": "k", "start": 0, "end": 5, "value": 1}]})
        out = run_cli("timeline", "-", stdin=doc)
        data = json.loads(out.stdout)
        self.assertEqual(data["segments"]["k"],
                         [{"start": 0, "end": 5, "value": 1}])


if __name__ == "__main__":
    unittest.main()
