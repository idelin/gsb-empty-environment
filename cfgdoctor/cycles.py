"""epsilon 环诊断与语言无限性分析,两者严格分离。

epsilon 环:依赖边 A->B 要求 A 的某条产生式右部中,B 之外的所有符号都可空,
即 A ⇒+ αAβ 且 αβ ⇒* ε,于是 A ⇒+ A。若环上符号可生成,则其见证终结串 w
有无限多推导(无限歧义)——这与"语言是否无限"是两个独立问题:
同一终结串的无限推导不蕴含语言无限。

语言无限性:存在可达且可生成的 A 使 A ⇒+ uAv 且 uv ≠ ε(泵送),
判定依据是依赖环上存在"宽边"(侧余含终结符或不可空非终结符)。
"""
from collections import deque
from dataclasses import dataclass

from .grammar import format_symbol_string


@dataclass(frozen=True)
class CycleEdge:
    target: str
    production_index: int
    target_position: int


def _tarjan(nodes, succ):
    """迭代式 Tarjan SCC;succ: 节点 -> 后继节点可迭代对象。"""
    index_of = {}
    low = {}
    on_stack = set()
    stack = []
    sccs = []
    counter = [0]
    for root in nodes:
        if root in index_of:
            continue
        index_of[root] = low[root] = counter[0]
        counter[0] += 1
        stack.append(root)
        on_stack.add(root)
        work = [(root, iter(succ.get(root, ())))]
        while work:
            node, it = work[-1]
            descended = False
            for w in it:
                if w not in index_of:
                    index_of[w] = low[w] = counter[0]
                    counter[0] += 1
                    stack.append(w)
                    on_stack.add(w)
                    work.append((w, iter(succ.get(w, ()))))
                    descended = True
                    break
                if w in on_stack and index_of[w] < low[node]:
                    low[node] = index_of[w]
            if descended:
                continue
            work.pop()
            if work:
                parent = work[-1][0]
                if low[node] < low[parent]:
                    low[parent] = low[node]
            if low[node] == index_of[node]:
                scc = []
                while True:
                    w = stack.pop()
                    on_stack.discard(w)
                    scc.append(w)
                    if w == node:
                        break
                sccs.append(scc)
    return sccs


@dataclass
class EpsilonCycle:
    members: frozenset
    kind: str                # 'dead' | 'nullable-exit' | 'contextual'
    generating: bool
    nullable: bool
    reachable: bool
    path: tuple              # ((from_symbol, production_index), ...) 回到起点
    regeneration_steps: tuple  # (production_index, position): A ⇒+ A,可独立执行
    ambiguous_string: object   # 有无限多推导的终结串(若可生成),否则 None

    def describe(self):
        members = ", ".join(sorted(self.members))
        if self.kind == "dead":
            return (f"死循环 {{{members}}}: 环上符号互相依赖且无任何终结出口, "
                    "不可生成任何终结串")
        if self.kind == "nullable-exit":
            return (f"可空 epsilon 环 {{{members}}}: 环另有终结出口, "
                    "ε 等串有无限多推导(无限歧义); 规则有用, 不可整体删除")
        return (f"epsilon 上下文环 {{{members}}}: 环上符号可生成, "
                "某终结串有无限多推导(无限歧义)")


def _cycle_path(start, members, edges):
    for e in edges.get(start, ()):
        if e.target == start:
            return [(start, e)]
    prev = {}
    dq = deque()
    for e in edges.get(start, ()):
        if e.target in members and e.target not in prev:
            prev[e.target] = (start, e)
            dq.append(e.target)
    while dq:
        x = dq.popleft()
        for e in edges.get(x, ()):
            if e.target not in members:
                continue
            if e.target == start:
                path = [(x, e)]
                cur = x
                while cur != start:
                    frm, ed = prev[cur]
                    path.append((frm, ed))
                    cur = frm
                path.reverse()
                return path
            if e.target not in prev:
                prev[e.target] = (x, e)
                dq.append(e.target)
    return None


