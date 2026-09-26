"""段落锚点重定位（paragraph anchor relocation）。

文本视为 Unicode 码点序列（Python str 的 len/索引即码点语义，绝不使用
UTF-8 字节长度）。批量编辑以基准版本（base version）的码点偏移定义，
支持插入 / 删除 / 替换，重叠编辑整批原子拒绝。锚点带 left/right 吸附
语义，可跨多个变更集（版本）映射到当前版本；删除覆盖锚点时返回折叠
位置与原因。

映射规则（单个编辑 e = [start, start+delete) 替换为 inserted 个码点，
锚点位置 p，shift 为之前编辑累计的位移）：

- p < start                : 不变
- p > end                  : p + (inserted - delete)
- 纯插入且 p == start      : left 吸附不变；right 吸附移到插入文本之后
- start < p < end          : 被覆盖，折叠到 start + shift
- p == start 且 delete > 0 : left 吸附留在 start；right 吸附被覆盖，折叠到 start + shift
- p == end   且 delete > 0 : right 吸附映射到 start + inserted（幸存）；
                             left 吸附被覆盖，折叠到 start + shift + inserted
"""

from __future__ import annotations

import argparse
import json
import sys
from dataclasses import dataclass, field
from pathlib import Path

AFFINITY_LEFT = "left"
AFFINITY_RIGHT = "right"
AFFINITIES = (AFFINITY_LEFT, AFFINITY_RIGHT)

EXIT_OK = 0
EXIT_INVALID_INPUT = 2
EXIT_OVERLAPPING_EDITS = 3


class EditError(ValueError):
    """编辑本身非法（负偏移、越界、空编辑等）。"""


class OverlappingEditsError(EditError):
    """同一批次内编辑区间重叠：整批原子拒绝，不应用任何编辑。"""


@dataclass(frozen=True)
class Edit:
    """一次编辑：删除 [start, start+delete) 并插入 text（均为码点语义）。"""

    start: int
    delete: int = 0
    text: str = ""

    @property
    def end(self) -> int:
        return self.start + self.delete

    @property
    def inserted(self) -> int:
        return len(self.text)  # 码点数，不是 UTF-8 字节数


@dataclass(frozen=True)
class Anchor:
    pos: int
    affinity: str = AFFINITY_LEFT
    name: str = ""


@dataclass
class MappedAnchor:
    pos: int
    affinity: str
    name: str = ""
    covered: bool = False
    reasons: list[str] = field(default_factory=list)


def validate_batch(edits: list[Edit], text_len: int) -> list[Edit]:
    """校验整批编辑并返回按基准偏移排序的副本。任何非法都抛异常（原子拒绝）。"""
    if not edits:
        return []
    for e in edits:
        if e.start < 0 or e.delete < 0:
            raise EditError(f"编辑偏移非法: start={e.start}, delete={e.delete}")
        if e.end > text_len:
            raise EditError(
                f"编辑越界: [{e.start}, {e.end}) 超出文本长度 {text_len}（码点）"
            )
        if e.delete == 0 and e.text == "":
            raise EditError(f"空编辑（不删不插）: start={e.start}")
    ordered = sorted(edits, key=lambda e: (e.start, e.end))
    for prev, nxt in zip(ordered, ordered[1:]):
        if prev.end > nxt.start:
            raise OverlappingEditsError(
                f"编辑重叠，整批拒绝: [{prev.start}, {prev.end}) 与 "
                f"[{nxt.start}, {nxt.end})"
            )
    return ordered


def apply_edits(text: str, edits: list[Edit]) -> str:
    """把一批以 text（基准版本）偏移定义的编辑应用到文本。重叠则原子拒绝。"""
    ordered = validate_batch(edits, len(text))
    parts: list[str] = []
    cursor = 0
    for e in ordered:
        parts.append(text[cursor : e.start])
        parts.append(e.text)
        cursor = e.end
    parts.append(text[cursor:])
    return "".join(parts)


