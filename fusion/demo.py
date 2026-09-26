"""Runnable sample:  ``python3.11 -m fusion.demo``

Builds a small synthetic UTF-8 corpus, runs a weighted multi-field query
through the threshold TopK algorithm, verifies it against the exhaustive
reference, and demonstrates fixed-snapshot pagination plus the
reject/degrade behaviour for negative weights.
"""

from __future__ import annotations

from .engine import Engine
from .fusion import NegativeWeightError
from .index import Document, Index

FIELDS = ["title", "body", "notes"]

DOCUMENTS = [
    Document("d1", {
        "title": "机器学习入门",
        "body": "机器学习 是 人工智能 的 重要 分支 machine learning basics",
        "notes": "tutorial 教程 入门",
    }),
    Document("d2", {
        "title": "深度学习 deep learning",
        "body": "神经网络 与 机器学习 模型 训练",
        # notes 字段缺失：融合时不以 0 惩罚
    }),
    Document("d3", {
        "title": "数据挖掘 技术",
        "body": "machine learning 与 数据 挖掘 的 结合",
        "notes": "machine 挖掘",
    }),
    Document("d4", {
        "title": "Python 编程 指南",
        "body": "编程 语言 入门 与 实践",
        "notes": "machine 无关 备注",
    }),
    Document("d5", {
        "title": "机器学习实战 machine",
        "body": "机器 学习 项目 案例 machine learning 实战",
        "notes": "实战 项目 案例",
    }),
    Document("d6", {
        "title": "操作系统 原理",
        "body": "进程 线程 与 内存 管理",
        # notes 缺失
    }),
    Document("d7", {
        "title": "machine vision 导论",
        "body": "计算机 视觉 与 machine 感知",
        "notes": "vision 视觉",
    }),
    Document("d8", {
        "title": "高效 学习 方法",
        "body": "如何 高效 学习 机器 相关 知识",
        # notes 缺失
    }),
]

QUERY = "机器学习 machine"
WEIGHTS = {"title": 2.0, "body": 1.0, "notes": 1.5}
K = 3


def _fmt(value):
    return "  --  " if value is None else f"{value:.4f}"


def _print_items(items):
    for rank, sd in enumerate(items, 1):
        print(f"  #{rank} {sd.doc_id}  score={sd.score:.4f}")
        for fs in sd.field_scores:
            state = "缺失" if not fs.present else f"raw={fs.raw:.0f}"
            print(
                f"       {fs.field:<6} w={fs.weight:<4} {state:<10} "
                f"normalized={_fmt(fs.normalized)}"
            )


def main() -> None:
    index = Index(DOCUMENTS, FIELDS)
    engine = Engine(index)

    print("== 多字段阈值式 TopK 融合 运行样例 ==")
    print(f"查询: {QUERY!r}   权重: {WEIGHTS}   k={K}")

    result = engine.search(QUERY, WEIGHTS, k=K)
    meta = result.meta
    print(f"\n[meta] mode={meta['mode']} degraded={meta['degraded']} "
          f"stopped_early={meta.get('stopped_early')}")
    print(f"[meta] 各字段 sorted-access 拉取次数: {meta.get('pulls')}")
    print(f"[meta] 停止时上界 bound={meta.get('bound')} "
          f"kth_score={meta.get('kth_score')}")
    print(f"[meta] 归一化明确统计: {meta['stats']}")

    print("\n[TopK 结果（含分项得分）]")
    _print_items(result.items)

    report = engine.verify(QUERY, WEIGHTS, k=K)
    print(f"\n[全量融合参考核验] ok={report['ok']} mode={report['mode']}")

    print("\n[固定快照分页] limit=2")
    snap = engine.snapshot(QUERY, WEIGHTS)
    print(f"  snapshot token={snap.token} total={snap.total}")
    offset = 0
    while offset is not None:
        page = engine.page(snap.token, offset=offset, limit=2)
        ids = [sd.doc_id for sd in page.items]
        print(f"  page offset={page.offset}: {ids} next={page.next_offset}")
        offset = page.next_offset

    print("\n[负权行为]")
    try:
        engine.search(QUERY, {"title": 1.0, "body": -0.5}, k=K)
    except NegativeWeightError as exc:
        print(f"  严格模式拒绝: {exc}")
    degraded = Engine(index, on_negative_weights="degrade").search(
        QUERY, {"title": 1.0, "body": -0.5}, k=K
    )
    print(f"  退化模式: mode={degraded.meta['mode']} "
          f"degraded={degraded.meta['degraded']}")
    print(f"  退化原因: {degraded.meta['reason']}")
    print(f"  退化结果: {[(sd.doc_id, round(sd.score, 4)) for sd in degraded.items]}")


if __name__ == "__main__":
    main()
