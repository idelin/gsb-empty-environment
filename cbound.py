"""cbound: 按主体（subject）的贡献裁剪与分片合并工具（仅用于合成数据）。

核心性质：
- 按主体 ID 限制保留记录数（max_records_per_subject）、分组数
  （max_groups_per_subject）与数值幅度（[lower, upper] 裁剪）。
- 保留策略是主体当前有效记录集合的纯函数：按稳定 record_id 字典序选择，
  与分片顺序、记录到达顺序无关。
- 分片合并 = 记录按 record_id 去重后的并集 + 重新执行同一选择函数，
  因此合并不绕过全局上限；同一 record_id 换分片重复提交会被去重。
- 撤回（retract）/ 更正（correct）只修改有效记录集合，随后重算选择，
  之前因上限被丢弃的候选记录会自动补回。
- 本库不做任何噪声添加，不提供形式化差分隐私证明；统计实验（如 L1
  枚举检查）只是对确定性裁剪界的一致性验证。
"""

from __future__ import annotations

import json
from dataclasses import dataclass, field


@dataclass(frozen=True)
class Params:
    """裁剪参数。lower <= upper；两个上限均 >= 1。"""

    max_records_per_subject: int = 2
    max_groups_per_subject: int = 2
    lower: float = -5.0
    upper: float = 5.0

    def __post_init__(self) -> None:
        if self.max_records_per_subject < 1:
            raise ValueError("max_records_per_subject 必须 >= 1")
        if self.max_groups_per_subject < 1:
            raise ValueError("max_groups_per_subject 必须 >= 1")
        if self.lower > self.upper:
            raise ValueError("lower 必须 <= upper")

    @property
    def l1_sum_bound(self) -> float:
        """求和统计量下，单主体 L1 贡献的确定性上界。"""
        return self.max_records_per_subject * max(abs(self.lower), abs(self.upper))


@dataclass(frozen=True)
class Record:
    """一条合成记录。record_id 为全局稳定唯一 ID。"""

    record_id: str
    subject_id: str
    group: str
    value: float


def clip_value(value: float, lower: float, upper: float) -> float:
    return min(max(value, lower), upper)


def select_retained(records: list[Record], params: Params) -> list[Record]:
    """确定性保留策略：仅依赖记录集合本身，与输入顺序无关。

    1. 组选择：每组按组内最小 record_id 排序，取前 max_groups_per_subject 组；
    2. 记录选择：在入选组内按 record_id 字典序取前 max_records_per_subject 条。
    """
    ordered = sorted(records, key=lambda r: r.record_id)
    group_rank: dict[str, str] = {}
    for rec in ordered:
        if rec.group not in group_rank:
            group_rank[rec.group] = rec.record_id
    allowed_groups = {
        g for g, _ in sorted(group_rank.items(), key=lambda kv: (kv[1], kv[0]))[
            : params.max_groups_per_subject
        ]
    }
    chosen = [r for r in ordered if r.group in allowed_groups]
    return chosen[: params.max_records_per_subject]


