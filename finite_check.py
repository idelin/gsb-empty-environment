"""小有限域上的邻接枚举核验与“达到界”的邻接见证搜索。

方法：固定少量主体、每主体行数上限 m、有限取值网格与固定组域，
枚举全部数据集与全部主体级相邻对 (D, D')（D' 把某个主体在所有表中的
行同时替换为任意合法行集），计算查询输出的 L1 距离，验证其不超过
analyze 给出的保守上界，并记录达到最大观测值的相邻对作为见证。

注意：这是合成数据上的工程一致性核验，用于发现推导错误并展示界的
可达性（紧性证据）；它不构成形式化隐私证明，统计实验不能替代证明。
"""

from __future__ import annotations

from dataclasses import dataclass
from itertools import combinations_with_replacement, product
from typing import Dict, Optional, Sequence, Tuple

from sensitivity import (
    Database,
    QueryExpr,
    Table,
    analyze,
    eval_query,
    l1_distance,
)

CellRows = Tuple[Tuple[str, float], ...]  # 一个主体在一张表中的行集
Dataset = Tuple[Tuple[CellRows, ...], ...]  # dataset[表][主体] = 行集


def subject_row_options(table: Table, grid: Sequence[float]) -> Tuple[CellRows, ...]:
    """一个主体在一张表中的全部合法行集（0..m 行的多重集）。"""
    if table.max_rows_per_subject is None:
        raise ValueError(f"表 '{table.name}' 无贡献上限，无法枚举")
    cells = [(g, float(v)) for g in table.groups for v in grid]
    options = [()]
    for r in range(1, table.max_rows_per_subject + 1):
        options.extend(combinations_with_replacement(cells, r))
    return tuple(options)


def enumerate_datasets(
    tables: Sequence[Table],
    subjects: Sequence[str],
    grids: Dict[str, Sequence[float]],
) -> Tuple[Tuple[CellRows, ...], ...]:
    """枚举全部数据集：每张表独立地为每个主体选一个合法行集。"""
    per_table = []
    for t in tables:
        opts = subject_row_options(t, grids[t.name])
        per_table.append(tuple(product(opts, repeat=len(subjects))))
    return tuple(product(*per_table))


def iter_neighbors(
    dataset: Dataset,
    options_per_table: Tuple[Tuple[CellRows, ...], ...],
    subject_idx: int,
    num_tables: int,
):
    """生成把第 subject_idx 个主体（在所有表中同时）替换后的相邻数据集。"""
    alt_lists = []
    for t in range(num_tables):
        current = dataset[t][subject_idx]
        alt_lists.append([o for o in options_per_table[t] if o != current])
    for alt in product(*alt_lists):
        new_dataset = []
        for t in range(num_tables):
            column = list(dataset[t])
            column[subject_idx] = alt[t]
            new_dataset.append(tuple(column))
        yield tuple(new_dataset)


def to_database(
    dataset: Dataset, tables: Sequence[Table], subjects: Sequence[str]
) -> Database:
    db: Database = {}
    for t_idx, table in enumerate(tables):
        rows = []
        for s_idx, subject in enumerate(subjects):
            for (g, v) in dataset[t_idx][s_idx]:
                rows.append((subject, g, v))
        db[table.name] = rows
    return db


def format_dataset(
    dataset: Dataset, tables: Sequence[Table], subjects: Sequence[str]
) -> str:
    parts = []
    for t_idx, table in enumerate(tables):
        per_subject = {
            subjects[s_idx]: list(dataset[t_idx][s_idx])
            for s_idx in range(len(subjects))
        }
        parts.append(f"{table.name}={per_subject}")
    return "{" + ", ".join(parts) + "}"


@dataclass
class CheckResult:
    bound: float
    max_observed: float
    ok: bool
    num_datasets: int
    num_adjacent_pairs: int
    witness: Optional[Tuple[Dataset, Dataset, str]]  # (D, D', 被替换主体)

    def summary(self) -> str:
        status = "通过：上界未被突破" if self.ok else "失败：观测值超过上界！"
        return (
            f"上界={self.bound:g}, 最大观测 L1={self.max_observed:g}, "
            f"数据集数={self.num_datasets}, 相邻对数={self.num_adjacent_pairs}, {status}"
        )


def check_on_finite_domain(
    query: QueryExpr,
    tables: Sequence[Table],
    subjects: Sequence[str],
    grids: Dict[str, Sequence[float]],
    max_datasets: int = 20000,
) -> CheckResult:
    """枚举核验：所有相邻对的输出 L1 距离不超过 analyze 给出的上界。"""
    bound, _ = analyze(query)
    tables = tuple(tables)
    subjects = tuple(subjects)
    options_per_table = tuple(
        subject_row_options(t, grids[t.name]) for t in tables
    )
    datasets = enumerate_datasets(tables, subjects, grids)
    if len(datasets) > max_datasets:
        raise ValueError(
            f"有限域过大（{len(datasets)} 个数据集 > {max_datasets}），请缩小参数"
        )

    outputs: Dict[Dataset, object] = {}

    def output_of(ds: Dataset):
        if ds not in outputs:
            outputs[ds] = eval_query(query, to_database(ds, tables, subjects))
        return outputs[ds]

    max_observed = 0.0
    witness = None
    num_pairs = 0
    for ds in datasets:
        out_a = output_of(ds)
        for s_idx, subject in enumerate(subjects):
            for ds2 in iter_neighbors(ds, options_per_table, s_idx, len(tables)):
                dist = l1_distance(out_a, output_of(ds2))  # type: ignore[arg-type]
                num_pairs += 1
                if dist > max_observed + 1e-12:
                    max_observed = dist
                    witness = (ds, ds2, subject)
    ok = max_observed <= bound + 1e-9
    return CheckResult(
        bound=bound,
        max_observed=max_observed,
        ok=ok,
        num_datasets=len(datasets),
        num_adjacent_pairs=num_pairs,
        witness=witness,
    )
