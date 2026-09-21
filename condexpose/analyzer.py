"""带条件的依赖暴露分析器（DRed 增量最小不动点）。

事实（事实值是 ROBDD 条件节点，不是布尔）:

* ``("R", pkg)``            运行闭包中包可达（public 边可达；root 自己恒可达）
* ``("P0", pkg)``           pkg 是 root 的直接 private 依赖
* ``("B", pkg)``            构建闭包中包可达（沿 build 边，build 闭包彼此可达）
* ``("F", scope, pkg, name, src)``  符号流
      scope = "rt"：运行命名空间（root 可见）
      scope = "priv"：经 private 边直接抬到 root 的流
      scope = "build"：构建闭包命名空间

每个事实由若干“支持”（support）派生：支持值 = 其各前提事实条件的合取再 AND 边/守卫条件；
事实条件 = 所有支持值的析取（= 最小不动点）。

增量删除采用 DRed（delete/rederive）：先删掉被直接影响的支持、
对可能失去依据的事实做过度删除，再用仍然成立的其他支持重新推导；
纯靠引用计数无法回收的循环可达性由此被正确清除。
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Iterable

from .bdd import BDDManager, FALSE, TRUE
from .model import BUILD, PRIVATE, PUBLIC, ROOT_ID, Edge, PackageGraph, Source


@dataclass(eq=False)
class Support:
    """一条派生依据。guard 为静态条件（边条件/包守卫），prems 为前提事实。"""

    key: tuple
    guard: int = TRUE
    prems: tuple = ()
    edge_key: int | None = None
    tag: str = ""
    head: Any = None


@dataclass
class UpdateReport:
    kind: str
    edge_key: int | None
    supports_removed: int = 0
    supports_added: int = 0
    facts_touched: int = 0          # 条件发生变化的事实数
    facts_overdeleted: int = 0
    packages_touched: int = 0
    bdd_nodes: int = 0


@dataclass
class ExposureRecord:
    name: str
    source: Source
    condition: int


@dataclass
class Conflict:
    name: str
    sources: tuple[Source, Source]
    condition: int


@dataclass
class SupplyPath:
    name: str
    source: Source
    condition: int
    assignment: dict[str, bool]
    # 元素: ("own", pkg, symbol) / ("edge", edge_key, at_pkg, visible_name)
    hops: list[tuple]
    packages: list[str]


class ExposureAnalyzer:
    def __init__(self, graph: PackageGraph):
        self.graph = graph
        self.bdd: BDDManager = graph.bdd

        self.value: dict[Any, int] = {}        # 事实 -> 条件
        self.supports: dict[Any, list[Support]] = {}
        self.dependents: dict[Any, set[Support]] = {}
        self.support_edges: dict[int, list[Support]] = {}
        # 已物化的 F 事实集合（用于按需生成流支持）
        self.materialized: set[Any] = set()

        self._register_static_supports()
        # 初始全量装载：所有“无前提支持”的事实 + 所有包自有符号。
        seeds = []
        for fact, bucket in self.supports.items():
            if any(not support.prems for support in bucket):
                seeds.append(fact)
        for pkg_id, pkg in self.graph.packages.items():
            for symbol in pkg.symbols:
                if pkg_id == ROOT_ID:
                    source = self.graph.source_of(pkg_id, symbol)
                    for visible in self._apply_policy_all(pkg_id, symbol):
                        seeds.append(("F", "rt", pkg_id, visible,
                                      source.as_tuple()))
                else:
                    seeds.append(self._own_fact(pkg_id, symbol))
        self._insert_fixpoint(seeds)

    # ------------------------------------------------------------------
    # 事实构造
    # ------------------------------------------------------------------
    def _own_fact(self, pkg_id: str, symbol: str) -> tuple:
        source = self.graph.source_of(pkg_id, symbol)
        return ("F", "rt", pkg_id, symbol, source.as_tuple())

    def _apply_policy_lookup(self, pkg_id: str, incoming_name: str) -> str | None:
        """符号进入 pkg 时应用其 exports 映射：返回可见名，None 表示隐藏。

        无映射时原名可见；rename 只作用于映射中出现的原名；hide 优先。
        """
        pkg = self.graph.packages[pkg_id]
        if incoming_name in pkg.hide:
            return None
        for exposed, original in pkg.rename.items():
            if original == incoming_name:
                return exposed
        return incoming_name

    def _apply_policy_all(self, pkg_id: str, incoming_name: str) -> list[str]:
        """原名可扇出为多个别名；被 hide 时返回空。"""
        pkg = self.graph.packages[pkg_id]
        if incoming_name in pkg.hide:
            return []
        aliases = [exposed for exposed, original in pkg.rename.items()
                   if original == incoming_name]
        return aliases or [incoming_name]

    # ------------------------------------------------------------------
    # 静态支持注册
    # ------------------------------------------------------------------
    def _register_static_supports(self) -> None:
        # root 恒可达
        self._add_support(("R", ROOT_ID),
                          Support(("root"), TRUE, (), tag="root-seed"))
        for edge in self.graph.active_edges():
            self._register_edge_supports(edge)
        # 各包自有符号（无条件）；root 处按 exports 扇出为多个别名
        for pkg_id, pkg in self.graph.packages.items():
            for symbol in pkg.symbols:
                if pkg_id == ROOT_ID:
                    source = self.graph.source_of(pkg_id, symbol)
                    for visible in self._apply_policy_all(pkg_id, symbol):
                        fact = ("F", "rt", pkg_id, visible, source.as_tuple())
                        self._add_support(
                            fact,
                            Support((pkg_id, symbol, visible), TRUE, (),
                                    tag="own"))
                else:
                    fact = self._own_fact(pkg_id, symbol)
                    self._add_support(fact,
                                      Support((pkg_id, symbol), TRUE, (),
                                              tag="own"))

    def _register_edge_supports(self, edge: Edge) -> None:
        if edge.kind == PUBLIC:
            # R(to) :- R(from) && edge.cond
            self._add_support(
                ("R", edge.to),
                Support(("R-edge", edge.key), edge.condition,
                        (("R", edge.from_),), edge.key, "R-public"),
            )
        elif edge.kind == PRIVATE and edge.from_ == ROOT_ID:
            # P0(to) 恒真守卫上的边条件（直接 private 依赖标记）
            self._add_support(
                ("P0", edge.to),
                Support(("P0-edge", edge.key), edge.condition,
                        (), edge.key, "P0"),
            )
        elif edge.kind == BUILD:
            if edge.from_ == ROOT_ID:
                # B(root)：构建闭包入口标记（无条件；边条件在 B(to) 一侧）
                self._add_support(
                    ("B", ROOT_ID),
                    Support(("B-root", edge.key), TRUE,
                            (), edge.key, "B-root"),
                )
            # B(to) :- B(from) && edge.cond
            self._add_support(
                ("B", edge.to),
                Support(("B-edge", edge.key), edge.condition,
                        (("B", edge.from_),), edge.key, "B"),
            )
            # 进入构建闭包的包，其自有符号进入 build 命名空间
            target = self.graph.packages[edge.to]
            for symbol in target.symbols:
                source = self.graph.source_of(edge.to, symbol)
                fact = ("F", "build", edge.to, symbol, source.as_tuple())
                self._add_support(
                    fact,
                    Support(("B-own", edge.key, symbol), edge.condition,
                            (("B", edge.to),), edge.key, "B-own"),
                )
            if edge.from_ == ROOT_ID:
                root_pkg = self.graph.packages[ROOT_ID]
                for symbol in root_pkg.symbols:
                    source = self.graph.source_of(ROOT_ID, symbol)
                    for visible in self._apply_policy_all(ROOT_ID, symbol):
                        fact = ("F", "build", ROOT_ID, visible,
                                source.as_tuple())
                        self._add_support(
                            fact,
                            Support(("B-own-root", edge.key, symbol, visible),
                                    edge.condition, (("B", ROOT_ID),),
                                    edge.key, "B-own"),
                        )

    def _rebuild_edge_flow_supports(self, edge: Edge) -> None:
        """为与该边相关的已存在流事实重新生成传播支持。

        边条件被修改（旧支持删除、新支持登记）后，结构性的 R/B/P0 支持
        已在 _register_edge_supports 中重建；流支持是按“已物化的符号流”
        惰性生成的，这里遍历所有受影响作用域中的 F 事实补做物化。
        """
        scopes = ["rt"] if edge.kind in (PUBLIC, PRIVATE) else []
        if edge.kind == BUILD:
            scopes = ["build"]
        affected_facts = [
            fact for fact in list(self.materialized)
            if fact[0] == "F"
            and fact[1] in scopes
            and fact[2] in (edge.from_, edge.to)
        ]
        for fact in affected_facts:
            # 物化过程会检查去重；先临时摘除再注册等价支持的工作由
            # 上层“删除旧支持”保证，这里只负责补登记缺失的支持。
            self._materialize_flow_for_edge(fact, edge)

    def _materialize_flow_for_edge(self, fact: Any, edge: Edge) -> None:
        _, scope, pkg_id, name, src_tuple = fact
        if scope == "priv" or pkg_id != edge.from_:
            return
        if scope == "rt" and edge.kind == PRIVATE:
            if edge.from_ != ROOT_ID:
                return
            incoming_fact = ("F", scope, edge.to, name, src_tuple)
            for visible in self._apply_policy_all(ROOT_ID, name):
                head = ("F", "priv", ROOT_ID, visible, src_tuple)
                if any(sup.edge_key == edge.key
                       for sup in self.supports.get(head, [])):
                    continue
                self._add_support(
                    head,
                    Support(("F-priv", edge.key, name, visible, src_tuple),
                            edge.condition,
                            (("P0", edge.to), incoming_fact),
                            edge.key, "F-private"),
                )
            return
        if (scope == "rt" and edge.kind != PUBLIC) or            (scope == "build" and edge.kind != BUILD):
            return
        incoming_fact = ("F", scope, edge.to, name, src_tuple)
        reach_premise = ("R", edge.to) if scope == "rt" else ("B", edge.to)
        for visible in self._apply_policy_all(edge.from_, name):
            head = ("F", scope, edge.from_, visible, src_tuple)
            if any(sup.edge_key == edge.key and sup.tag == "F-flow"
                   for sup in self.supports.get(head, [])):
                continue
            self._add_support(
                head,
                Support(("F-flow", scope, edge.key, name, visible, src_tuple),
                        edge.condition,
                        (reach_premise, incoming_fact),
                        edge.key, "F-flow"),
            )

    # ------------------------------------------------------------------
    # 支持索引维护
    # ------------------------------------------------------------------
    def _add_support(self, fact: Any, support: Support) -> None:
        support.head = fact
        self.supports.setdefault(fact, []).append(support)
        for premise in support.prems:
            self.dependents.setdefault(premise, set()).add(support)
        if support.edge_key is not None:
            self.support_edges.setdefault(support.edge_key, []).append(
                (fact, support))

    def _remove_support(self, fact: Any, support: Support) -> None:
        bucket = self.supports.get(fact)
        if bucket and support in bucket:
            bucket.remove(support)
            if not bucket:
                del self.supports[fact]
        for premise in support.prems:
            dep = self.dependents.get(premise)
            if dep:
                dep.discard(support)
                if not dep:
                    del self.dependents[premise]
        if support.edge_key is not None:
            indexed = self.support_edges.get(support.edge_key)
            if indexed:
                indexed[:] = [pair for pair in indexed if pair[1] is not support]

    # ------------------------------------------------------------------
    # 流支持按需物化
    # ------------------------------------------------------------------
    def _incoming_edges_for(self, pkg_id: str, scope: str) -> list[Edge]:
        result = []
        for key in self.graph.in_edges.get(pkg_id, []):
            edge = self.graph.edges[key]
            if edge.deleted:
                continue
            if scope == "rt" and edge.kind in (PUBLIC, PRIVATE):
                result.append(edge)
            elif scope == "build" and edge.kind == BUILD:
                result.append(edge)
        return result

    def _materialize(self, fact: Any) -> None:
        """为新出现的 F 事实生成沿入边继续传播所需的支持。"""
        if fact in self.materialized or fact[0] != "F":
            return
        self.materialized.add(fact)
        _, scope, pkg_id, name, src_tuple = fact
        # priv 流只用于“直达 root”，不再继续传播
        if scope == "priv":
            return

        for edge in self._incoming_edges_for(pkg_id, scope):
            incoming_fact = (
                "F", scope, edge.to, name, src_tuple
            )
            if edge.kind == PRIVATE:
                if edge.from_ != ROOT_ID:
                    continue
                # private 直接依赖：依赖处符号直达 root，且不继续传递
                for visible in self._apply_policy_all(ROOT_ID, name):
                    head = ("F", "priv", ROOT_ID, visible, src_tuple)
                    self._add_support(
                        head,
                        Support(("F-priv", edge.key, name, visible, src_tuple),
                                edge.condition,
                                (("P0", edge.to), incoming_fact),
                                edge.key, "F-private"),
                    )
                continue

            # public（rt 内）或 build 边（build 内）：导入者按 exports 扇出
            reach_premise = ("R", edge.to) if scope == "rt" else ("B", edge.to)
            for visible in self._apply_policy_all(edge.from_, name):
                head = ("F", scope, edge.from_, visible, src_tuple)
                self._add_support(
                    head,
                    Support(("F-flow", scope, edge.key, name, visible,
                             src_tuple),
                            edge.condition,
                            (reach_premise, incoming_fact),
                            edge.key, "F-flow"),
                )
        # 递归物化：若入边带来的“上游事实”已存在却尚未物化，补做一次
        for edge in self._incoming_edges_for(pkg_id, scope):
            upstream = ("F", scope, edge.to, name, src_tuple)
            if upstream in self.value:
                self._materialize(upstream)

    # ------------------------------------------------------------------
    # 支持值 / 头值
    # ------------------------------------------------------------------
    def _support_value(self, support: Support) -> int:
        result = support.guard
        for premise in support.prems:
            result = self.bdd.and_(result, self.value.get(premise, FALSE))
            if result == FALSE:
                break
        return result

    def _head_value(self, fact: Any) -> int:
        result = FALSE
        for support in self.supports.get(fact, ()):  # type: ignore[arg-type]
            result = self.bdd.or_(result, self._support_value(support))
        return result

    # ------------------------------------------------------------------
    # 增量上升（insert / increase）
    # ------------------------------------------------------------------
    def _insert_fixpoint(self, facts: Iterable[Any]) -> set[Any]:
        queue = list(dict.fromkeys(facts))
        changed: set[Any] = set()
        queued: set[Any] = set(queue)
        while queue:
            fact = queue.pop(0)
            queued.discard(fact)
            if fact[0] == "F" and fact not in self.materialized:
                self._materialize(fact)
            new_value = self._head_value(fact)
            old_value = self.value.get(fact, FALSE)
            if new_value == old_value:
                continue
            self.value[fact] = new_value
            changed.add(fact)
            for support in self.dependents.get(fact, ()):
                head = support.head
                if head is not None and head not in queued:
                    queue.append(head)
                    queued.add(head)
        return changed

    # ------------------------------------------------------------------
    # DRed：删除 / 过度删除 / 重导
    # ------------------------------------------------------------------
    def _supports_referencing(self, fact: Any) -> list[Support]:
        return list(self.dependents.get(fact, ()))

    def _delete_edge_update(self, edge: Edge) -> UpdateReport:
        report = UpdateReport("delete", edge.key,
                              bdd_nodes=self.bdd.node_count())

        # 1) 移除该边登记的、当前仍挂在事实上的支持
        indexed = [
            (head, support)
            for head, support in self.support_edges.get(edge.key, [])
            if support in self.supports.get(head, [])
        ]
        for head, support in indexed:
            self._remove_support(head, support)
            report.supports_removed += 1

        # 2) 过度删除：把“支持被删除波及到的”事实（含其在依赖图中的
        #    传递消费者）统一过度删除。不在这里逐个判断是否还有替代支持——
        #    环中事实此刻还靠环内同伴互相支撑，但整环已无外部依据；
        #    是否真正失去依据交给第 3 步 rederive 判定。
        seeds = {head for head, _ in indexed}
        affected: set[Any] = set()
        queue = list(seeds)
        while queue:
            fact = queue.pop()
            if fact in affected:
                continue
            affected.add(fact)
            for support in self._supports_referencing(fact):
                if support.head not in affected:
                    queue.append(support.head)
        overdeleted: dict[Any, int] = {}
        for fact in affected:
            current = self.value.get(fact, FALSE)
            if current != FALSE:
                overdeleted[fact] = current
                self.value[fact] = FALSE
                report.facts_overdeleted += 1

        # 3) 重导：用剩余支持从过度删除集合重新求最小不动点。
        #    被清零的前提暂时为 FALSE，纯循环无法自我恢复；
        #    仍有外部依据的事实（菱形删一条路径等）会被正确恢复。
        old_values = dict(overdeleted)
        self._insert_fixpoint(list(overdeleted.keys()))

        changed: set[Any] = set()
        for fact, old_value in old_values.items():
            if self.value.get(fact, FALSE) != old_value:
                changed.add(fact)
        self._garbage_collect_facts()
        report.facts_touched = len(changed)
        report.packages_touched = self._packages_in_facts(changed)
        report.bdd_nodes = self.bdd.node_count()
        return report

    def _garbage_collect_facts(self) -> None:
        # 仅清除零值；保留 materialized 登记，以便被修改边删除的流事实
        # 在重注册时仍能枚举并重建支持。materialized 是有限集合（受符号/包数约束）。
        dead = [fact for fact, value in self.value.items() if value == FALSE]
        for fact in dead:
            del self.value[fact]

    @staticmethod
    def _packages_in_facts(facts: Iterable[Any]) -> int:
        packages: set[str] = set()
        for fact in facts:
            if fact[0] in ("R", "P0", "B"):
                packages.add(fact[1])
            else:
                packages.add(fact[2])
        return len(packages)

    # ------------------------------------------------------------------
    # 公开增量 API
    # ------------------------------------------------------------------
    def delete_edge(self, edge_key: int) -> UpdateReport:
        edge = self.graph.delete_edge(edge_key)
        return self._delete_edge_update(edge)

    def modify_edge(self, edge_key: int, when: Any) -> UpdateReport:
        """修改边条件（语义上 = 删旧支持 + 加新支持）。"""
        from .model import parse_condition

        edge = self.graph.edges[edge_key]
        old_report = self._delete_edge_update(edge)
        edge.deleted = False
        edge.condition_text = when
        edge.condition = parse_condition(when, self.bdd)
        self._register_edge_supports(edge)
        self._rebuild_edge_flow_supports(edge)
        # 恢复 build 自有符号种子
        target = self.graph.packages[edge.to]
        extra_seeds: list[Any] = []
        if edge.kind == BUILD:
            for symbol in target.symbols:
                source = self.graph.source_of(edge.to, symbol)
                extra_seeds.append(("F", "build", edge.to, symbol,
                                    source.as_tuple()))
        added = len([
            pair for pair in self.support_edges.get(edge_key, [])
        ])
        seeds = self._seeds_for_edge(edge) + extra_seeds
        changed = self._insert_fixpoint(seeds)
        self._garbage_collect_facts()
        report = UpdateReport("modify", edge_key,
                              supports_removed=old_report.supports_removed,
                              supports_added=added,
                              facts_touched=old_report.facts_touched + len(changed),
                              facts_overdeleted=old_report.facts_overdeleted,
                              packages_touched=self._packages_in_facts(
                                  self._facts_for_edge(edge) | changed),
                              bdd_nodes=self.bdd.node_count())
        return report

    def _seeds_for_edge(self, edge: Edge) -> list[Any]:
        seeds: list[Any] = []
        if edge.kind == PUBLIC:
            seeds.append(("R", edge.to))
        elif edge.kind == BUILD:
            seeds.append(("B", edge.to))
            if edge.from_ == ROOT_ID:
                seeds.append(("B", ROOT_ID))
        elif edge.kind == PRIVATE and edge.from_ == ROOT_ID:
            seeds.append(("P0", edge.to))
        return seeds

    def _facts_for_edge(self, edge: Edge) -> set[Any]:
        result = set()
        for head, _ in self.support_edges.get(edge.key, ()):
            result.add(head)
        return result

    # ------------------------------------------------------------------
    # 查询
    # ------------------------------------------------------------------
    def _runtime_facts(self) -> list[tuple]:
        result = []
        for fact, condition in self.value.items():
            if fact[0] != "F" or condition == FALSE:
                continue
            scope = fact[1]
            if scope in ("rt", "priv") and fact[2] == ROOT_ID:
                result.append((fact, condition))
        return result

    def exposures(self) -> list[ExposureRecord]:
        """root 在某些配置下暴露的全部符号及其可达条件。"""
        records: dict[tuple[str, tuple], ExposureRecord] = {}
        for fact, condition in self._runtime_facts():
            _, _, _, name, src_tuple = fact
            key = (name, src_tuple)
            source = Source(*src_tuple)
            if key in records:
                records[key] = ExposureRecord(
                    name, source,
                    self.bdd.or_(records[key].condition, condition))
            else:
                records[key] = ExposureRecord(name, source, condition)
        return sorted(records.values(),
                      key=lambda record: (record.name, record.source.package,
                                          record.source.symbol))

    def exposure_condition(self, name: str) -> list[ExposureRecord]:
        return [record for record in self.exposures() if record.name == name]

    def conflicts(self) -> list[Conflict]:
        """同一名字、不同来源且条件可共同满足时为冲突。"""
        grouped: dict[str, list[ExposureRecord]] = {}
        for record in self.exposures():
            grouped.setdefault(record.name, []).append(record)
        conflicts: list[Conflict] = []
        for name, records in grouped.items():
            records.sort(key=lambda r: r.source.as_tuple())
            for i in range(len(records)):
                for j in range(i + 1, len(records)):
                    left, right = records[i], records[j]
                    if left.source.as_tuple() == right.source.as_tuple():
                        continue
                    common = self.bdd.and_(left.condition, right.condition)
                    if common != FALSE:
                        conflicts.append(Conflict(
                            name,
                            (left.source, right.source),
                            common,
                        ))
        return sorted(conflicts, key=lambda c: (c.name, c.sources[0].as_tuple()))

    def build_closure(self) -> list[ExposureRecord]:
        """build 闭包内可见、但不进入运行暴露的符号。"""
        records: dict[tuple[str, tuple], ExposureRecord] = {}
        for fact, condition in self.value.items():
            if fact[0] != "F" or fact[1] != "build" or condition == FALSE:
                continue
            _, _, pkg_id, name, src_tuple = fact
            key = (pkg_id, name, src_tuple)
            records[key] = ExposureRecord(
                f"{pkg_id}::{name}", Source(*src_tuple), condition)
        return sorted(records.values(), key=lambda r: r.name)

    def build_packages(self) -> list[str]:
        return sorted(fact[1] for fact, value in self.value.items()
                      if fact[0] == "B" and value != FALSE)

    def runtime_packages(self) -> list[str]:
        return sorted(fact[1] for fact, value in self.value.items()
                      if fact[0] == "R" and value != FALSE)

    def witness(self, name: str, source: Source | None = None,
                assignment: dict[str, bool] | None = None
                ) -> SupplyPath | None:
        """给出一个满足配置与对应供应路径；assignment 非空时在该配置下取证。"""
        candidates = [
            record for record in self.exposure_condition(name)
            if source is None or record.source.as_tuple() == source.as_tuple()
        ]
        if not candidates:
            return None
        if assignment is None:
            for record in candidates:
                assignment = self.bdd.one_model(record.condition)
                if assignment is not None:
                    chosen = record
                    break
            else:
                return None
        else:
            chosen = next(
                (record for record in candidates
                 if self.bdd.evaluate(record.condition, assignment)),
                None,
            )
            if chosen is None:
                return None
        fact = ("F", "rt", ROOT_ID, name, chosen.source.as_tuple())
        if self.value.get(fact, FALSE) == FALSE or not self.bdd.evaluate(
                self.value[fact], assignment):
            fact = ("F", "priv", ROOT_ID, name, chosen.source.as_tuple())
        hops, packages = self._path_hops(fact, assignment, set())
        return SupplyPath(name, chosen.source, chosen.condition,
                          dict(assignment), hops, packages)

    def conflict_paths(self, conflict: Conflict,
                       assignment: dict[str, bool] | None = None
                       ) -> tuple[SupplyPath | None, SupplyPath | None] | None:
        if assignment is None:
            assignment = self.bdd.one_model(conflict.condition)
            if assignment is None:
                return None
        left = self.witness(conflict.name, conflict.sources[0], assignment)
        right = self.witness(conflict.name, conflict.sources[1], assignment)
        return left, right

    def _pick_support(self, fact: Any, assignment: dict[str, bool]) -> Support | None:
        for support in self.supports.get(fact, ()):  # type: ignore[arg-type]
            value = self._support_value(support)
            if value != FALSE and self.bdd.evaluate(value, assignment):
                return support
        return None

    def _path_hops(self, fact: Any, assignment: dict[str, bool],
                   seen: set[tuple]) -> tuple[list[tuple], list[str]]:
        if fact in seen:
            return [], []
        seen = seen | {fact}
        support = self._pick_support(fact, assignment)
        if support is None:
            return [], []
        if support.tag == "own":
            _, scope, pkg_id, name, src_tuple = fact
            return [("own", pkg_id, src_tuple[2])], [pkg_id]

        hops: list[tuple] = []
        packages: list[str] = []
        for premise in support.prems:
            if premise[0] == "F" and premise[2] != ROOT_ID:
                child_hops, child_pkgs = self._path_hops(premise, assignment, seen)
                hops.extend(child_hops)
                packages.extend(child_pkgs)
        edge_key = support.edge_key
        if edge_key is not None:
            edge = self.graph.edges[edge_key]
            hops.append(("edge", edge_key, fact[2], fact[3]))
            if edge.from_ not in packages:
                packages.append(edge.from_)
        return hops, packages
