"""执行计划算子（Scan / Filter / Project / Join）与公共子计划识别。

每个算子提供：

- ``signature()``   结构化签名（可哈希），用于识别"同一个子计划"；
- ``estimate_rows()`` / ``row_width()``  代价估计用的基数与行宽；
- ``compute_cost(model)``  完整求值一次的 CPU 代价；
- ``iter_rows()``   以迭代器方式产出结果行（流式，不强制落内存）。
"""

import math

ROW_OVERHEAD = 16  # 每行固定开销（字节，估计值）
FIELD_WIDTH = 8    # 每个字段平均宽度（字节，估计值）


class Node:
    def children(self):
        return ()

    def signature(self):
        raise NotImplementedError

    def estimate_rows(self):
        raise NotImplementedError

    def row_width(self):
        raise NotImplementedError

    def compute_cost(self, model):
        raise NotImplementedError

    def iter_rows(self):
        raise NotImplementedError


class Scan(Node):
    def __init__(self, table):
        self.table = table

    def signature(self):
        return ('scan', self.table.name)

    def estimate_rows(self):
        return len(self.table.rows)

    def row_width(self):
        return ROW_OVERHEAD + FIELD_WIDTH * len(self.table.columns)

    def compute_cost(self, model):
        return len(self.table.rows) * model.scan_row

    def iter_rows(self):
        return iter(self.table.rows)


class Filter(Node):
    """只保留谓词结果 ``is True`` 的行（三值逻辑：UNKNOWN 被丢弃）。

    ``label`` 参与签名：不同过滤条件必须给不同 label，
    保证两个不同过滤不会被误认为同一个子计划。
    """

    def __init__(self, child, predicate, label, selectivity=0.5):
        self.child = child
        self.predicate = predicate
        self.label = label
        self.selectivity = selectivity

    def children(self):
        return (self.child,)

    def signature(self):
        return ('filter', self.label, self.child.signature())

    def estimate_rows(self):
        return max(1, math.ceil(self.child.estimate_rows() * self.selectivity))

    def row_width(self):
        return self.child.row_width()

    def compute_cost(self, model):
        return (self.child.compute_cost(model)
                + self.child.estimate_rows() * model.filter_row)

    def iter_rows(self):
        return (row for row in self.child.iter_rows()
                if self.predicate(row) is True)


class Project(Node):
    def __init__(self, child, indexes):
        self.child = child
        self.indexes = tuple(indexes)

    def children(self):
        return (self.child,)

    def signature(self):
        return ('project', self.indexes, self.child.signature())

    def estimate_rows(self):
        return self.child.estimate_rows()

    def row_width(self):
        return ROW_OVERHEAD + FIELD_WIDTH * len(self.indexes)

    def compute_cost(self, model):
        return (self.child.compute_cost(model)
                + self.child.estimate_rows() * model.project_row)

    def iter_rows(self):
        indexes = self.indexes
        return (tuple(row[i] for i in indexes)
                for row in self.child.iter_rows())


class Join(Node):
    """等值连接（hash join），bag 语义：匹配按多重集合乘法展开。

    连接键为 NULL 的行不参与匹配（NULL 不等于 NULL）。
    """

    def __init__(self, left, right, left_key, right_key, estimate=None):
        self.left = left
        self.right = right
        self.left_key = left_key
        self.right_key = right_key
        self._estimate = estimate

    def children(self):
        return (self.left, self.right)

    def signature(self):
        return ('join', self.left_key, self.right_key,
                self.left.signature(), self.right.signature())

    def estimate_rows(self):
        if self._estimate is not None:
            return self._estimate
        return max(self.left.estimate_rows(), self.right.estimate_rows(), 1)

    def row_width(self):
        return self.left.row_width() + self.right.row_width()

    def compute_cost(self, model):
        return (self.left.compute_cost(model)
                + self.right.compute_cost(model)
                + (self.left.estimate_rows()
                   + self.right.estimate_rows()
                   + self.estimate_rows()) * model.join_row)

    def iter_rows(self):
        buckets = {}
        for row in self.right.iter_rows():
            key = row[self.right_key]
            if key is None:
                continue
            buckets.setdefault(key, []).append(row)

        def generate():
            for left_row in self.left.iter_rows():
                key = left_row[self.left_key]
                if key is None:
                    continue
                for right_row in buckets.get(key, ()):
                    yield left_row + right_row

        return generate()


def largest_common_subplan(plan_a, plan_b):
    """返回两棵计划树中最大的公共子计划（按估计行数比较）。

    两个查询若只是顶层过滤不同，公共部分是过滤**之前**的子计划，
    因此共享/物化的永远是未过滤的结果，不会把某个消费者的过滤
    结果错误地共享给另一个消费者。
    """
    if plan_a.signature() == plan_b.signature():
        return plan_a
    best = None
    for child_a, child_b in zip(plan_a.children(), plan_b.children()):
        candidate = largest_common_subplan(child_a, child_b)
        if candidate is not None and (
                best is None
                or candidate.estimate_rows() > best.estimate_rows()):
            best = candidate
    return best
