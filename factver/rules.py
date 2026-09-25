"""规则与读取追踪：规则通过 FactReader 读取事实，实际读取被精确记录。"""
from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Callable

from .model import DEP_EXISTS, DEP_VALUE, DEP_WILDCARD, Dependency, FactVersion


class FactReader:
    """规则执行期间的读取入口；记录实际依赖、输入快照与存在性探测。"""

    def __init__(self, resolver: Callable[[str], FactVersion]) -> None:
        self._resolver = resolver
        self.dependencies: list[Dependency] = []
        self.inputs: dict[str, dict[str, Any]] = {}
        self.existence: dict[str, dict[str, bool]] = {}

    def value(self, fact_id: str, field: str) -> Any:
        fv = self._resolver(fact_id)
        if field not in fv.fields:
            raise KeyError(f"{fact_id}.{field} missing at v{fv.version}")
        self.dependencies.append(Dependency(fact_id, fv.version, DEP_VALUE, field))
        self.inputs.setdefault(fact_id, {})[field] = fv.fields[field].value
        return fv.fields[field].value

    def exists(self, fact_id: str, field: str) -> bool:
        fv = self._resolver(fact_id)
        self.dependencies.append(Dependency(fact_id, fv.version, DEP_EXISTS, field))
        present = field in fv.fields
        self.existence.setdefault(fact_id, {})[field] = present
        return present

    def snapshot(self, fact_id: str) -> dict[str, Any]:
        """通配读取：视为依赖该事实的全部字段。"""
        fv = self._resolver(fact_id)
        self.dependencies.append(Dependency(fact_id, fv.version, DEP_WILDCARD))
        data = {name: fv.fields[name].value for name in sorted(fv.fields)}
        self.inputs[fact_id] = data
        return dict(data)


@dataclass(frozen=True)
class Rule:
    """规则：稳定 rule_id + version，derive 为确定性纯函数。"""
    rule_id: str
    version: int
    derive: Callable[[FactReader], Any]
