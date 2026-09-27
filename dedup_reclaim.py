#!/usr/bin/env python3
"""多快照去重空间回收规划器。

模型：
  - 内容块(block)：有字节大小，被多个文件版本共享。
  - 文件版本(version)：引用一组内容块。
  - 快照(snapshot)：引用一组文件版本，带有业务损失(loss)。
  - 删除一组快照后，仅当某内容块的所有引用方快照都被删除（引用数归零）时，
    该块才可回收。共享块具有互补性：单独可回收量不能简单相加。

目标：在回收字节数 >= required_bytes 的约束下，选总损失最小的快照集合；
平局时删除数最少；再平局时按快照 ID 排序后的元组字典序最小。

算法：精确分支定界（branch & bound），可选损失预算界(--loss-budget)与
节点数上限(--max-nodes)。上界剪枝利用"潜在可回收量"：所有引用方仍在
未决定集合中的块，乐观估计都可被回收。
"""

from __future__ import annotations

import argparse
import json
import sys
from dataclasses import dataclass, field


class InstanceError(ValueError):
    """输入数据非法。"""


@dataclass
class Instance:
    block_sizes: dict          # block_id -> int (>0)
    snap_losses: dict          # snap_id -> int/float (>=0)
    snap_blocks: dict          # snap_id -> frozenset(block_id)
    required_bytes: int


@dataclass
class Result:
    status: str                # optimal | feasible | infeasible | budget_exceeded | unknown
    optimal_proven: bool
    selected: list             # 排序后的快照 ID 列表
    total_loss: float
    reclaimed_bytes: int
    required_bytes: int
    reclaimed_blocks: list
    nodes_explored: int
    notes: list = field(default_factory=list)

    def to_dict(self):
        return {
            "status": self.status,
            "optimal_proven": self.optimal_proven,
            "selected_snapshots": self.selected,
            "total_loss": self.total_loss,
            "reclaimed_bytes": self.reclaimed_bytes,
            "required_bytes": self.required_bytes,
            "reclaimed_blocks": self.reclaimed_blocks,
            "nodes_explored": self.nodes_explored,
            "notes": self.notes,
        }


def load_instance(data) -> Instance:
    """从 dict 构造实例并校验。格式见 README.md。"""
    if not isinstance(data, dict):
        raise InstanceError("顶层必须是 JSON 对象")
    blocks = data.get("blocks")
    if not isinstance(blocks, dict) or not blocks:
        raise InstanceError("'blocks' 必须是非空对象 {block_id: size}")
    block_sizes = {}
    for bid, size in blocks.items():
        if not isinstance(size, int) or isinstance(size, bool) or size <= 0:
            raise InstanceError(f"块 {bid!r} 的 size 必须是正整数")
        block_sizes[str(bid)] = size

    versions = data.get("versions", {})
    if not isinstance(versions, dict):
        raise InstanceError("'versions' 必须是对象 {version_id: [block_id, ...]}")
    version_blocks = {}
    for vid, blist in versions.items():
        if not isinstance(blist, list):
            raise InstanceError(f"版本 {vid!r} 的块列表必须是数组")
        seen = set()
        for bid in blist:
            bid = str(bid)
            if bid not in block_sizes:
                raise InstanceError(f"版本 {vid!r} 引用了未知块 {bid!r}")
            seen.add(bid)
        version_blocks[str(vid)] = seen

    snaps = data.get("snapshots")
    if not isinstance(snaps, list) or not snaps:
        raise InstanceError("'snapshots' 必须是非空数组")
    snap_losses, snap_blocks = {}, {}
    for entry in snaps:
        if not isinstance(entry, dict) or "id" not in entry:
            raise InstanceError("每个快照必须是含 'id' 的对象")
        sid = str(entry["id"])
        if sid in snap_losses:
            raise InstanceError(f"快照 ID 重复: {sid!r}")
        loss = entry.get("loss", 0)
        if not isinstance(loss, (int, float)) or isinstance(loss, bool) or loss < 0:
            raise InstanceError(f"快照 {sid!r} 的 loss 必须是非负数值")
        refs = set()
        for vid in entry.get("versions", []):
            vid = str(vid)
            if vid not in version_blocks:
                raise InstanceError(f"快照 {sid!r} 引用了未知版本 {vid!r}")
            refs |= version_blocks[vid]
        for bid in entry.get("blocks", []):  # 允许快照直接引用块（便捷写法）
            bid = str(bid)
            if bid not in block_sizes:
                raise InstanceError(f"快照 {sid!r} 引用了未知块 {bid!r}")
            refs.add(bid)
        snap_losses[sid] = loss
        snap_blocks[sid] = frozenset(refs)

    required = data.get("required_bytes")
    if not isinstance(required, int) or isinstance(required, bool) or required < 0:
        raise InstanceError("'required_bytes' 必须是非负整数")
    return Instance(block_sizes, snap_losses, snap_blocks, required)


