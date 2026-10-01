"""基数约束的 CNF 编码。

实现两种编码：
  * "seq" —— Sinz 的顺序计数器（sequential counter）
  * "net" —— 基于 Batcher 奇偶归并的基数网络（cardinality network，
            只生成前 k+1 个一元输出的简化归并）

支持的语义：at_most（至多 k）、at_least（至少 k）、exactly（恰好 k）。
边界情形直接化简：k<0、k>n、k=0、k=n、n=0 均按语义处理，不产生多余辅助变量。

增量与撤回：每个约束可携带一个激活文字 a（所有子句附加 -a）。求解时在
假设（assumptions）中令 a 为真即启用该约束；令 a 为假（或不再假设）即撤回。
收紧界有两种方式：
  1. 通用方式：用新激活变量重新编码更紧的界，假设层面切换（可撤回）；
  2. 网络编码特有：一元输出 outputs[j] 为真当且仅当至少 j+1 个输入为真，
     收紧到 k' 只需添加单位子句 (-outputs[k'])（单调，不可单独撤回）。
"""

from cnf import VarManager, normalize_clause

AT_MOST = "at_most"
AT_LEAST = "at_least"
EXACTLY = "exactly"
SENSES = (AT_MOST, AT_LEAST, EXACTLY)
METHODS = ("seq", "net")


class EncodedConstraint:
    """一次编码的结果：子句、辅助变量、用于构造性扩展的定义等。"""

    def __init__(self, sense, lits, k, method, activation=None):
        self.sense = sense
        self.lits = tuple(lits)
        self.k = k
        self.method = method
        self.activation = activation
        self.clauses = []
        self.aux_vars = []
        self.outputs = []       # 网络编码的一元输出：outputs[j] 为真 <=> 至少 j+1 个输入为真
        self.definitions = []   # 由原赋值确定辅助赋值的定义（按拓扑序）
        self.constant = None    # 边界化简为常量时为 True / False

    def add_clause(self, lits):
        clause = list(lits)
        if self.activation is not None:
            clause.append(-self.activation)
        norm = normalize_clause(clause)
        if norm is not None:
            self.clauses.append(norm)

    @property
    def num_clauses(self):
        return len(self.clauses)

    @property
    def num_aux(self):
        return len(self.aux_vars)