def _regeneration_steps(grammar, path, nullable_info):
    """把环路径编译为可独立执行的推导步序列 A ⇒+ A。

    沿路径展开被跟踪符号;路径两侧的可空符号最后用可空见证产生式消去,
    由于可空轮次严格下降,该过程必然终止,最终句型恰为 (A,)。
    """
    form = [path[0][0]]
    steps = []
    tracked = 0
    for _frm, edge in path:
        p = grammar.productions[edge.production_index]
        steps.append((edge.production_index, tracked))
        rhs = list(p.rhs)
        form[tracked:tracked + 1] = rhs
        tracked += edge.target_position
    i = 0
    while i < len(form):
        if i == tracked:
            i += 1
            continue
        s = form[i]
        pi = nullable_info.witness_production[s]
        steps.append((pi, i))
        rhs = list(grammar.productions[pi].rhs)
        form[i:i + 1] = rhs
        if i < tracked:
            tracked += len(rhs) - 1
    return tuple(steps)


def find_epsilon_cycles(grammar, cert, nullable_info, reachable):
    """找出全部 epsilon 环并分类;为每个环生成 A ⇒+ A 的可执行见证。"""
    nts = grammar.nonterminals
    nullable = nullable_info.nullable
    edges = {}
    for i, p in enumerate(grammar.productions):
        rhs = p.rhs
        for pos, s in enumerate(rhs):
            if s not in nts:
                continue
            rest_ok = True
            for k, t in enumerate(rhs):
                if k == pos:
                    continue
                if t not in nts or t not in nullable:
                    rest_ok = False
                    break
            if rest_ok:
                edges.setdefault(p.lhs, []).append(CycleEdge(s, i, pos))
    succ = {a: [e.target for e in es] for a, es in edges.items()}
    cycles = []
    for scc in _tarjan(sorted(nts), succ):
        self_loop = (len(scc) == 1
                     and any(e.target == scc[0] for e in edges.get(scc[0], ())))
        if len(scc) == 1 and not self_loop:
            continue
        members = frozenset(scc)
        start = scc[0]
        path = _cycle_path(start, members, edges)
        steps = _regeneration_steps(grammar, path, nullable_info)
        is_gen = start in cert.generating
        is_null = start in nullable
        if not is_gen:
            kind = "dead"
        elif is_null:
            kind = "nullable-exit"
        else:
            kind = "contextual"
        cycles.append(EpsilonCycle(
            members=members,
            kind=kind,
            generating=is_gen,
            nullable=is_null,
            reachable=start in reachable,
            path=tuple((frm, e.production_index) for frm, e in path),
            regeneration_steps=steps,
            ambiguous_string=(cert.witness_string.get(start)
                              if is_gen else None),
        ))
    return cycles


@dataclass
class Infiniteness:
    language_infinite: bool
    pumping_symbol: object
    witness_production: object
    explanation: str


def analyze_language_infiniteness(grammar, cert, nullable_info, reachable):
    """语言无限性:存在有用符号 A 使 A ⇒+ uAv 且 uv ≠ ε。

    与无限歧义完全分开诊断:epsilon 环只说明某个终结串有无限多推导,
    不蕴含语言无限;这里判定的是语言本身是否无限(泵送结构)。
    """
    useful = cert.generating & reachable
    nullable = nullable_info.nullable
    edges = {}
    wide = {}
    for i, p in enumerate(grammar.productions):
        if p.lhs not in useful:
            continue
        rhs = p.rhs
        for pos, s in enumerate(rhs):
            if s not in grammar.nonterminals or s not in useful:
                continue
            is_wide = False
            for k, t in enumerate(rhs):
                if k == pos:
                    continue
                if t in grammar.terminals or (
                        t in grammar.nonterminals and t not in nullable):
                    is_wide = True
                    break
            edges.setdefault(p.lhs, []).append((s, i, is_wide))
    succ = {a: [t for t, _i, _w in es] for a, es in edges.items()}
    for scc in _tarjan(sorted(useful), succ):
        members = set(scc)
        cyclic = (len(scc) > 1 or any(
            t == scc[0] for t, _i, _w in edges.get(scc[0], ())))
        if not cyclic:
            continue
        found = None
        for m in sorted(members):
            for t, i, w in edges.get(m, ()):
                if t in members and w:
                    found = (m, i)
                    break
            if found:
                break
        if found:
            m, i = found
            return Infiniteness(
                True, m, i,
                f"符号 {m} 位于含宽边的依赖环上(见证产生式 #{i}: "
                f"{grammar.productions[i]}): {m} ⇒+ u{m}v 且 uv ≠ ε, "
                f"且 {m} 可达且可生成, 故 L(G) 无限")
    return Infiniteness(False, None, None,
                        "未发现泵送结构: 语言有限")
