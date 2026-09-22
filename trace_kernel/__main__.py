"""命令行查询库：
  python -m trace_kernel demo [--out DIR]     跑演示场景，落盘轨迹/证据/事件并打印结论
  python -m trace_kernel query EVIDENCE EVENTS  从恢复的证据 + 事件文件给出三态结论
"""
from __future__ import annotations

import json
import os
import sys

from .evidence import load_evidence
from .kernel import AnalysisKernel
from .replayer import run_task_program
from .trace import TraceLog
from .vfs import VFS


def _demo(out: str) -> None:
    vfs = VFS()
    vfs.populate({
        "a": {"f": "f1", "s": ("->", "/a/f")},
        "b": {"g": "g1"},
    })
    vfs.link("/a/f", "/a/l")
    setup_len = len(vfs.trace.records)

    vfs.current_task = "T-read-file"
    run_task_program(vfs, [("open", "/a/f", "h"), ("read", "h")])
    vfs.current_task = "T-via-symlink"
    run_task_program(vfs, [("open", "/a/s", "h"), ("read", "h")])
    vfs.current_task = "T-failed-lookup"
    run_task_program(vfs, [("lookup", "/a/missing",)])
    vfs.current_task = "T-enum-prefix"
    run_task_program(vfs, [("enum", "/a", 1)])
    vfs.current_task = "T-unrelated"
    run_task_program(vfs, [("open", "/b/g", "h"), ("read", "h")])

    vfs.current_task = None
    vfs.write("/a/f", "f2")          # 内容变更
    vfs.create("/a/missing", "m1")   # 失败查询后创建
    vfs.set_target("/a/s", "/b/g")   # 链接指向改变
    vfs.create("/a/new", "n1")       # 前缀消费后新增成员

    kernel = AnalysisKernel()
    kernel.ingest(vfs.trace.records[setup_len:])
    kernel.evaluate()

    os.makedirs(out, exist_ok=True)
    with open(os.path.join(out, "trace.jsonl"), "w", encoding="utf-8") as f:
        f.write(vfs.trace.to_jsonl())
    kernel.save(os.path.join(out, "evidence.json"))
    with open(os.path.join(out, "events.jsonl"), "w", encoding="utf-8") as f:
        for e in kernel.events:
            f.write(json.dumps(e, ensure_ascii=False, sort_keys=True) + "\n")
    for task in sorted(kernel.evidence):
        print(kernel.explain(task))
    print(f"\n轨迹/证据/事件已写入 {out}/")


def _query(evidence_path: str, events_path: str) -> None:
    events = []
    with open(events_path, encoding="utf-8") as f:
        for line in f:
            line = line.strip()
            if line:
                events.append(json.loads(line))
    kernel = AnalysisKernel()
    for task, ev in load_evidence(evidence_path).items():
        kernel._install(task, ev)
    kernel.evaluate(events)
    for task in sorted(kernel.evidence):
        print(kernel.explain(task))


def main(argv: list[str]) -> int:
    if len(argv) >= 2 and argv[1] == "demo":
        out = argv[argv.index("--out") + 1] if "--out" in argv else "demo_out"
        _demo(out)
        return 0
    if len(argv) == 4 and argv[1] == "query":
        _query(argv[2], argv[3])
        return 0
    print(__doc__)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv))
