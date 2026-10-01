"""独立验证器:只依赖 Grammar 数据结构,不复用分析器内部实现。

所有见证/反例(证书、终结串推导、循环再生推导)都可在此独立执行,
满足"反例可独立执行"的要求。
"""


class DerivationError(Exception):
    pass


def run_derivation(grammar, start_symbol, steps):
    """逐步执行推导。steps: [(产生式编号, 展开位置), ...],返回最终句型。"""
    form = [start_symbol]
    for n, (index, pos) in enumerate(steps):
        if not 0 <= index < len(grammar.productions):
            raise DerivationError(f"第 {n} 步: 产生式编号 {index} 越界")
        p = grammar.productions[index]
        if not 0 <= pos < len(form):
            raise DerivationError(f"第 {n} 步: 位置 {pos} 越界")
        if form[pos] != p.lhs:
            raise DerivationError(
                f"第 {n} 步: 位置 {pos} 上是 {form[pos]!r}, "
                f"与产生式左部 {p.lhs!r} 不符")
        form[pos:pos + 1] = list(p.rhs)
    return tuple(form)


def independent_reachable(grammar):
    """可达性的独立实现(反向 DFS),用于交叉核验主分析结果。"""
    seen = {grammar.start}
    stack = [grammar.start]
    while stack:
        a = stack.pop()
        for i in reversed(grammar.production_indices_of(a)):
            for s in reversed(grammar.productions[i].rhs):
                if s in grammar.nonterminals and s not in seen:
                    seen.add(s)
                    stack.append(s)
    return frozenset(seen)


def verify_generating_certificate(grammar, cert):
    """独立复核可生成性最小不动点证书,返回错误列表(空 = 通过)。

    两组条件合起来恰好刻画最小不动点:
      声音性: 每个生成符号的见证产生式右部符号都可生成且轮次严格下降
              (秩函数保证见证推导树有限);
      完备性: 每个非生成符号的所有产生式右部都含非生成符号
              (补集封闭 => 对推导长度归纳,它们导不出终结串)。
    """
    errors = []
    nts = grammar.nonterminals
    gen = set(cert.generating)
    non = set(cert.non_generating)
    if gen & non:
        errors.append("生成集与非生成集相交")
    if gen | non != set(nts):
        errors.append("生成集与非生成集未划分全部非终结符")
    for a in sorted(gen):
        if (a not in cert.rounds or a not in cert.witness_production
                or a not in cert.witness_string):
            errors.append(f"{a}: 证书字段缺失")
            continue
        r = cert.rounds[a]
        pi = cert.witness_production[a]
        if not 0 <= pi < len(grammar.productions):
            errors.append(f"{a}: 见证产生式编号越界")
            continue
        p = grammar.productions[pi]
        if p.lhs != a:
            errors.append(f"{a}: 见证产生式左部不符")
            continue
        child_rounds = []
        expected = []
        for s in p.rhs:
            if s in nts:
                if s not in gen:
                    errors.append(f"{a}: 见证右部符号 {s} 不在生成集")
                    continue
                if cert.rounds.get(s, 10 ** 9) >= r:
                    errors.append(
                        f"{a}: 依赖 {s} 的轮次未严格下降(秩不合法)")
                else:
                    child_rounds.append(cert.rounds[s])
                    expected.extend(cert.witness_string[s])
            else:
                if s not in grammar.terminals:
                    errors.append(f"{a}: 见证右部符号 {s} 不在字母表")
                expected.append(s)
        if r != 1 + max(child_rounds, default=0):
            errors.append(f"{a}: 轮次与依赖不一致")
        if tuple(expected) != tuple(cert.witness_string[a]):
            errors.append(f"{a}: 见证终结串与见证产生式不一致")
        for t in cert.witness_string[a]:
            if t not in grammar.terminals:
                errors.append(f"{a}: 见证串含非终结符 {t!r}")
    for a in sorted(non):
        for p in grammar.productions_of(a):
            if all(s in grammar.terminals or s in gen for s in p.rhs):
                errors.append(
                    f"{a}: 存在右部全可生成的产生式 {p}, "
                    "补集不封闭, 证书不是不动点")
    return errors


def certificate_to_derivation(grammar, cert, symbol):
    """把证书编译为最左推导步序列(轮次严格下降保证终止)。"""
    steps = []
    form = [symbol]
    while True:
        pos = None
        for k, s in enumerate(form):
            if s in grammar.nonterminals:
                pos = k
                break
        if pos is None:
            return steps
        pi = cert.witness_production[form[pos]]
        steps.append((pi, pos))
        form[pos:pos + 1] = list(grammar.productions[pi].rhs)


def execute_certificate_witness(grammar, cert, symbol):
    """独立执行证书见证:推导出终结串并与证书断言比对。"""
    steps = certificate_to_derivation(grammar, cert, symbol)
    final = run_derivation(grammar, symbol, steps)
    if final != tuple(cert.witness_string[symbol]):
        raise DerivationError(
            f"见证执行结果 {final} 与证书见证串 "
            f"{cert.witness_string[symbol]} 不符")
    return final


def verify_cycle_witness(grammar, symbol, steps):
    """独立执行循环见证:非空步序列且 A ⇒+ A。"""
    if not steps:
        return False
    return run_derivation(grammar, symbol, steps) == (symbol,)
