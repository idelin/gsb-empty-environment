"""anchor_relocate 的 unittest 测试。所有数据均为合成数据。"""

import io
import json
import unittest
from unittest import mock

import anchor_relocate as ar


class AcceptanceBoundaryTest(unittest.TestCase):
    """验收边界：位置 2 插入 3 码点，左吸附仍为 2、右吸附变为 5。"""

    def test_insert_3_codepoints_at_2(self):
        edits = [ar.Edit(start=2, delete=0, text="abc")]  # 恰好 3 个码点
        left_pos, left_cov, _ = ar.map_anchor_through_batch(
            ar.Anchor(pos=2, affinity="left"), edits
        )
        right_pos, right_cov, _ = ar.map_anchor_through_batch(
            ar.Anchor(pos=2, affinity="right"), edits
        )
        self.assertEqual(left_pos, 2)
        self.assertFalse(left_cov)
        self.assertEqual(right_pos, 5)
        self.assertFalse(right_cov)

    def test_utf8_byte_length_must_not_replace_codepoint_length(self):
        # 合成文本：é(2 字节) 🙂(4 字节) x y → 4 码点 / 8 个 UTF-8 字节
        text = "é🙂xy"
        self.assertEqual(len(text), 4)
        self.assertEqual(len(text.encode("utf-8")), 8)
        self.assertNotEqual(len(text), len(text.encode("utf-8")))

        # 在码点偏移 2（emoji 之后）插入 1 码点
        new_text = ar.apply_edits(text, [ar.Edit(start=2, delete=0, text="ß")])
        self.assertEqual(new_text, "é🙂ßxy")
        # 若误用 UTF-8 字节偏移 2，会落在 emoji 编码内部，结果必然不同
        self.assertNotEqual(new_text, "éß🙂xy")

        # 多字节字符的插入长度按码点计：插入 "🙂🙂🙂" 是 3 码点而非 12 字节
        edits = [ar.Edit(start=2, delete=0, text="🙂🙂🙂")]
        pos, _, _ = ar.map_anchor_through_batch(ar.Anchor(2, "right"), edits)
        self.assertEqual(pos, 5)  # 2 + 3 码点，而非 2 + 12 字节

        # 删除长度同样按码点计
        new_text = ar.apply_edits(text, [ar.Edit(start=0, delete=2, text="")])
        self.assertEqual(new_text, "xy")  # 删掉 é 和 🙂 两个码点


class SingleBatchMappingTest(unittest.TestCase):
    def test_anchor_before_edit_unchanged(self):
        edits = [ar.Edit(start=5, delete=2, text="xyz")]
        pos, cov, _ = ar.map_anchor_through_batch(ar.Anchor(3, "left"), edits)
        self.assertEqual((pos, cov), (3, False))

    def test_anchor_after_edit_shifts_by_delta(self):
        edits = [ar.Edit(start=2, delete=4, text="ab")]  # 净 -2
        pos, cov, _ = ar.map_anchor_through_batch(ar.Anchor(10, "left"), edits)
        self.assertEqual((pos, cov), (8, False))

    def test_delete_covering_anchor_collapses_with_reason(self):
        edits = [ar.Edit(start=1, delete=3, text="")]  # 删除 [1, 4)
        pos, cov, reasons = ar.map_anchor_through_batch(ar.Anchor(3, "left"), edits)
        self.assertTrue(cov)
        self.assertEqual(pos, 1)  # 折叠到删除起点
        self.assertEqual(reasons, ["covered-by-delete"])

    def test_replace_covering_anchor_reports_replace_reason(self):
        edits = [ar.Edit(start=2, delete=2, text="XYZ")]
        pos, cov, reasons = ar.map_anchor_through_batch(ar.Anchor(3, "right"), edits)
        self.assertTrue(cov)
        self.assertEqual(pos, 2)
        self.assertEqual(reasons, ["covered-by-replace"])

    def test_boundary_at_delete_start(self):
        edits = [ar.Edit(start=2, delete=3, text="")]
        # left 吸附贴在左侧幸存文本上，不被覆盖
        pos, cov, _ = ar.map_anchor_through_batch(ar.Anchor(2, "left"), edits)
        self.assertEqual((pos, cov), (2, False))
        # right 吸附贴的内容被删，折叠并给出原因
        pos, cov, reasons = ar.map_anchor_through_batch(ar.Anchor(2, "right"), edits)
        self.assertEqual((pos, cov), (2, True))
        self.assertEqual(reasons, ["covered-by-delete"])

    def test_boundary_at_delete_end(self):
        edits = [ar.Edit(start=2, delete=3, text="R")]  # [2,5) -> "R"
        # right 吸附幸存，映射到替换文本之后
        pos, cov, _ = ar.map_anchor_through_batch(ar.Anchor(5, "right"), edits)
        self.assertEqual((pos, cov), (3, False))
        # left 吸附贴的内容被删，折叠到替换文本之后并给出原因
        pos, cov, reasons = ar.map_anchor_through_batch(ar.Anchor(5, "left"), edits)
        self.assertEqual((pos, cov), (3, True))
        self.assertEqual(reasons, ["covered-by-replace"])

    def test_multiple_edits_one_batch(self):
        # 基准文本 "abcdefghij"（10 码点）
        edits = [
            ar.Edit(start=1, delete=0, text="XX"),  # +2
            ar.Edit(start=5, delete=2, text=""),  # -2
        ]
        text = ar.apply_edits("abcdefghij", edits)
        self.assertEqual(text, "aXXbcdehij")
        pos, cov, _ = ar.map_anchor_through_batch(ar.Anchor(8, "left"), edits)
        self.assertEqual((pos, cov), (8, False))  # +2 -2 抵消
        pos, cov, _ = ar.map_anchor_through_batch(ar.Anchor(6, "left"), edits)
        self.assertEqual((pos, cov), (7, True))  # 落在 [5,7) 删除区内


