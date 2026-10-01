"""信息损失度量（归一化 LM / 确定性惩罚，定义见 README）。

单字段单记录损失：field_loss = (leaves_under(node) - 1) / (total_leaves - 1)，
叶子为 0，根（覆盖全部叶子）为 1；组合损失为所有记录、所有字段的算术平均。
损失关于层级单调不减，这是截断搜索中剪枝的依据。
"""

from __future__ import annotations


def field_loss(hierarchy, node):
    total = hierarchy.total_leaves
    if total <= 1:
        return 0.0
    return (hierarchy.leaf_count[node] - 1) / (total - 1)


def combination_loss(records, hierarchies, levels):
    if not records:
        return 0.0
    dims = len(hierarchies)
    total = 0.0
    for rec in records:
        for i, hier in enumerate(hierarchies):
            total += field_loss(hier, hier.generalize(rec[i], levels[i]))
    return total / (len(records) * dims)
