"""结果报告：输出可独立核验的等价类计数。"""

from __future__ import annotations

from .search import equivalence_classes


def build_report(result, records, hierarchies, k, params=None):
    report = {
        "status": result.status.value,
        "k": k,
        "num_records": len(records),
        "levels": list(result.levels) if result.levels is not None else None,
        "loss": result.loss,
        "explored_states": result.explored,
        "search_space_size": result.space_size,
        "search_exhausted": result.explored >= result.space_size,
    }
    if params:
        report["params"] = params
    if result.levels is not None:
        groups = equivalence_classes(records, hierarchies, result.levels)
        sizes = sorted(groups.values(), reverse=True)
        report["equivalence_classes"] = {
            "num_classes": len(sizes),
            "sizes": sizes,
            "min_size": min(sizes),
            "sum_sizes": sum(sizes),
            "k_satisfied": all(size >= k for size in sizes),
        }
    return report


def verify_report(report, records, hierarchies, k):
    """独立重算等价类，校验报告中的计数与 k 约束（防篡改/防算错）。"""
    if report["levels"] is None:
        return report["status"] in ("INFEASIBLE", "NO_SOLUTION_TRUNCATED")
    levels = tuple(report["levels"])
    groups = equivalence_classes(records, hierarchies, levels)
    sizes = sorted(groups.values(), reverse=True)
    ec = report["equivalence_classes"]
    return (
        sizes == ec["sizes"]
        and len(sizes) == ec["num_classes"]
        and sum(sizes) == ec["sum_sizes"] == len(records)
        and ec["min_size"] == min(sizes)
        and ec["k_satisfied"] == all(size >= k for size in sizes)
    )
