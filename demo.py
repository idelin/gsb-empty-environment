"""运行样例：TopK 改写、外连接谓词下推、聚合下推（含被拒绝的情形）。"""

from functools import cmp_to_key

from engine import (Aggregate, Filter, Join, OrderLimit, Pred, Scan,
                    _kway_merge, make_cmp, sql_cmp)
from planner import push_aggregate, push_filter, rewrite_topk
from synth_data import (DEPARTMENT_COLS, DEPARTMENT_PARTS, DEPT_YEARLY_COLS,
                        DEPT_YEARLY_PARTS, EMPLOYEE_COLS, EMPLOYEE_PARTS)


def emp_scan():
    return Scan("employees", EMPLOYEE_COLS, EMPLOYEE_PARTS)


def dept_scan():
    return Scan("departments", DEPARTMENT_COLS, DEPARTMENT_PARTS)


def show(title, rows, cols):
    print(f"  结果（{len(rows)} 行）[{title}]:")
    for r in rows:
        print("   ", {c: r[c] for c in cols})


def main():
    print("=" * 72)
    print("场景 1：ORDER BY e_salary DESC OFFSET 5 LIMIT 3（分区数据）")
    print("=" * 72)
    keys = [("e_salary", True)]
    logical = OrderLimit(emp_scan(), keys, offset=5, limit=3)
    plan, why = rewrite_topk(logical)
    print("改写说明:", why)
    print(plan.describe())
    cols = ["e_id", "e_name", "e_salary"]
    show("局部TopK+归并", plan.execute(), cols)
    show("全量排序(参考)", logical.execute(), cols)

    # 反例：offset=5 limit=3 时局部只取 limit=3 行会漏行
    scan = emp_scan()
    cmp_key = cmp_to_key(make_cmp(keys))
    naive_runs = [sorted(p, key=cmp_key)[:3] for p in scan.partitions()]
    naive = _kway_merge(naive_runs, cmp_key)[5:8]
    show("错误示范：局部只取3行", naive, cols)
    print("  -> 局部只取 limit=3 行漏掉了 Kim(84000)，结果与参考不一致；")
    print("     本实现局部取 offset+limit=8 行，与全量排序严格一致。\n")

    print("=" * 72)
    print("场景 2：左外连接上的谓词下推（安全 / NULL 拒绝 / 不安全）")
    print("=" * 72)
    loj = lambda: Join(emp_scan(), dept_scan(), "e_dept", "d_id", how="left")

    p_left = Pred("e_salary > 70000", ["e_salary"],
                  lambda r: sql_cmp(">", r["e_salary"], 70000))
    plan, why = push_filter(Filter(loj(), p_left))
    print("[保留侧谓词]", why)
    print(plan.describe(), "\n")

    p_nr = Pred("d_budget > 100000", ["d_budget"],
                lambda r: sql_cmp(">", r["d_budget"], 100000))
    plan, why = push_filter(Filter(loj(), p_nr))
    print("[NULL 拒绝谓词]", why)
    print(plan.describe(), "\n")

    p_unsafe = Pred("d_budget IS NULL", ["d_budget"],
                    lambda r: r["d_budget"] is None)
    plan, why = push_filter(Filter(loj(), p_unsafe))
    print("[不安全谓词]", why)
    print(plan.describe(), "\n")

    print("=" * 72)
    print("场景 3：聚合下推（前提成立 vs 前提不成立）")
    print("=" * 72)
    aggs = [("count", "*", "cnt"), ("avg", "e_salary", "avg_sal")]
    inner = Join(emp_scan(), dept_scan(), "e_dept", "d_id", how="inner")
    agg_over_join = Aggregate(inner, ["e_dept"], aggs)
    plan, why = push_aggregate(agg_over_join)
    print("[内连接+唯一键+外键]", why)
    print(plan.describe())
    show("下推计划", plan.execute(), ["e_dept", "cnt", "avg_sal"])
    show("原计划(参考)", agg_over_join.execute(), ["e_dept", "cnt", "avg_sal"])
    print()

    dup_join = Join(emp_scan(), Scan("dept_yearly", DEPT_YEARLY_COLS, DEPT_YEARLY_PARTS),
                    "e_dept", "y_dept", how="inner")
    agg2 = Aggregate(dup_join, ["e_dept"], aggs)
    plan2, why2 = push_aggregate(agg2)
    print("[连接键不唯一]", why2)
    print(plan2.describe())


if __name__ == "__main__":
    main()