class OverlapRejectionTest(unittest.TestCase):
    def test_overlapping_edits_rejected_atomically(self):
        text = "abcdef"
        edits = [ar.Edit(start=1, delete=3, text="X"), ar.Edit(start=2, delete=2, text="Y")]
        with self.assertRaises(ar.OverlappingEditsError):
            ar.apply_edits(text, edits)
        # 原子性：run() 层面同样整批拒绝，不产生部分结果
        with self.assertRaises(ar.OverlappingEditsError):
            ar.run({"text": text, "anchors": [], "changes": [edits_to_json(edits)]})

    def test_touching_edits_allowed(self):
        # [1,3) 与 [3,4) 相邻不重叠
        text = ar.apply_edits(
            "abcdef", [ar.Edit(1, 2, "X"), ar.Edit(3, 1, "Y")]
        )
        self.assertEqual(text, "aXYef")

    def test_invalid_edits_rejected(self):
        with self.assertRaises(ar.EditError):
            ar.apply_edits("abc", [ar.Edit(start=-1, delete=1)])
        with self.assertRaises(ar.EditError):
            ar.apply_edits("abc", [ar.Edit(start=2, delete=5)])  # 越界
        with self.assertRaises(ar.EditError):
            ar.apply_edits("abc", [ar.Edit(start=1)])  # 空编辑


def edits_to_json(edits):
    return [{"start": e.start, "delete": e.delete, "text": e.text} for e in edits]


class MultiVersionTest(unittest.TestCase):
    def test_anchor_maps_across_versions(self):
        # v0: "hello world"
        # v1: 在 0 处插入 ">> "  → ">> hello world"
        # v2: 替换 v1 中 [9,14) 的 "world" 为 "世界" → ">> hello 世界"
        changes = [
            [ar.Edit(0, 0, ">> ")],
            [ar.Edit(9, 5, "世界")],
        ]
        final = ar.apply_changes("hello world", changes)
        self.assertEqual(final, ">> hello 世界")

        # 旧版本中指向 "world" 起点（pos 6）的 left 锚点
        m = ar.map_anchor(ar.Anchor(6, "left", name="w"), changes)
        self.assertEqual(m.pos, 9)
        self.assertFalse(m.covered)
        self.assertEqual(final[m.pos], "世")

    def test_covered_anchor_keeps_mapping_with_reason(self):
        # v0 -> v1 删除 [1,4)，锚点 2 被覆盖折叠到 1；
        # v1 -> v2 在 0 处插入 "##"，折叠位置继续映射到 3，原因保留
        changes = [
            [ar.Edit(1, 3, "")],
            [ar.Edit(0, 0, "##")],
        ]
        m = ar.map_anchor(ar.Anchor(2, "left"), changes)
        self.assertTrue(m.covered)
        self.assertEqual(m.reasons, ["covered-by-delete"])
        self.assertEqual(m.pos, 3)


class CliTest(unittest.TestCase):
    def run_cli(self, payload):
        stdin = io.StringIO(json.dumps(payload, ensure_ascii=False))
        stdout = io.StringIO()
        stderr = io.StringIO()
        with mock.patch("sys.stdin", stdin), mock.patch(
            "sys.stdout", stdout
        ), mock.patch("sys.stderr", stderr):
            code = ar.main([])
        return code, stdout.getvalue(), stderr.getvalue()

    def test_cli_round_trip(self):
        payload = {
            "text": "é🙂xy",
            "anchors": [
                {"name": "L", "pos": 2, "affinity": "left"},
                {"name": "R", "pos": 2, "affinity": "right"},
            ],
            "changes": [[{"start": 2, "delete": 0, "text": "abc"}]],
        }
        code, out, err = self.run_cli(payload)
        self.assertEqual(code, ar.EXIT_OK, err)
        result = json.loads(out)
        self.assertEqual(result["text"], "é🙂abcxy")
        by_name = {a["name"]: a for a in result["anchors"]}
        self.assertEqual(by_name["L"]["pos"], 2)
        self.assertEqual(by_name["R"]["pos"], 5)
        self.assertFalse(by_name["L"]["covered"])

    def test_cli_reports_covered_anchor(self):
        payload = {
            "text": "abcdef",
            "anchors": [{"name": "a", "pos": 3, "affinity": "left"}],
            "changes": [[{"start": 1, "delete": 4, "text": ""}]],
        }
        code, out, _ = self.run_cli(payload)
        self.assertEqual(code, ar.EXIT_OK)
        anchor = json.loads(out)["anchors"][0]
        self.assertTrue(anchor["covered"])
        self.assertEqual(anchor["pos"], 1)
        self.assertEqual(anchor["reasons"], ["covered-by-delete"])

    def test_cli_overlap_exit_code(self):
        payload = {
            "text": "abcdef",
            "anchors": [],
            "changes": [
                [
                    {"start": 1, "delete": 3, "text": "X"},
                    {"start": 2, "delete": 1, "text": "Y"},
                ]
            ],
        }
        code, out, err = self.run_cli(payload)
        self.assertEqual(code, ar.EXIT_OVERLAPPING_EDITS)
        self.assertEqual(out, "")  # 原子拒绝：stdout 无部分结果
        self.assertEqual(json.loads(err)["type"], "overlapping-edits")

    def test_cli_invalid_input_exit_code(self):
        code, _, err = self.run_cli({"text": "abc", "anchors": [{"pos": 9}]})
        self.assertEqual(code, ar.EXIT_INVALID_INPUT)
        self.assertEqual(json.loads(err)["type"], "invalid-input")


if __name__ == "__main__":
    unittest.main()
