"""命令行接口：python -m setdiff <subcommand>。

子命令
------
reconcile  对两个存储文件执行增量差异对账，输出 JSON 对账报告
merge      将 B 的记录按 LWW 规则合并进 A，输出合并报告与（可选）合并后的 A
demo       运行内置合成数据演示（概率模式碰撞漏报 vs 精确模式确定核验）

输入格式（存储文件，JSON）
-------------------------
{
  "records": [
    {"key": "k1", "version": 3, "deleted": false,
     "value": "hello", "value_b64": "aGVsbG8="},
    {"key": "k2", "version": 5, "deleted": true, "value": null, "value_b64": null}
  ]
}
解析时 value_b64 优先于 value；deleted=true 或值缺省表示删除墓碑。

输出：JSON 报告写至 stdout（或 --out 指定文件），退出码见 --check。
"""

from __future__ import annotations

import argparse
import json
import sys
from typing import Optional, Sequence

from .hasher import ConstantHasher, Sha256Hasher
from .protocol import Peer, reconcile
from .store import VersionedStore


def _load_store(path: str) -> VersionedStore:
    with open(path, "r", encoding="utf-8") as handle:
        return VersionedStore.load_json(handle.read())


def _make_hasher(name: str):
    if name == "sha256":
        return Sha256Hasher()
    if name == "constant":
        return ConstantHasher(0)
    raise SystemExit(f"未知哈希器: {name}（可选: sha256, constant）")


def _emit(payload: str, out: Optional[str]) -> None:
    if out:
        with open(out, "w", encoding="utf-8") as handle:
            handle.write(payload + "\n")
    else:
        print(payload)


def _cmd_reconcile(args: argparse.Namespace) -> int:
    local = _load_store(args.local)
    remote_store = _load_store(args.remote)
    hasher = _make_hasher(args.hash)
    remote = Peer(remote_store, hasher=hasher, depth=args.depth, label="remote")
    result = reconcile(local, remote, mode=args.mode, hasher=hasher, depth=args.depth)
    _emit(result.dump_json(), args.out)
    if args.check:
        return 0 if result.equal else 1
    return 0


def _cmd_merge(args: argparse.Namespace) -> int:
    local = _load_store(args.local)
    remote_store = _load_store(args.remote)
    report = local.merge(remote_store.records())
    payload = {
        "merge_report": report.to_json(),
        "merged_store": local.to_json(),
    }
    _emit(json.dumps(payload, ensure_ascii=False, indent=2, sort_keys=True), args.out)
    return 0


def _build_demo_stores() -> tuple[VersionedStore, VersionedStore]:
    """合成演示数据：仅用于展示协议行为，非真实数据。"""
    alice = VersionedStore()
    alice.put("user:1", "alice-v1".encode("utf-8"), version=1)
    alice.put("user:2", "shared-old".encode("utf-8"), version=2)
    alice.put("user:3", "only-in-alice".encode("utf-8"), version=1)
    alice.delete("user:4", version=6)  # 高版本墓碑

    bob = VersionedStore()
    bob.put("user:1", "alice-v1".encode("utf-8"), version=1)
    bob.put("user:2", "shared-new".encode("utf-8"), version=5)  # 同键不同值/版本
    bob.put("user:4", "resurrected".encode("utf-8"), version=3)  # 低版本存活值
    bob.put("user:5", "only-in-bob".encode("utf-8"), version=1)
    return alice, bob


def _cmd_demo(args: argparse.Namespace) -> int:
    alice, bob = _build_demo_stores()
    hasher = _make_hasher(args.hash)
    depth = args.depth

    print("== 合成演示数据（非真实数据）==")
    print(f"A 记录数: {len(alice)}  B 记录数: {len(bob)}  哈希器: {hasher.name}  树深: {depth}")
    print()

    peer_b = Peer(bob, hasher=hasher, depth=depth, label="B")
    prob = reconcile(alice, peer_b, mode="probabilistic", hasher=hasher, depth=depth)
    print("== 概率模式（摘要剪枝）==")
    print(prob.dump_json())
    print()

    peer_b2 = Peer(bob, hasher=hasher, depth=depth, label="B")
    exact = reconcile(alice, peer_b2, mode="exact", hasher=hasher, depth=depth)
    print("== 精确模式（显式逐条核验）==")
    print(exact.dump_json())
    print()

    print("== 模式差异说明 ==")
    print(
        f"概率模式: 摘要请求 {prob.stats.digest_requests} 次, "
        f"传输记录 {prob.stats.records_sent} 条; "
        f"结论 equal={prob.equal} 为概率性（依赖哈希无碰撞）。"
    )
    print(
        f"精确模式: 摘要请求 {exact.stats.digest_requests} 次, "
        f"传输记录 {exact.stats.records_sent} 条; "
        f"结论 equal={exact.equal} 为确定性（逐条核验全部记录）。"
    )
    if prob.warnings:
        print("预警: " + " | ".join(prob.warnings))
    return 0


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="python -m setdiff",
        description="增量集合差异证明：分层哈希摘要定位差异 + 显式精确核验",
    )
    sub = parser.add_subparsers(dest="command", required=True)

    p_rec = sub.add_parser("reconcile", help="对两个存储文件执行差异对账")
    p_rec.add_argument("--local", required=True, help="本地存储 JSON 文件")
    p_rec.add_argument("--remote", required=True, help="对端存储 JSON 文件")
    p_rec.add_argument(
        "--mode",
        choices=["probabilistic", "exact"],
        default="probabilistic",
        help="probabilistic=摘要剪枝(概率性) exact=逐条核验(确定性)",
    )
    p_rec.add_argument("--hash", choices=["sha256", "constant"], default="sha256",
                       help="constant 用于测试强制碰撞")
    p_rec.add_argument("--depth", type=int, default=2, help="摘要树深度（每级16叉）")
    p_rec.add_argument("--out", help="报告输出文件（默认 stdout）")
    p_rec.add_argument("--check", action="store_true",
                       help="无差异退出码 0，有差异退出码 1")
    p_rec.set_defaults(func=_cmd_reconcile)

    p_merge = sub.add_parser("merge", help="将 remote 按 LWW 合并进 local")
    p_merge.add_argument("--local", required=True, help="本地存储 JSON 文件（将被合并更新）")
    p_merge.add_argument("--remote", required=True, help="对端存储 JSON 文件")
    p_merge.add_argument("--out", help="输出文件（默认 stdout）")
    p_merge.set_defaults(func=_cmd_merge)

    p_demo = sub.add_parser("demo", help="运行合成数据演示")
    p_demo.add_argument("--hash", choices=["sha256", "constant"], default="constant")
    p_demo.add_argument("--depth", type=int, default=2)
    p_demo.set_defaults(func=_cmd_demo)

    return parser


def main(argv: Optional[Sequence[str]] = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)
    return args.func(args)


if __name__ == "__main__":
    sys.exit(main())