class ContributionBounder:
    """跨分片的贡献裁剪状态。所有上限均为全局（合并后仍成立）。"""

    def __init__(self, params: Params):
        self.params = params
        self._records: dict[str, Record] = {}  # 有效记录：record_id -> Record
        self.audit: list[dict] = []  # 事件审计日志
        self._seq = 0

    # ---- 内部 ----

    def _log(self, event: str, **detail) -> dict:
        self._seq += 1
        entry = {"seq": self._seq, "event": event, **detail}
        self.audit.append(entry)
        return entry

    def _clip_evidence(self, rec: Record) -> dict:
        clipped = clip_value(rec.value, self.params.lower, self.params.upper)
        return {
            "record_id": rec.record_id,
            "subject_id": rec.subject_id,
            "group": rec.group,
            "raw_value": rec.value,
            "clipped_value": clipped,
            "was_clipped": clipped != rec.value,
        }

    # ---- 写入 ----

    def ingest_shard(self, records: list[Record], shard: str = "") -> list[dict]:
        """摄入一个分片。重复 record_id（无论来自哪个分片）被去重。"""
        evidence = []
        for rec in records:
            if rec.record_id in self._records:
                evidence.append(
                    self._log(
                        "duplicate",
                        shard=shard,
                        record_id=rec.record_id,
                        subject_id=rec.subject_id,
                        reason="record_id 已存在，去重丢弃（换分片不能绕过）",
                    )
                )
                continue
            self._records[rec.record_id] = rec
            evidence.append(self._log("ingest", shard=shard, **self._clip_evidence(rec)))
        return evidence

    def retract(self, record_id: str) -> dict:
        """撤回一条记录；随后重算选择，候选记录自动补回。"""
        if record_id not in self._records:
            return self._log("retract_miss", record_id=record_id, reason="记录不存在")
        rec = self._records.pop(record_id)
        return self._log(
            "retract",
            record_id=record_id,
            subject_id=rec.subject_id,
            reason="已移除；该主体将按剩余记录重算保留集（候选补回）",
        )

    def correct(self, rec: Record) -> dict:
        """更正一条已存在记录（同 record_id，新取值/分组）。"""
        if rec.record_id not in self._records:
            return self._log(
                "correct_miss", record_id=rec.record_id, reason="记录不存在，按未更正处理"
            )
        self._records[rec.record_id] = rec
        return self._log("correct", **self._clip_evidence(rec))

    # ---- 派生结果（纯函数式重算，保证顺序无关） ----

    def subjects(self) -> list[str]:
        return sorted({r.subject_id for r in self._records.values()})

    def retained(self) -> list[Record]:
        out: list[Record] = []
        for sid in self.subjects():
            mine = [r for r in self._records.values() if r.subject_id == sid]
            out.extend(select_retained(mine, self.params))
        return sorted(out, key=lambda r: r.record_id)

    def clipping_evidence(self) -> list[dict]:
        """每条有效记录的裁剪证据 + 当前保留状态与丢弃原因。"""
        kept = {r.record_id for r in self.retained()}
        rows = []
        for rec in sorted(self._records.values(), key=lambda r: r.record_id):
            ev = self._clip_evidence(rec)
            if rec.record_id in kept:
                ev.update(status="retained", drop_reason="")
            else:
                ev.update(status="dropped",
                          drop_reason=self._drop_reason(rec))
            rows.append(ev)
        return rows

    def _drop_reason(self, rec: Record) -> str:
        same_subject = [
            r for r in self._records.values() if r.subject_id == rec.subject_id
        ]
        ordered = sorted(same_subject, key=lambda r: r.record_id)
        group_rank: dict[str, str] = {}
        for r in ordered:
            group_rank.setdefault(r.group, r.record_id)
        allowed_groups = {
            g for g, _ in sorted(group_rank.items(), key=lambda kv: (kv[1], kv[0]))[
                : self.params.max_groups_per_subject
            ]
        }
        if rec.group not in allowed_groups:
            return "超出分组数上限"
        return "超出记录数上限"

    def aggregate_sum(self) -> float:
        return sum(
            clip_value(r.value, self.params.lower, self.params.upper)
            for r in self.retained()
        )

    # ---- 合并 ----

    def merge(self, other: "ContributionBounder") -> "ContributionBounder":
        """合并两个状态：record_id 去重的并集 + 重算选择。上限仍是全局的。"""
        if self.params != other.params:
            raise ValueError("参数不一致，不能合并")
        merged = ContributionBounder(self.params)
        dup = 0
        for rec in list(self._records.values()) + list(other._records.values()):
            if rec.record_id in merged._records:
                dup += 1
                continue
            merged._records[rec.record_id] = rec
        merged._log(
            "merge",
            left_records=len(self._records),
            right_records=len(other._records),
            duplicates_removed=dup,
            merged_records=len(merged._records),
        )
        return merged

    def state_size_bytes(self) -> int:
        """当前合并状态（有效记录 + 参数）的 JSON 序列化大小。"""
        state = {
            "params": {
                "max_records_per_subject": self.params.max_records_per_subject,
                "max_groups_per_subject": self.params.max_groups_per_subject,
                "lower": self.params.lower,
                "upper": self.params.upper,
            },
            "records": [
                [r.record_id, r.subject_id, r.group, r.value]
                for r in sorted(self._records.values(), key=lambda r: r.record_id)
            ],
        }
        return len(json.dumps(state, sort_keys=True).encode("utf-8"))


def check_l1_bound(
    domain: dict[str, list[Record]], params: Params
) -> dict:
    """小域枚举：对主体级相邻数据集验证实际 L1 贡献不超过确定性上界。

    domain: subject_id -> 该主体的全部候选记录（合成小域）。
    枚举所有主体子集 D，对每个主体 s（s in D）构造相邻集 D - {s}，
    比较求和统计量之差，取最大值与 params.l1_sum_bound 对比。
    注意：这是对裁剪界的一致性检查，不是形式化隐私证明。
    """
    subjects = sorted(domain)

    def agg(sids: tuple[str, ...]) -> float:
        b = ContributionBounder(params)
        for sid in sids:
            b.ingest_shard(domain[sid], shard="enum")
        return sum(
            clip_value(r.value, params.lower, params.upper) for r in b.retained()
        )

    worst = 0.0
    worst_pair = None
    # 枚举全部主体子集（2^n，小域可行）
    for mask in range(1, 1 << len(subjects)):
        sids = tuple(s for i, s in enumerate(subjects) if mask & (1 << i))
        full = agg(sids)
        for sid in sids:
            neigh = tuple(s for s in sids if s != sid)
            delta = abs(full - agg(neigh))
            if delta > worst:
                worst = delta
                worst_pair = (sids, neigh)
    return {
        "subjects": subjects,
        "neighboring_pairs_checked": sum(
            bin(m).count("1") for m in range(1, 1 << len(subjects))
        ),
        "actual_max_l1": worst,
        "deterministic_bound": params.l1_sum_bound,
        "bound_holds": worst <= params.l1_sum_bound + 1e-9,
        "worst_pair": worst_pair,
    }