def map_anchor_through_batch(
    anchor: Anchor, edits: list[Edit]
) -> tuple[int, bool, list[str]]:
    """把锚点映射穿过一批编辑（编辑与锚点位置基于同一版本）。

    返回 (新位置, 是否被覆盖, 原因列表)。被覆盖时新位置为折叠位置。
    """
    ordered = sorted(edits, key=lambda e: (e.start, e.end))
    pos = anchor.pos
    shift = 0
    for e in ordered:
        if pos < e.start:
            break  # 之后的编辑都在锚点右侧，无影响
        delta = e.inserted - e.delete
        if e.delete == 0 and pos == e.start:
            # 纯插入发生在锚点处：left 留在原地，right 移到插入文本之后
            if anchor.affinity == AFFINITY_RIGHT:
                shift += e.inserted
            continue
        if pos > e.end or (pos == e.end and anchor.affinity == AFFINITY_RIGHT):
            shift += delta
            continue
        if pos == e.start and anchor.affinity == AFFINITY_LEFT:
            continue  # 贴着左侧幸存文本，位置不变
        # 其余情况：锚点被删除/替换区间覆盖
        if pos == e.end and anchor.affinity == AFFINITY_LEFT:
            collapsed = e.start + shift + e.inserted
        else:
            collapsed = e.start + shift
        reason = "covered-by-delete" if e.inserted == 0 else "covered-by-replace"
        return collapsed, True, [reason]
    return pos + shift, False, []


def apply_changes(text: str, changes: list[list[Edit]]) -> str:
    """依次应用多个变更集（每个变更集基于上一版本），得到当前版本文本。"""
    for batch in changes:
        text = apply_edits(text, batch)
    return text


def map_anchor(anchor: Anchor, changes: list[list[Edit]]) -> MappedAnchor:
    """把旧版本锚点依次映射穿过多个变更集，得到当前版本位置。

    一旦被覆盖，covered 标志与原因保留，折叠位置继续参与后续版本映射。
    """
    pos = anchor.pos
    covered = False
    reasons: list[str] = []
    for batch in changes:
        pos, cov, rs = map_anchor_through_batch(
            Anchor(pos=pos, affinity=anchor.affinity, name=anchor.name), batch
        )
        covered = covered or cov
        reasons.extend(rs)
    return MappedAnchor(
        pos=pos,
        affinity=anchor.affinity,
        name=anchor.name,
        covered=covered,
        reasons=reasons,
    )


def run(payload: dict) -> dict:
    """执行一次完整的重定位请求（见 README 的输入输出格式）。"""
    if not isinstance(payload, dict) or not isinstance(payload.get("text"), str):
        raise EditError("输入必须是包含字符串字段 'text' 的 JSON 对象")
    text = payload["text"]

    anchors: list[Anchor] = []
    for i, a in enumerate(payload.get("anchors", [])):
        affinity = a.get("affinity", AFFINITY_LEFT)
        if affinity not in AFFINITIES:
            raise EditError(f"锚点 #{i} 吸附语义非法: {affinity!r}")
        pos = a["pos"]
        if not isinstance(pos, int) or not 0 <= pos <= len(text):
            raise EditError(
                f"锚点 #{i} 位置越界: pos={pos!r}, 文本长度 {len(text)}（码点）"
            )
        anchors.append(Anchor(pos=pos, affinity=affinity, name=a.get("name", "")))

    changes: list[list[Edit]] = []
    for v, batch in enumerate(payload.get("changes", [])):
        changes.append(
            [
                Edit(
                    start=e["start"],
                    delete=e.get("delete", 0),
                    text=e.get("text", ""),
                )
                for e in batch
            ]
        )

    final_text = apply_changes(text, changes)  # 任一版本重叠即整体拒绝
    mapped = [map_anchor(a, changes) for a in anchors]
    return {
        "text": final_text,
        "anchors": [
            {
                "name": m.name,
                "pos": m.pos,
                "affinity": m.affinity,
                "covered": m.covered,
                "reasons": m.reasons,
            }
            for m in mapped
        ],
    }


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        prog="anchor_relocate",
        description="段落锚点重定位：从 stdin（或文件）读 JSON 请求，向 stdout 写 JSON 结果。",
    )
    parser.add_argument("input", nargs="?", help="JSON 输入文件（缺省读 stdin）")
    args = parser.parse_args(argv)

    if args.input is None:
        raw = sys.stdin.read()
    else:
        raw = Path(args.input).read_text(encoding="utf-8")

    try:
        payload = json.loads(raw)
        result = run(payload)
    except OverlappingEditsError as exc:
        print(
            json.dumps(
                {"error": str(exc), "type": "overlapping-edits"}, ensure_ascii=False
            ),
            file=sys.stderr,
        )
        return EXIT_OVERLAPPING_EDITS
    except (EditError, KeyError, TypeError, ValueError) as exc:
        print(
            json.dumps(
                {"error": str(exc), "type": "invalid-input"}, ensure_ascii=False
            ),
            file=sys.stderr,
        )
        return EXIT_INVALID_INPUT

    json.dump(result, sys.stdout, ensure_ascii=False, indent=2)
    sys.stdout.write("\n")
    return EXIT_OK


if __name__ == "__main__":
    raise SystemExit(main())
