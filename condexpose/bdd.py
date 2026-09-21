"""规范化布尔决策表示 (reduced ordered BDD)。

特性按声明顺序固定排列；``make`` 唯一化表保证同构子树只存一份，
因此“菱形/多源共享条件”只产生线性节点数，无需逐配置枚举。

节点编码：1 = 真，0 = 假，其余为非终结节点 id。
"""

from __future__ import annotations

FALSE = 0
TRUE = 1


class BDDManager:
    def __init__(self, features: list[str] | None = None):
        # 0/1 槽位预留给 FALSE/TRUE，使节点 id 可直接索引
        self.var_of: list[int] = [-1, -1]
        self.low_of: list[int] = [FALSE, TRUE]
        self.high_of: list[int] = [FALSE, TRUE]
        self.level: dict[str, int] = {}        # 特性名 -> 层级
        self.unique: dict[tuple[int, int, int], int] = {}
        self._ite_cache: dict[tuple[int, int, int], int] = {}
        self._restrict_cache: dict[tuple[int, int, int], int] = {}
        for name in features or []:
            self.declare(name)

    def declare(self, name: str) -> int:
        if name not in self.level:
            self.level[name] = len(self.level)
        return self.level[name]

    @property
    def features(self) -> list[str]:
        return [
            name
            for name, _ in sorted(self.level.items(), key=lambda kv: kv[1])
        ]

    def node_count(self) -> int:
        """可达节点总数（含两个终结点），每次动态统计。"""
        seen = {FALSE, TRUE}
        stack = [n for n in range(2, len(self.var_of))]
        while stack:
            node = stack.pop()
            if node in seen:
                continue
            seen.add(node)
            stack.extend((self.low_of[node], self.high_of[node]))
        return len(seen)

    def make(self, var: int, low: int, high: int) -> int:
        if low == high:  # 归约：两边相同则消除该变量
            return low
        key = (var, low, high)
        existing = self.unique.get(key)
        if existing is not None:
            return existing
        node = len(self.var_of)
        self.var_of.append(var)
        self.low_of.append(low)
        self.high_of.append(high)
        self.unique[key] = node
        return node

    def literal(self, name: str, positive: bool = True) -> int:
        level = self.level[name]
        return self.make(level, FALSE, TRUE) if positive else self.make(level, TRUE, FALSE)

    def ite(self, g: int, t: int, e: int) -> int:
        # 标准 ROBDD apply：取三棵树的最小顶变量展开，
        # 经由 make 自底向上归约并唯一化，保证结果是规范形式。
        if g == TRUE:
            return t
        if g == FALSE:
            return e
        if t == TRUE and e == FALSE:
            return g
        if t == e:
            return t
        key = (g, t, e)
        cached = self._ite_cache.get(key)
        if cached is not None:
            return cached
        levels = [self.var_of[g]]
        for node in (t, e):
            if node > TRUE:
                levels.append(self.var_of[node])
        top = min(levels)
        result = self.make(
            top,
            self.ite(self.cofactor(g, top, 0),
                     self.cofactor(t, top, 0),
                     self.cofactor(e, top, 0)),
            self.ite(self.cofactor(g, top, 1),
                     self.cofactor(t, top, 1),
                     self.cofactor(e, top, 1)),
        )
        self._ite_cache[key] = result
        return result

    def cofactor(self, node: int, level: int, value: int) -> int:
        """香农余因子，递归消解顶变量层级更小的节点（带缓存）。"""
        if node <= TRUE or self.var_of[node] > level:
            return node
        if self.var_of[node] < level:
            key = (node, level, value)
            cached = self._restrict_cache.get(key)
            if cached is not None:
                return cached
            result = self.make(
                self.var_of[node],
                self.cofactor(self.low_of[node], level, value),
                self.cofactor(self.high_of[node], level, value),
            )
            self._restrict_cache[key] = result
            return result
        return self.high_of[node] if value else self.low_of[node]

    def restrict(self, node: int, level: int, value: int) -> int:
        """求节点在第 level 个特性固定为 value 时的余因子（递归消解无关变量）。"""
        if node <= TRUE:
            return node
        node_level = self.var_of[node]
        if node_level > level:
            return node
        if node_level < level:
            key = (node, level, value)
            cached = self._restrict_cache.get(key)
            if cached is not None:
                return cached
            result = self.make(
                node_level,
                self.restrict(self.low_of[node], level, value),
                self.restrict(self.high_of[node], level, value),
            )
            self._restrict_cache[key] = result
            return result
        return self.high_of[node] if value else self.low_of[node]

    def neg(self, node: int) -> int:
        return self.ite(node, FALSE, TRUE)

    def and_(self, *nodes: int) -> int:
        result = TRUE
        for node in nodes:
            result = self.ite(result, node, FALSE)
        return result

    def or_(self, *nodes: int) -> int:
        result = FALSE
        for node in nodes:
            result = self.ite(result, TRUE, node)
        return result

    def implies(self, a: int, b: int) -> int:
        return self.ite(a, b, TRUE)

    def one_model(self, node: int) -> dict[str, bool] | None:
        """返回使条件成立的一个特性指派；矛盾条件返回 None。"""
        if node == FALSE:
            return None
        model: dict[str, bool] = {}
        current = node
        while current > TRUE:
            level = self.var_of[current]
            low, high = self.low_of[current], self.high_of[current]
            if low != FALSE:
                model[self.features[level]] = False
                current = low
            else:
                model[self.features[level]] = True
                current = high
        unassigned = set(self.features) - model.keys()
        for name in unassigned:
            model[name] = False
        return model

    def evaluate(self, node: int, assignment: dict[str, bool]) -> bool:
        current = node
        while current > TRUE:
            name = self.features[self.var_of[current]]
            current = self.high_of[current] if assignment.get(name, False) else self.low_of[current]
        return current == TRUE
