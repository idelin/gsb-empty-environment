"""极简关系代数执行引擎（纯标准库，不调用任何 SQL 引擎）。

语义约定（显式声明）：
- NULL 用 None 表示；比较运算遵循三值逻辑（TRUE / FALSE / UNKNOWN），
  任何与 NULL 的比较结果都是 UNKNOWN；过滤只保留结果为 TRUE 的行。
- 等值连接中 NULL 连接键永不匹配（NULL 不等于 NULL）。
- 多重集合（bag）语义：重复行保留，连接按匹配对数倍增，聚合不去重。
- ORDER BY 采用 NULLS LAST（两个方向一致），且为稳定排序：
  排序键相同（并列）时按输入原始顺序（_seq）决胜。
"""

from functools import cmp_to_key
import heapq


# ---------------- 三值逻辑 ----------------

def sql_not(v):
    return None if v is None else (not v)


def sql_and(a, b):
    if a is False or b is False:
        return False
    if a is None or b is None:
        return None
    return True


def sql_or(a, b):
    if a is True or b is True:
        return True
    if a is None or b is None:
        return None
    return False


def sql_cmp(op, a, b):
    """与 SQL 一致：任一操作数为 NULL 时结果为 UNKNOWN（None）。"""
    if a is None or b is None:
        return None
    if op == "=":
        return a == b
    if op == "<":
        return a < b
    if op == "<=":
        return a <= b
    if op == ">":
        return a > b
    if op == ">=":
        return a >= b
    if op == "<>":
        return a != b
    raise ValueError(op)


class Pred:
    """谓词：cols 为引用的列集合（供下推分析），fn 返回 True/False/None。"""

    def __init__(self, name, cols, fn):
        self.name = name
        self.cols = list(cols)
        self.fn = fn

    def __call__(self, row):
        return self.fn(row)

    def __repr__(self):
        return f"Pred({self.name})"


# ---------------- 排序 ----------------

def make_cmp(keys):
    """keys: [(col, desc_bool)]；NULLS LAST；并列时按 _seq 保持原始顺序。"""

    def cmp(r1, r2):
        for col, desc in keys:
            a, b = r1[col], r2[col]
            if a is None and b is None:
                continue
            if a is None:
                return 1  # NULLS LAST
            if b is None:
                return -1
            if a == b:
                continue
            less = a < b
            if desc:
                less = not less
            return -1 if less else 1
        s1, s2 = r1["_seq"], r2["_seq"]
        return (s1 > s2) - (s1 < s2)

    return cmp


def _kway_merge(runs, cmp_key):
    """k 路归并；比较器含 _seq 决胜，因此并列时全局稳定。"""
    heap = []
    for i, run in enumerate(runs):
        if run:
            heapq.heappush(heap, (cmp_key(run[0]), i, 0))
    out = []
    while heap:
        _, i, j = heapq.heappop(heap)
        out.append(runs[i][j])
        if j + 1 < len(runs[i]):
            heapq.heappush(heap, (cmp_key(runs[i][j + 1]), i, j + 1))
    return out


# ---------------- 计划节点 ----------------

class Node:
    def execute(self):
        raise NotImplementedError

    def columns(self):
        raise NotImplementedError

    def describe(self, indent=0):
        raise NotImplementedError


class Scan(Node):
    """分区表扫描；_seq 按分区顺序全局递增，作为稳定性的原始顺序依据。"""

    def __init__(self, name, cols, partitions):
        self.name = name
        self.cols = list(cols)
        self._parts = [[dict(zip(cols, t)) for t in part] for part in partitions]
        seq = 0
        for part in self._parts:
            for row in part:
                row["_seq"] = seq
                seq += 1

    def partitions(self):
        return [[dict(r) for r in part] for part in self._parts]

    def execute(self):
        return [r for part in self.partitions() for r in part]

    def columns(self):
        return list(self.cols)

    def describe(self, indent=0):
        return " " * indent + f"Scan({self.name}, partitions={len(self._parts)})"


class Filter(Node):
    def __init__(self, child, pred):
        self.child = child
        self.pred = pred

    def execute(self):
        # 三值逻辑：只有 TRUE 保留，UNKNOWN/FALSE 都丢弃
        return [r for r in self.child.execute() if self.pred(r) is True]

    def columns(self):
        return self.child.columns()

    def describe(self, indent=0):
        pad = " " * indent
        return f"{pad}Filter({self.pred.name})\n{self.child.describe(indent + 2)}"