class Encoder:
    """在共享的 VarManager 上分配辅助变量，保证编号与命名不冲突。"""

    def __init__(self, vm=None):
        self.vm = vm or VarManager()

    # ------------------------------------------------------------------
    # 对外入口
    # ------------------------------------------------------------------
    def encode(self, sense, lits, k, method="seq", activation=None):
        lits = list(lits)
        if sense == AT_MOST:
            return self.at_most(lits, k, method, activation)
        if sense == AT_LEAST:
            return self.at_least(lits, k, method, activation)
        if sense == EXACTLY:
            return self.exactly(lits, k, method, activation)
        raise ValueError(f"未知约束类型: {sense}")

    def at_most(self, lits, k, method="seq", activation=None):
        enc = EncodedConstraint(AT_MOST, lits, k, method, activation)
        n = len(lits)
        if k < 0:                       # 至多负数：不可满足
            enc.constant = False
            enc.add_clause(())
            return enc
        if k >= n:                      # 至多 >= n：恒真
            enc.constant = True
            return enc
        if k == 0:                      # 至多 0：全部取假的单位子句
            for lit in lits:
                enc.add_clause((-lit,))
            return enc
        if method == "seq":
            self._seq_at_most(enc, lits, k)
        elif method == "net":
            self._net_at_most(enc, lits, k)
        else:
            raise ValueError(f"未知编码方法: {method}")
        return enc

    def at_least(self, lits, k, method="seq", activation=None):
        enc = EncodedConstraint(AT_LEAST, lits, k, method, activation)
        n = len(lits)
        if k <= 0:                      # 至少非正数：恒真
            enc.constant = True
            return enc
        if k > n:                       # 至少超过 n：不可满足
            enc.constant = False
            enc.add_clause(())
            return enc
        if k == n:                      # 至少 n：全部取真的单位子句
            for lit in lits:
                enc.add_clause((lit,))
            return enc
        # 至少 k 个为真 <=> 至多 n-k 个为假
        sub = self.at_most([-lit for lit in lits], n - k, method, activation)
        self._absorb(enc, sub)
        return enc

    def exactly(self, lits, k, method="seq", activation=None):
        enc = EncodedConstraint(EXACTLY, lits, k, method, activation)
        n = len(lits)
        if k < 0 or k > n:              # 恰好 k 越界：不可满足（n=0,k=0 不在此列）
            enc.constant = False
            enc.add_clause(())
            return enc
        lo = self.at_most(lits, k, method, activation)
        hi = self.at_least(lits, k, method, activation)
        self._absorb(enc, lo)
        self._absorb(enc, hi)
        if lo.constant is not None and hi.constant is not None:
            enc.constant = lo.constant and hi.constant
        return enc

    @staticmethod
    def _absorb(enc, sub):
        enc.clauses.extend(sub.clauses)
        enc.aux_vars.extend(sub.aux_vars)
        enc.outputs.extend(sub.outputs)
        enc.definitions.extend(sub.definitions)

    # ------------------------------------------------------------------
    # 顺序计数器（Sinz）
    # ------------------------------------------------------------------
    def _seq_at_most(self, enc, lits, k):
        n = len(lits)
        regs = {}

        def reg(i, j):
            """s[i][j]：前 i 个输入中至少 j 个为真（1 <= i <= n-1, 1 <= j <= k）。"""
            if (i, j) not in regs:
                var = self.vm.new_var(f"s_{i}_{j}")
                regs[(i, j)] = var
                enc.aux_vars.append(var)
                enc.definitions.append((var, "count_ge", tuple(lits[:i]), j))
            return regs[(i, j)]

        enc.add_clause((-lits[0], reg(1, 1)))
        for j in range(2, k + 1):
            enc.add_clause((-reg(1, j),))
        for i in range(2, n):
            enc.add_clause((-lits[i - 1], reg(i, 1)))
            enc.add_clause((-reg(i - 1, 1), reg(i, 1)))
            for j in range(2, k + 1):
                enc.add_clause((-lits[i - 1], -reg(i - 1, j - 1), reg(i, j)))
                enc.add_clause((-reg(i - 1, j), reg(i, j)))
        for i in range(1, n):
            enc.add_clause((-lits[i], -reg(i, k)))

    # ------------------------------------------------------------------
    # 基数网络（Batcher 奇偶归并，简化归只保留前 p 个输出）
    # ------------------------------------------------------------------
    def _or(self, a, b, enc):
        if a is None:
            return b
        if b is None:
            return a
        var = self.vm.new_var(f"or_{self.vm.num_vars}")
        enc.aux_vars.append(var)
        enc.add_clause((-a, var))
        enc.add_clause((-b, var))
        enc.add_clause((a, b, -var))
        enc.definitions.append((var, "or", a, b))
        return var

    def _and(self, a, b, enc):
        if a is None or b is None:
            return None
        var = self.vm.new_var(f"and_{self.vm.num_vars}")
        enc.aux_vars.append(var)
        enc.add_clause((a, -var))
        enc.add_clause((b, -var))
        enc.add_clause((-a, -b, var))
        enc.definitions.append((var, "and", a, b))
        return var

    def _net_at_most(self, enc, lits, k):
        n = len(lits)
        size = 1
        while size < n:
            size *= 2
        padded = list(lits) + [None] * (size - n)   # None 表示常量 False
        enc.outputs = self._sort_rec(padded, k + 1, enc)
        top = enc.outputs[k]
        if top is None:                 # 结构上不可能（k+1 <= n），防御性处理
            enc.constant = True
            enc.clauses.clear()
            return
        enc.add_clause((-top,))

    def _sort_rec(self, items, p, enc):
        """返回 items 降序排序后的前 min(p, len) 个输出（文字或 None）。"""
        length = len(items)
        if length == 1:
            return items
        half = length // 2
        a = self._sort_rec(items[:half], min(p, half), enc)
        b = self._sort_rec(items[half:], min(p, half), enc)
        return self._merge(a, b, min(p, length), enc)

    def _merge(self, a, b, p, enc):
        """归并两个等长降序序列，只生成前 p 个输出（简化归并）。"""
        m = len(a)
        if p == 0 or m == 0:
            return []
        if m == 1:
            hi = self._or(a[0], b[0], enc)
            if p == 1:
                return [hi]
            lo = self._and(a[0], b[0], enc)
            return [hi, lo]
        pd = min((p + 2) // 2, m)
        pe = min(p // 2, m)
        d = self._merge(a[0::2], b[0::2], pd, enc)
        e = self._merge(a[1::2], b[1::2], pe, enc)
        out = [d[0]]
        i = 1
        while len(out) < p and i < m:
            out.append(self._or(d[i], e[i - 1], enc))    # c_{2i} = max(d_{i+1}, e_i)
            if len(out) < p:
                out.append(self._and(d[i], e[i - 1], enc))  # c_{2i+1} = min(d_{i+1}, e_i)
            i += 1
        if len(out) < p:  # p == 2m：最后一个输出 c_{2m} = e_m，无需比较器
            out.append(e[m - 1])
        return out


def tighten_network(enc, k_new):
    """网络编码的原生收紧：添加单位子句 -outputs[k_new]，把界从 k 收紧到 k_new。

    仅适用于 method="net" 的 at_most 约束；单调（不可单独撤回，
    撤回请使用激活文字 + 假设机制）。返回新增的子句。
    """
    if enc.method != "net" or enc.sense != AT_MOST or not enc.outputs:
        raise ValueError("仅网络编码的 at_most 约束支持输出单元收紧")
    if not 0 <= k_new < enc.k:
        raise ValueError(f"收紧目标必须满足 0 <= k_new < {enc.k}，得到 {k_new}")
    clause = [-enc.outputs[k_new]]
    if enc.activation is not None:
        clause.append(-enc.activation)
    norm = normalize_clause(clause)
    if norm is not None:
        enc.clauses.append(norm)
    enc.k = k_new
    return norm
