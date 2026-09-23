"""计划改写规则：每条规则先构造性地验证前提，前提不成立则保留原计划。

每条规则返回 (plan, justification)：
- plan 为改写后的计划或原计划；
- justification 记录所验证的前提 / 拒绝改写的原因（即可检查的“证明”）。
"""

from engine import Aggregate, Filter, Join, OrderLimit, Scan, TopKMerge


# ---------------- 可验证的前提（约束检查即证明） ----------------

def check_unique(rows, col):
    """验证 col 在非 NULL 值上唯一（NULL 不参与等值匹配，不产生扇出）。"""
    seen = set()
    for r in rows:
        v = r[col]
        if v is None:
            continue
        if v in seen:
            return False
        seen.add(v)
    return True


def check_fk(rows_a, ak, rows_b, bk):
    """验证外键前提：A 的每个非 NULL 连接键都能在 B 的连接键中找到。"""
    keys_b = {r[bk] for r in rows_b if r[bk] is not None}
    return all(r[ak] is None or r[ak] in keys_b for r in rows_a)


# ---------------- 规则 1：Sort+Limit -> 局部 TopK + 归并 ----------------

def rewrite_topk(node):
    """OrderLimit(Scan) -> TopKMerge；仅当数据源是分区 Scan 且 limit 有限。"""
    if (isinstance(node, OrderLimit) and isinstance(node.child, Scan)
            and node.limit is not None):
        plan = TopKMerge(node.child, node.keys, node.offset, node.limit)
        why = (f"改写为局部TopK+归并：每分区取前 {plan.local_n} 行 "
               f"(offset+limit={node.offset}+{node.limit})，归并后再施加 "
               f"offset={node.offset}/limit={node.limit}；并列由 _seq 决胜保证稳定。")
        return plan, why
    return node, "不满足改写条件（非分区 Scan 上带有限 LIMIT 的 Sort），保留原计划。"


# ---------------- 规则 2：谓词下推（含外连接安全判定） ----------------

def push_filter(node):
    """Filter(Join) 的谓词下推；外连接仅在谓词 NULL 拒绝时改写。"""
    if not (isinstance(node, Filter) and isinstance(node.child, Join)):
        return node, "不是 Filter(Join) 形态，保留原计划。"
    join = node.child
    pred = node.pred
    lcols = set(join.left.columns())
    rcols = set(join.right.columns())
    pcols = set(pred.cols)

    if pcols <= lcols:
        new = Join(Filter(join.left, pred), join.right,
                   join.lkey, join.rkey, join.how)
        return new, ("谓词仅引用左表（外连接的保留侧）列：左表行的谓词值与连接"
                     "结果无关，下推等价。")

    if pcols <= rcols:
        if join.how == "inner":
            new = Join(join.left, Filter(join.right, pred),
                       join.lkey, join.rkey, "inner")
            return new, "内连接两侧谓词下推均等价。"
        # 左外连接：右表是补 NULL 侧。精确判定：对全 NULL 扩展行求值，
        # 结果不是 TRUE 即为 NULL 拒绝谓词（补 NULL 行必然被过滤掉）。
        probe = {c: None for c in join.right.columns()}
        verdict = pred(probe)
        if verdict is not True:
            new = Join(join.left, Filter(join.right, pred),
                       join.lkey, join.rkey, "inner")
            return new, (f"谓词对全 NULL 扩展行求值为 {verdict!r}（非 TRUE），"
                         "是 NULL 拒绝谓词：左外连接退化为内连接，谓词可下推。")
        return node, ("谓词对全 NULL 扩展行求值为 TRUE（如 IS NULL），不是 "
                      "NULL 拒绝谓词，下推会错误丢弃补 NULL 行，保留原计划。")

    return node, "谓词同时引用两侧列，无法下推，保留原计划。"


# ---------------- 规则 3：聚合下推（需证明连接保持左侧多重集合） ----------------

def push_aggregate(node):
    """Aggregate(Join) -> Join(Aggregate)：仅当连接不复制、不丢失左表行。"""
    if not (isinstance(node, Aggregate) and isinstance(node.child, Join)):
        return node, "不是 Aggregate(Join) 形态，保留原计划。"
    join = node.child

    used = set(node.group_cols) | {c for _, c, _ in node.aggs if c != "*"}
    if not used <= set(join.left.columns()):
        return node, "分组/聚合列涉及右表，无法只下推到左表，保留原计划。"
    if join.lkey not in node.group_cols:
        return node, "连接键不在分组列中，先聚合后无法回连，保留原计划。"

    rrows = join.right.execute()
    if not check_unique(rrows, join.rkey):
        return node, ("前提不成立：右表连接键不唯一，连接会复制左表行，"
                      "COUNT/SUM 会被放大，保留原计划。")

    if join.how == "inner":
        lrows = join.left.execute()
        if not check_fk(lrows, join.lkey, rrows, join.rkey):
            return node, ("前提不成立：缺少 左.键->右.键 外键约束，内连接可能"
                          "丢失左表行，先聚合会多算，保留原计划。")
        how, why = "inner", "右表键唯一（不放大）且外键成立（不丢失）"
    else:
        how, why = "left", "右表键唯一，左外连接中每行左表恰好出现一次"

    new = Join(Aggregate(join.left, node.group_cols, node.aggs),
               join.right, join.lkey, join.rkey, how)
    return new, f"前提已验证：{why}，连接保持左表多重集合，聚合可安全下推。"
