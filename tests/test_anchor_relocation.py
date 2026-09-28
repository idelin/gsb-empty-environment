import json
import subprocess
import sys
import unittest
from pathlib import Path

from anchor_relocation import (
    AnchorError,
    Edit,
    EditError,
    Gravity,
    OverlappingEditsError,
    TextHistory,
    apply_edits,
)

REPO_ROOT = Path(__file__).resolve().parents[1]


class InsertGravityTest(unittest.TestCase):
    def test_acceptance_insert_three_codepoints_at_position_two(self):
        # 验收边界：在位置 2 插入 3 码点。
        history = TextHistory("abcdef")
        history.apply([Edit(2, 2, "XYZ")])
        left = history.locate(2, Gravity.LEFT, version=0)
        right = history.locate(2, Gravity.RIGHT, version=0)
        self.assertEqual(left.position, 2)
        self.assertEqual(right.position, 5)
        self.assertFalse(left.collapsed)
        self.assertFalse(right.collapsed)
        self.assertEqual(history.text, "abXYZcdef")

    def test_insert_at_ends(self):
        history = TextHistory("ab")
        history.apply([Edit(0, 0, "X"), Edit(2, 2, "Y")])
        self.assertEqual(history.text, "XabY")
        self.assertEqual(history.locate(0, Gravity.LEFT, version=0).position, 0)
        self.assertEqual(history.locate(0, Gravity.RIGHT, version=0).position, 1)
        self.assertEqual(history.locate(2, Gravity.LEFT, version=0).position, 3)
        self.assertEqual(history.locate(2, Gravity.RIGHT, version=0).position, 4)


class CodepointSemanticsTest(unittest.TestCase):
    def test_emoji_counted_as_one_codepoint_not_utf8_bytes(self):
        # U+1F600 占 4 个 UTF-8 字节，但只是 1 个码点。
        text = "a\U0001F600b"
        self.assertEqual(len(text.encode("utf-8")), 6)
        self.assertEqual(len(text), 3)
        history = TextHistory(text)
        history.apply([Edit(2, 2, "中")])
        # 右吸附锚点越过 1 个插入码点，到位置 3；字节长度会错误地给出 5/6。
        result = history.locate(2, Gravity.RIGHT, version=0)
        self.assertEqual(result.position, 3)
        self.assertEqual(history.text, "a\U0001F600中b")

    def test_combining_sequence(self):
        # e + U+0301（组合重音）是两个码点。
        text = "e\u0301x"
        history = TextHistory(text)
        history.apply([Edit(2, 2, "-")])
        self.assertEqual(history.locate(2, Gravity.RIGHT, version=0).position, 3)
        self.assertEqual(len(history.text), 4)


class DeleteCollapseTest(unittest.TestCase):
    def test_delete_left_and_right_gravity_collapse(self):
        history = TextHistory("abcdef")
        history.apply([Edit(2, 5, "")])  # 删除 cde
        self.assertEqual(history.text, "abf")
        left = history.locate(4, Gravity.LEFT, version=0)
        right = history.locate(4, Gravity.RIGHT, version=0)
        self.assertEqual((left.position, left.collapsed), (2, True))
        self.assertEqual((right.position, right.collapsed), (2, True))
        event = right.events[0]
        self.assertEqual(event.reason, "deleted")
        self.assertEqual(event.kind, "delete")
        self.assertEqual((event.base_start, event.base_end), (2, 5))
        self.assertEqual(event.version, 1)

    def test_boundaries_are_not_collapsed(self):
        history = TextHistory("abcdef")
        history.apply([Edit(2, 5, "")])
        # 左吸附依附左侧码点：左边界不折叠、右边界折叠。
        self.assertFalse(history.locate(2, Gravity.LEFT, version=0).collapsed)
        self.assertTrue(history.locate(5, Gravity.LEFT, version=0).collapsed)
        # 右吸附依附右侧码点：左边界折叠、右边界不折叠。
        self.assertTrue(history.locate(2, Gravity.RIGHT, version=0).collapsed)
        self.assertFalse(history.locate(5, Gravity.RIGHT, version=0).collapsed)

    def test_replace_records_replace_reason(self):
        history = TextHistory("abcdef")
        history.apply([Edit(2, 5, "XY")])
        result = history.locate(3, Gravity.LEFT, version=0)
        self.assertTrue(result.collapsed)
        self.assertEqual(result.events[0].kind, "replace")
        self.assertEqual(result.events[0].inserted_length, 2)
        self.assertEqual(result.position, 2)  # 左吸附折叠到替换内容起点
        right = history.locate(3, Gravity.RIGHT, version=0)
        self.assertEqual(right.position, 4)  # 右吸附折叠到替换内容终点
        self.assertEqual(history.text, "abXYf")


