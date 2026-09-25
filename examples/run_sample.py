"""运行样例：合成三分片链的拼接，外加拒绝场景演示。

用法：python3 examples/run_sample.py
"""
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from shardstitch.stitch import Stitcher
from shardstitch.synthetic import (
    build_analytics,
    build_chain,
    build_ingest,
    build_mutual_assumption_pair,
    build_reporting,
)


def show(title, report):
    print(f"== {title} ==")
    for ref, digest in sorted(report.grounded.items()):
        print(f"  接地结论 {ref[0]}@{ref[1]}  证据摘要 {digest[:16]}…")
    print(f"  贡献计数: {report.contributions}  全局唯一证明: {report.total_unique_proofs}")
    if report.duplicates_ignored:
        print(f"  忽略的重复分片: {report.duplicates_ignored}")
    if report.cycles:
        print(f"  假设环: {list(report.cycles)}")
    for r in report.rejections:
        print(f"  拒绝 [{r.reason}] 分片={r.shard_id} 证明={r.proof_id}  {r.detail}")
    print()


def main():
    # 1) 正常链：ingest -> analytics -> reporting，并重复提交 ingest 验证去重
    chain = build_chain()
    stitcher = Stitcher()
    for shard in chain:
        stitcher.submit(shard)
    stitcher.submit(chain[0])  # 重复提交同一分片
    show("正常链（含一次重复提交）", stitcher.stitch())

    # 2) 摘要不匹配的边界证据被拒绝
    ingest = build_ingest()
    bad_analytics = build_analytics(ingest, expected_digest="f" * 64)
    reporting = build_reporting(build_analytics(ingest))  # 期望的是正确摘要
    stitcher = Stitcher()
    for shard in (ingest, bad_analytics, reporting):
        stitcher.submit(shard)
    show("边界证据摘要不匹配", stitcher.stitch())

    # 3) A 假设 B 且 B 假设 A：无根互假设，不能凭空成立
    stitcher = Stitcher()
    for shard in build_mutual_assumption_pair():
        stitcher.submit(shard)
    show("互相假设的无根环", stitcher.stitch())


if __name__ == "__main__":
    main()