def reclaimed_bytes(inst: Instance, deleted) -> int:
    """删除集合 deleted 后实际可回收的字节数（引用数归零的块）。"""
    deleted = set(deleted)
    total = 0
    for bid, size in inst.block_sizes.items():
        referrers = [s for s in inst.snap_losses if bid in inst.snap_blocks[s]]
        if referrers and all(s in deleted for s in referrers):
            total += size
    return total


def reclaimed_blocks(inst: Instance, deleted) -> list:
    deleted = set(deleted)
    out = []
    for bid in inst.block_sizes:
        referrers = [s for s in inst.snap_losses if bid in inst.snap_blocks[s]]
        if referrers and all(s in deleted for s in referrers):
            out.append(bid)
    return sorted(out)


def max_reclaimable(inst: Instance) -> int:
    """删除全部快照时可回收的字节数（可行性上界）。"""
    return reclaimed_bytes(inst, inst.snap_losses.keys())


def solve(inst: Instance, loss_budget=None, max_nodes=None) -> Result:
    """精确分支定界求解。

    loss_budget: 可选损失预算界，损失超过该值的分支被剪除。
    max_nodes:   可选搜索节点上限，超出后返回当前最优解且 optimal_proven=False。
    """
    snap_ids = sorted(inst.snap_losses)
    n = len(snap_ids)
    sizes = inst.block_sizes
    losses = [inst.snap_losses[s] for s in snap_ids]
    blocks_of = [sorted(inst.snap_blocks[s]) for s in snap_ids]

    refs = {b: [] for b in sizes}
    for i in range(n):
        for b in blocks_of[i]:
            refs[b].append(i)

    required = inst.required_bytes
    if required == 0:
        return Result("optimal", True, [], 0, 0, 0, [], 1,
                      ["required_bytes 为 0，无需删除任何快照"])

    # live[b]: 未被删除的引用方数量；kept[b]: 已确定保留的引用方数量。
    live = {b: len(r) for b, r in refs.items()}
    kept = {b: 0 for b in refs}
    state = {
        "reclaimed": 0,  # live==0 的块的大小之和
        # kept==0 且 live>0 的块的大小之和（乐观上界：仍可能回收的部分）
        "potential": sum(sizes[b] for b in refs if live[b] > 0),
        "nodes": 0,
        "aborted": False,
    }
    deleted = []          # 当前已删除快照的下标栈
    best = {"key": None, "deleted": None}  # key=(loss, count, ids_tuple)

    def record(loss):
        ids = tuple(sorted(snap_ids[d] for d in deleted))
        key = (loss, len(deleted), ids)
        if best["key"] is None or key < best["key"]:
            best["key"] = key
            best["deleted"] = list(deleted)

    sys.setrecursionlimit(max(10000, n * 4 + 100))

    def dfs(i, loss):
        if state["aborted"]:
            return
        state["nodes"] += 1
        if max_nodes is not None and state["nodes"] > max_nodes:
            state["aborted"] = True
            return
        if loss_budget is not None and loss > loss_budget:
            return
        bk = best["key"]
        if bk is not None and (loss > bk[0] or (loss == bk[0] and len(deleted) > bk[1])):
            return  # 损失或删除数已不可能更优
        if state["reclaimed"] >= required:
            record(loss)  # 损失非负，继续删除不会更优，直接记录
            return
        if i == n:
            return
        if state["reclaimed"] + state["potential"] < required:
            return  # 上界剪枝：即使回收所有潜在块也不够

        # 分支 1：保留快照 i
        for b in blocks_of[i]:
            kept[b] += 1
            if kept[b] == 1 and live[b] > 0:
                state["potential"] -= sizes[b]
        dfs(i + 1, loss)
        for b in blocks_of[i]:
            kept[b] -= 1
            if kept[b] == 0 and live[b] > 0:
                state["potential"] += sizes[b]

        # 分支 2：删除快照 i（引用计数递减，归零块计入回收）
        for b in blocks_of[i]:
            live[b] -= 1
            if live[b] == 0:
                state["reclaimed"] += sizes[b]
                if kept[b] == 0:
                    state["potential"] -= sizes[b]
        deleted.append(i)
        dfs(i + 1, loss + losses[i])
        deleted.pop()
        for b in blocks_of[i]:
            if live[b] == 0:
                state["reclaimed"] -= sizes[b]
                if kept[b] == 0:
                    state["potential"] += sizes[b]
            live[b] += 1

    dfs(0, 0)

    notes = []
    if best["deleted"] is not None:
        sel = sorted(snap_ids[d] for d in best["deleted"])
        rec = reclaimed_bytes(inst, sel)
        if state["aborted"]:
            status, proven = "feasible", False
            notes.append("达到节点上限，结果为当前最优可行解，未证明全局最优")
        else:
            status, proven = "optimal", True
        if loss_budget is not None:
            notes.append(f"搜索受损失预算界 {loss_budget} 约束，最优性在该预算内成立")
        return Result(status, proven, sel, best["key"][0], rec, required,
                      reclaimed_blocks(inst, sel), state["nodes"], notes)

    if state["aborted"]:
        return Result("unknown", False, [], 0, 0, required, [],
                      state["nodes"], ["达到节点上限且未找到可行解，可行性未知"])
    if max_reclaimable(inst) < required:
        return Result("infeasible", True, [], 0, 0, required, [],
                      state["nodes"],
                      ["即使删除全部快照也无法满足 required_bytes"])
    return Result("budget_exceeded", True, [], 0, 0, required, [],
                  state["nodes"],
                  [f"存在可行解，但所有可行解的损失都超过预算界 {loss_budget}"])