class BatchEditsTest(unittest.TestCase):
    def test_multiple_non_overlapping_edits(self):
        text = "0123456789"
        edits = [Edit(1, 3, "AB"), Edit(6, 6, "+"), Edit(8, 9, "")]
        self.assertEqual(apply_edits(text, edits), "0AB345+679")

    def test_adjacent_edits_are_allowed(self):
        # 端点相接（[0,2) 与 [2,4)）不算重叠。
        text = "abcd"
        self.assertEqual(
            apply_edits(text, [Edit(0, 2, "X"), Edit(2, 4, "Y")]), "XY"
        )

    def test_zero_length_inserts_never_conflict(self):
        # 同一位置的多个纯插入互不重叠。
        text = "ab"
        self.assertEqual(apply_edits(text, [Edit(1, 1, "X"), Edit(1, 1, "Y")]), "aXYb")

    def test_insert_plus_adjacent_delete_gravity(self):
        # 在间隙 2 插入，同时删除其右侧/左侧相邻码点。
        history = TextHistory("abcd")
        history.apply([Edit(2, 3, ""), Edit(2, 2, "X")])
        self.assertEqual(history.text, "abXd")
        self.assertEqual(history.locate(2, Gravity.LEFT, version=0).position, 2)
        # 右吸附越过插入内容、随删除折叠到插入内容之后：位置 3。
        right = history.locate(2, Gravity.RIGHT, version=0)
        self.assertEqual(right.position, 3)
        self.assertTrue(right.collapsed)

        history2 = TextHistory("abcd")
        history2.apply([Edit(1, 2, ""), Edit(2, 2, "X")])
        # 左吸附随左侧删除折叠到插入内容之前：位置 1。
        left = history2.locate(2, Gravity.LEFT, version=0)
        self.assertEqual(left.position, 1)
        self.assertTrue(left.collapsed)
        self.assertEqual(history2.locate(2, Gravity.RIGHT, version=0).position, 2)

    def test_overlapping_edits_are_atomically_rejected(self):
        history = TextHistory("abcdef")
        with self.assertRaises(OverlappingEditsError) as ctx:
            history.apply([Edit(1, 4, "X"), Edit(3, 5, "Y")])
        self.assertEqual(ctx.exception.pair, (0, 1))
        # 原子拒绝：版本与文本不变。
        self.assertEqual(history.version, 0)
        self.assertEqual(history.text, "abcdef")

    def test_out_of_range_edit_rejected(self):
        history = TextHistory("ab")
        with self.assertRaises(EditError):
            history.apply([Edit(1, 3, "X")])
        with self.assertRaises(EditError):
            history.apply([Edit(2, 1, "X")])

    def test_dict_edits_accepted(self):
        history = TextHistory("abc")
        history.apply([{"start": 1, "end": 2, "new_text": "XY"}])
        self.assertEqual(history.text, "aXYc")


class MultiVersionTest(unittest.TestCase):
    def test_anchor_mapped_across_several_versions(self):
        history = TextHistory("0123456789")
        history.apply([Edit(2, 2, "AB")])   # v1: 01AB23456789
        history.apply([Edit(0, 1, "")])    # v2: 1AB23456789
        history.apply([Edit(5, 5, "-")])   # v3: 1AB23-456789
        left = history.locate(3, Gravity.LEFT, version=0)
        right = history.locate(3, Gravity.RIGHT, version=0)
        self.assertEqual(history.text, "1AB23-456789")
        self.assertEqual(left.position, 4)
        self.assertEqual(right.position, 4)
        self.assertEqual(right.version, 3)

    def test_delete_event_chain_kept_across_versions(self):
        history = TextHistory("abcdef")
        history.apply([Edit(2, 4, "")])       # v1 删除 cd
        history.apply([Edit(0, 0, "000")])    # v2 头部插入
        result = history.locate(3, Gravity.RIGHT, version=0)
        self.assertTrue(result.collapsed)
        self.assertEqual(len(result.events), 1)
        self.assertEqual(result.events[0].version, 1)
        self.assertEqual(result.position, 5)  # 折叠位置 2 再随头部插入右移
        self.assertEqual(history.text, "000abef")

    def test_gravity_persists_through_changes(self):
        history = TextHistory("ab")
        history.apply([Edit(1, 1, "X")])
        history.apply([Edit(2, 2, "Y")])
        left = history.locate(1, Gravity.LEFT, version=0)
        right = history.locate(1, Gravity.RIGHT, version=0)
        self.assertEqual(history.text, "aXYb")
        self.assertEqual(left.position, 1)
        self.assertEqual(right.position, 3)


