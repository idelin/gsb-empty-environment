"""命令行入口：在合成数据上运行 k-匿名全局泛化搜索。"""

from __future__ import annotations

import argparse
import json

from .dataset import generate_synthetic
from .report import build_report, verify_report
from .search import beam_search, exact_search


def main(argv=None):
    parser = argparse.ArgumentParser(
        prog="k_anonymizer",
        description="有限泛化树上的 k-匿名全局层级组合搜索（仅合成数据，非形式化隐私证明）",
    )
    parser.add_argument("--n", type=int, default=200, help="合成记录数（默认 200）")
    parser.add_argument("--k", type=int, default=5, help="k-匿名约束（默认 5）")
    parser.add_argument("--seed", type=int, default=42, help="合成数据随机种子（默认 42）")
    parser.add_argument("--mode", choices=["exact", "beam"], default="exact",
                        help="exact=精确穷举；beam=预算截断搜索（默认 exact）")
    parser.add_argument("--beam-width", type=int, default=4, help="beam 模式前沿宽度（默认 4）")
    parser.add_argument("--budget", type=int, default=10_000, help="beam 模式状态评估预算（默认 10000）")
    args = parser.parse_args(argv)

    records, hierarchies = generate_synthetic(args.n, args.seed)
    if args.mode == "exact":
        result = exact_search(records, hierarchies, args.k)
    else:
        result = beam_search(records, hierarchies, args.k,
                             width=args.beam_width, budget=args.budget)
    params = {
        "mode": args.mode,
        "seed": args.seed,
        "beam_width": args.beam_width if args.mode == "beam" else None,
        "budget": args.budget if args.mode == "beam" else None,
    }
    report = build_report(result, records, hierarchies, args.k, params=params)
    report["verified"] = verify_report(report, records, hierarchies, args.k)
    print(json.dumps(report, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
