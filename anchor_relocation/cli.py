"""命令行入口：从标准输入读取 JSON，把锚点结果以 JSON 写到标准输出。

用法：
    python3.11 -m anchor_relocation < request.json
    python3.11 -m anchor_relocation --pretty < request.json

输入格式见 README.md。
"""

from __future__ import annotations

import argparse
import json
import sys
from typing import Any

from .core import (
    AnchorError,
    EditError,
    OverlappingEditsError,
    TextHistory,
)


def _fail(payload: dict[str, Any], status: str) -> int:
    payload["status"] = status
    json.dump(payload, sys.stdout, ensure_ascii=False, indent=2)
    sys.stdout.write("\n")
    return 1


def run(payload: Any) -> tuple[dict[str, Any], int]:
    if not isinstance(payload, dict):
        return {"error": {"code": "invalid_input", "message": "顶层必须是 JSON 对象"}}, 1

    initial_text = payload.get("initial_text", "")
    if not isinstance(initial_text, str):
        return {
            "error": {"code": "invalid_input", "message": "initial_text 必须是字符串"}
        }, 1

    anchors = payload.get("anchors", [])
    if not isinstance(anchors, list):
        return {
            "error": {"code": "invalid_input", "message": "anchors 必须是列表"}
        }, 1

    changes = payload.get("changes", [])
    if not isinstance(changes, list):
        return {
            "error": {"code": "invalid_input", "message": "changes 必须是列表"}
        }, 1

    history = TextHistory(initial_text)

    # 先应用全部变更，再统一映射锚点；任一变更失败都不产生输出结果。
    for change_index, raw_change in enumerate(changes):
        if not isinstance(raw_change, dict):
            return {
                "error": {
                    "code": "invalid_input",
                    "message": f"第 {change_index} 个 change 必须是对象",
                }
            }, 1
        edits = raw_change.get("edits", [])
        try:
            history.apply(edits)
        except OverlappingEditsError as exc:
            return {
                "error": {
                    "code": "overlapping_edits",
                    "message": str(exc),
                    "change_index": change_index,
                    "edit_pair": list(exc.pair),
                }
            }, 2
        except EditError as exc:
            return {
                "error": {
                    "code": "invalid_edit",
                    "message": str(exc),
                    "change_index": change_index,
                }
            }, 2

    anchor_results = []
    for anchor_index, raw_anchor in enumerate(anchors):
        if not isinstance(raw_anchor, dict):
            return {
                "error": {
                    "code": "invalid_input",
                    "message": f"第 {anchor_index} 个 anchor 必须是对象",
                }
            }, 1
        anchor_id = raw_anchor.get("id", f"anchor-{anchor_index}")
        position = raw_anchor.get("position")
        gravity = raw_anchor.get("gravity", "left")
        version = raw_anchor.get("version", 0)
        try:
            result = history.locate(position, gravity, version=version, anchor_id=anchor_id)
        except AnchorError as exc:
            return {
                "error": {
                    "code": "invalid_anchor",
                    "message": str(exc),
                    "anchor_index": anchor_index,
                }
            }, 3
        anchor_results.append(result.to_dict())

    return (
        {
            "status": "ok",
            "initial_text": initial_text,
            "final_text": history.text,
            "version": history.version,
            "anchors": anchor_results,
        },
        0,
    )


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        description="段落锚点重定位（码点语义，批量编辑原子提交）"
    )
    parser.add_argument(
        "--pretty", action="store_true", help="以缩进格式输出 JSON"
    )
    args = parser.parse_args(argv)

    try:
        payload = json.load(sys.stdin)
    except json.JSONDecodeError as exc:
        return _fail(
            {"error": {"code": "invalid_json", "message": f"输入不是合法 JSON: {exc}"}},
            "error",
        )

    result, exit_code = run(payload)
    indent = 2 if args.pretty else None
    json.dump(result, sys.stdout, ensure_ascii=False, indent=indent)
    sys.stdout.write("\n")
    return exit_code


if __name__ == "__main__":
    raise SystemExit(main())