class ValidationTest(unittest.TestCase):
    def test_anchor_out_of_range(self):
        history = TextHistory("ab")
        with self.assertRaises(AnchorError):
            history.locate(3, Gravity.LEFT)
        with self.assertRaises(AnchorError):
            history.locate(-1, Gravity.RIGHT)

    def test_boolean_is_not_integer_position(self):
        history = TextHistory("ab")
        with self.assertRaises(AnchorError):
            history.locate(True, Gravity.LEFT)  # type: ignore[arg-type]

    def test_unknown_version_rejected(self):
        history = TextHistory("ab")
        with self.assertRaises(AnchorError):
            history.locate(0, Gravity.LEFT, version=5)

    def test_bad_gravity(self):
        history = TextHistory("ab")
        with self.assertRaises(AnchorError):
            history.locate(0, "middle")  # type: ignore[arg-type]


class CliTest(unittest.TestCase):
    def test_cli_acceptance_example(self):
        request = {
            "initial_text": "abcdef",
            "changes": [{"edits": [{"start": 2, "end": 2, "new_text": "XYZ"}]}],
            "anchors": [
                {"id": "L", "position": 2, "gravity": "left"},
                {"id": "R", "position": 2, "gravity": "right"},
            ],
        }
        proc = subprocess.run(
            [sys.executable, "-m", "anchor_relocation", "--pretty"],
            input=json.dumps(request),
            text=True,
            capture_output=True,
            cwd=REPO_ROOT,
            check=False,
        )
        self.assertEqual(proc.returncode, 0, proc.stderr)
        response = json.loads(proc.stdout)
        self.assertEqual(response["status"], "ok")
        self.assertEqual(response["final_text"], "abXYZcdef")
        anchors = {item["id"]: item for item in response["anchors"]}
        self.assertEqual(anchors["L"]["position"], 2)
        self.assertEqual(anchors["R"]["position"], 5)

    def test_cli_overlapping_rejected(self):
        request = {
            "initial_text": "abcdef",
            "changes": [
                {
                    "edits": [
                        {"start": 1, "end": 4, "new_text": "X"},
                        {"start": 3, "end": 5, "new_text": "Y"},
                    ]
                }
            ],
            "anchors": [],
        }
        proc = subprocess.run(
            [sys.executable, "-m", "anchor_relocation"],
            input=json.dumps(request),
            text=True,
            capture_output=True,
            cwd=REPO_ROOT,
            check=False,
        )
        self.assertEqual(proc.returncode, 2)
        response = json.loads(proc.stdout)
        self.assertEqual(response["error"]["code"], "overlapping_edits")
        self.assertEqual(response["error"]["edit_pair"], [0, 1])

    def test_cli_codepoint_example(self):
        request = {
            "initial_text": "a\U0001F600b",
            "changes": [{"edits": [{"start": 2, "new_text": "中"}]}],
            "anchors": [{"id": "r", "position": 2, "gravity": "right"}],
        }
        proc = subprocess.run(
            [sys.executable, "-m", "anchor_relocation"],
            input=json.dumps(request),
            text=True,
            capture_output=True,
            cwd=REPO_ROOT,
            check=False,
        )
        self.assertEqual(proc.returncode, 0, proc.stderr)
        response = json.loads(proc.stdout)
        self.assertEqual(response["anchors"][0]["position"], 3)
        self.assertEqual(response["final_text"], "a\U0001F600中b")


if __name__ == "__main__":
    unittest.main()
