"""核心数据模型：事实版本、字段来源、依赖、更正、推导步骤。"""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Optional

# 依赖种类
DEP_VALUE = "value"      # 读取了某字段的值
DEP_EXISTS = "exists"    # 探测了某字段的存在性
DEP_WILDCARD = "wildcard"  # 通配读取，视为依赖全部字段

# 步骤状态
STATUS_VALID = "valid"
STATUS_INVALID = "invalid"
STATUS_SUPERSEDED = "superseded"


@dataclass(frozen=True)
class FieldValue:
    """字段值及其来源（provenance）。"""
    value: Any
    source: str


@dataclass(frozen=True)
class FactVersion:
    """事实的某一个不可变版本。fact_id 稳定，version 单调递增。"""
    fact_id: str
    version: int
    fields: dict[str, FieldValue]

    def has(self, name: str) -> bool:
        return name in self.fields

    def value(self, name: str) -> Any:
        return self.fields[name].value

    def source_of(self, name: str) -> str:
        return self.fields[name].source


@dataclass(frozen=True)
class Dependency:
    """一次被记录的读取行为，用于精确的失效判定与重放。"""
    fact_id: str
    fact_version: int
    kind: str  # DEP_VALUE / DEP_EXISTS / DEP_WILDCARD
    field: Optional[str] = None


@dataclass(frozen=True)
class Correction:
    """对单个事实的一次更正：修改/新增字段（带来源）或删除字段。"""
    fact_id: str
    changes: dict[str, tuple[Any, str]] = field(default_factory=dict)
    removals: frozenset[str] = frozenset()
    expected_version: Optional[int] = None  # 乐观并发控制


@dataclass
class StepRecord:
    """一次推导步骤的版本化记录（证据）。step_id 稳定，version 递增。"""
    step_id: str
    version: int
    rule_id: str
    rule_version: int
    dependencies: tuple[Dependency, ...]
    inputs: dict[str, dict[str, Any]]       # 实际读到的字段值快照
    existence: dict[str, dict[str, bool]]   # 实际做过的存在性探测结果
    output: Any
    status: str = STATUS_VALID
    invalid_reason: Optional[str] = None

    @property
    def fact_versions(self) -> dict[str, int]:
        out: dict[str, int] = {}
        for dep in self.dependencies:
            out.setdefault(dep.fact_id, dep.fact_version)
        return out


@dataclass(frozen=True)
class FieldDiff:
    """两个事实版本之间的字段级差异。"""
    value_changed: frozenset[str]
    added: frozenset[str]
    removed: frozenset[str]

    @property
    def any_change(self) -> bool:
        return bool(self.value_changed or self.added or self.removed)


def diff_fields(old: FactVersion, new: FactVersion) -> FieldDiff:
    old_keys, new_keys = set(old.fields), set(new.fields)
    added = new_keys - old_keys
    removed = old_keys - new_keys
    changed = {
        k for k in old_keys & new_keys
        if old.fields[k].value != new.fields[k].value
    }
    return FieldDiff(frozenset(changed), frozenset(added), frozenset(removed))