class Join(Node):
    """等值连接，how in ('inner', 'left')；多重集合语义：每对匹配产生一行。"""

    def __init__(self, left, right, lkey, rkey, how="inner"):
        assert how in ("inner", "left")
        self.left = left
        self.right = right
        self.lkey = lkey
        self.rkey = rkey
        self.how = how

    def columns(self):
        return self.left.columns() + self.right.columns()

    def execute(self):
        rcols = self.right.columns()
        index = {}
        for r in self.right.execute():
            k = r[self.rkey]
            if k is None:
                continue  # NULL 连接键永不匹配
            index.setdefault(k, []).append(r)
        out = []
        for l in self.left.execute():
            k = l[self.lkey]
            matches = index.get(k) if k is not None else None
            if matches:
                for m in matches:  # bag：右侧重复键产生行倍增
                    row = dict(l)
                    row.update(m)
                    out.append(row)
            elif self.how == "left":
                row = dict(l)
                for c in rcols:
                    row[c] = None  # 补 NULL 扩展行
                out.append(row)
        for i, row in enumerate(out):
            row["_seq"] = i
        return out

    def describe(self, indent=0):
        pad = " " * indent
        head = f"{pad}{'LeftOuterJoin' if self.how == 'left' else 'InnerJoin'}({self.lkey}={self.rkey})"
        return f"{head}\n{self.left.describe(indent + 2)}\n{self.right.describe(indent + 2)}"


def _apply_agg(func, col, rows):
    if func == "count" and col == "*":
        return len(rows)  # COUNT(*) 计全部行（含 NULL 列的行）
    vals = [r[col] for r in rows if r[col] is not None]  # 其余聚合忽略 NULL
    if func == "count":
        return len(vals)
    if not vals:
        return None  # SQL：空集/全 NULL 的 SUM/AVG/MIN/MAX 为 NULL
    if func == "sum":
        return sum(vals)
    if func == "min":
        return min(vals)
    if func == "max":
        return max(vals)
    if func == "avg":
        return sum(vals) / len(vals)
    raise ValueError(func)


class Aggregate(Node):
    """分组聚合，bag 语义：不去重；NULL 是合法分组键（NULL 聚成一组）。"""

    def __init__(self, child, group_cols, aggs):
        # aggs: [(func, col_or_"*", out_name)]
        self.child = child
        self.group_cols = list(group_cols)
        self.aggs = list(aggs)

    def columns(self):
        return list(self.group_cols) + [name for _, _, name in self.aggs]

    def execute(self):
        groups = {}
        order = []
        for r in self.child.execute():
            key = tuple(r[c] for c in self.group_cols)
            if key not in groups:
                groups[key] = []
                order.append(key)
            groups[key].append(r)
        if not self.group_cols and not order:
            groups[()] = []  # 无分组列时，空输入也产生一行
            order.append(())
        out = []
        for key in order:
            rows = groups[key]
            row = dict(zip(self.group_cols, key))
            for func, col, name in self.aggs:
                row[name] = _apply_agg(func, col, rows)
            out.append(row)
        for i, row in enumerate(out):
            row["_seq"] = i
        return out

    def describe(self, indent=0):
        pad = " " * indent
        aggs = ", ".join(f"{f}({c}) AS {n}" for f, c, n in self.aggs)
        return f"{pad}Aggregate(group={self.group_cols}; {aggs})\n{self.child.describe(indent + 2)}"


class OrderLimit(Node):
    """逻辑算子：ORDER BY keys OFFSET offset LIMIT limit（全量排序后切片）。"""

    def __init__(self, child, keys, offset=0, limit=None):
        self.child = child
        self.keys = list(keys)
        self.offset = offset
        self.limit = limit

    def columns(self):
        return self.child.columns()

    def execute(self):
        rows = sorted(self.child.execute(), key=cmp_to_key(make_cmp(self.keys)))
        end = None if self.limit is None else self.offset + self.limit
        return rows[self.offset:end]

    def describe(self, indent=0):
        pad = " " * indent
        return (f"{pad}OrderLimit(keys={self.keys}, offset={self.offset}, "
                f"limit={self.limit})\n{self.child.describe(indent + 2)}")


class TopKMerge(Node):
    """Sort+Limit 的安全两阶段物理计划：各分区局部 TopK + k 路归并。

    安全性证明（局部必须取 offset+limit 行）：
    若某行位于全局有序序列的前 offset+limit 名之内，则全局至多有
    offset+limit-1 行排在它前面，其所在分区内排在它前面的行只会更少，
    故它必落在该分区局部前 offset+limit 名之内。因此每分区局部取
    offset+limit 行后归并、再施加 offset/limit，与全量排序严格等价；
    offset>0 时局部只取 limit 行会漏行（见测试与 demo 反例）。
    稳定性：比较器以 _seq 决胜，并列行保持原始输入顺序。
    """

    def __init__(self, scan, keys, offset, limit):
        self.scan = scan
        self.keys = list(keys)
        self.offset = offset
        self.limit = limit
        self.local_n = offset + limit  # 关键：局部不能少于 offset+limit

    def columns(self):
        return self.scan.columns()

    def execute(self):
        cmp_key = cmp_to_key(make_cmp(self.keys))
        runs = []
        for part in self.scan.partitions():
            runs.append(sorted(part, key=cmp_key)[: self.local_n])
        merged = _kway_merge(runs, cmp_key)
        return merged[self.offset:self.offset + self.limit]

    def describe(self, indent=0):
        pad = " " * indent
        return (f"{pad}TopKMerge(local_n={self.local_n}, offset={self.offset}, "
                f"limit={self.limit}, keys={self.keys})\n{self.scan.describe(indent + 2)}")