EXAMPLE = {
    "blocks": {"b1": 10, "b2": 6, "b3": 4, "b4": 8},
    "versions": {
        "v1": ["b1"],
        "v2": ["b1", "b2"],
        "v3": ["b3"],
        "v4": ["b4"],
    },
    "snapshots": [
        {"id": "snap-a", "loss": 3, "versions": ["v1", "v3"]},
        {"id": "snap-b", "loss": 2, "versions": ["v2"]},
        {"id": "snap-c", "loss": 5, "versions": ["v3", "v4"]},
    ],
    "required_bytes": 14,
}


def main(argv=None):
    parser = argparse.ArgumentParser(
        prog="dedup_reclaim",
        description="多快照去重空间回收规划器（精确分支定界，考虑共享块互补性）")
    sub = parser.add_subparsers(dest="cmd", required=True)

    p_solve = sub.add_parser("solve", help="求解一个实例（JSON 文件或 '-' 表示标准输入）")
    p_solve.add_argument("input", help="输入 JSON 文件路径，或 '-' 从标准输入读取")
    p_solve.add_argument("--loss-budget", type=float, default=None,
                         help="损失预算界：只搜索总损失不超过该值的解")
    p_solve.add_argument("--max-nodes", type=int, default=None,
                         help="搜索节点上限（超出则返回当前最优解且标记未证最优）")
    p_solve.add_argument("--pretty", action="store_true", help="美化输出 JSON")

    sub.add_parser("example", help="打印一个合成示例输入（可直接管道给 solve）")

    args = parser.parse_args(argv)
    if args.cmd == "example":
        json.dump(EXAMPLE, sys.stdout, ensure_ascii=False, indent=2)
        sys.stdout.write("\n")
        return 0

    try:
        text = sys.stdin.read() if args.input == "-" else open(args.input, encoding="utf-8").read()
        inst = load_instance(json.loads(text))
    except (OSError, json.JSONDecodeError, InstanceError) as exc:
        print(f"输入错误: {exc}", file=sys.stderr)
        return 2

    result = solve(inst, loss_budget=args.loss_budget, max_nodes=args.max_nodes)
    indent = 2 if args.pretty else None
    json.dump(result.to_dict(), sys.stdout, ensure_ascii=False, indent=indent)
    sys.stdout.write("\n")
    return 0 if result.status in ("optimal", "feasible") else 1


if __name__ == "__main__":
    raise SystemExit(main())
