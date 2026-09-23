"""unitchk 运行样例：python3 demo.py"""
from fractions import Fraction

from unitchk import (Registry, UnitKind, UnitTypeError, StepLog, QType,
                     Leaf, Mul, Pow, Equation,
                     build_system, check_equation, eval_known,
                     solve_integer_system, Solution, Infeasible,
                     solution_unit_vectors, ConversionGraph, AffineMap,
                     Contradiction, conversion_between, verify)
from unitchk.core import sub as core_sub, mul as core_mul


def section(title):
    print()
    print("=" * 64)
    print(title)
    print("=" * 64)


def show_steps(steps, limit=8):
    for st in list(steps)[:limit]:
        print(f"    步骤: {st}")
    if len(steps) > limit:
        print(f"    ... 共 {len(steps)} 步")


def make_registry():
    reg = Registry(["L", "M", "T", "Θ"])
    reg.define("m", {"L": 1})
    reg.define("km", {"L": 1}, scale=1000)
    reg.define("mile", {"L": 1}, scale=Fraction("1609.344"))
    reg.define("s", {"T": 1})
    reg.define("kg", {"M": 1})
    reg.define("mps", {"L": 1, "T": -1})
    reg.define("mpss", {"L": 1, "T": -2})
    reg.define("K", {"Θ": 1})
    reg.define("degC", {"Θ": 1}, kind=UnitKind.ABSOLUTE,
               offset=Fraction("273.15"))
    reg.define("degF", {"Θ": 1}, kind=UnitKind.ABSOLUTE,
               scale=Fraction(5, 9),
               offset=Fraction("273.15") - 32 * Fraction(5, 9))
    reg.define("dC", {"Θ": 1})
    return reg


def demo_speed_time(reg):
    section("1. 速度 × 时间 = 长度（已知单位联合类型检查）")
    log = StepLog()
    eq = Equation(Mul(Leaf("mps"), Leaf("s")), Leaf("m"), "v*t=s")
    t = check_equation(eq, reg, log)
    print(f"  mps * s 的类型 = {reg.fmt_dim(t.dim)}，与 m 一致 ✓")
    show_steps(log.steps)


def demo_affine(reg):
    section("2. 绝对仿射温度：乘法默认非法，差量可参与")
    log = StepLog()
    try:
        eval_known(Mul(Leaf("degC"), Leaf("s")), reg, log)
    except UnitTypeError as e:
        print(f"  degC * s      -> 拒绝: {e}")
    log2 = StepLog()
    # 差量：degC - degC 是比例量，可参与乘法
    abs_t = QType(reg.units["degC"].dim, True)
    delta_t = core_sub(abs_t, abs_t, log2)
    print(f"  degC - degC   -> 差量（比例量）absolute={delta_t.absolute}")
    prod = core_mul(delta_t, QType(reg.units["s"].dim), log2)
    print(f"  (degC-degC)*s -> {reg.fmt_dim(prod.dim)} ✓（差量可参与乘法）")


def demo_unknown_solve(reg):
    section("3. 未知单位约束求解：F = kg · m/s²")
    eqs = [Equation(Leaf("F"), Mul(Leaf("kg"), Leaf("mpss")), "F=ma")]
    system = build_system(eqs, reg, {"F"})
    sol = solve_integer_system(system.A, system.b,
                               system.var_names, system.row_labels)
    assert isinstance(sol, Solution)
    base, frees = solution_unit_vectors(system, sol)["F"]
    print(f"  解得 F 的指数向量 = {base} 即 {reg.fmt_dim(base)}（唯一解）")
    keyed = {v: sol.particular[k] for k, v in enumerate(system.variables)}
    errors = verify.verify_substitution(system, keyed)
    print(f"  独立代入全部原始约束: {'全部成立 ✓' if not errors else errors}")


def demo_lattice(reg):
    section("4. 欠定方程：X · Y = m → 整数解格与自由维度")
    eqs = [Equation(Mul(Leaf("X"), Leaf("Y")), Leaf("m"), "X*Y=m")]
    system = build_system(eqs, reg, {"X", "Y"})
    sol = solve_integer_system(system.A, system.b,
                               system.var_names, system.row_labels)
    print(f"  格维数 = {len(sol.lattice)}，自由变量 = {sol.free_variables}")
    vectors = solution_unit_vectors(system, sol)
    for name, (base, frees) in vectors.items():
        print(f"  {name}: 特解 {base}（{reg.fmt_dim(base)}），"
              f"自由方向 {len(frees)} 个")
    assign = sol.assignment(params=[1, -2, 3, 0])
    keyed = {v: assign[system.var_names[k]]
             for k, v in enumerate(system.variables)}
    print(f"  取格参数 [1,-2,3,0] 独立代入: "
          f"{'成立 ✓' if not verify.verify_substitution(system, keyed) else '失败'}")
    print(f"  核验证: {'通过 ✓' if not verify.verify_kernel(system, sol) else '失败'}")


def demo_contradiction(reg):
    section("5. 矛盾方程组：X·s = m 与 X·s = kg → 不相容证书")
    eqs = [Equation(Mul(Leaf("X"), Leaf("s")), Leaf("m"), "eq1"),
           Equation(Mul(Leaf("X"), Leaf("s")), Leaf("kg"), "eq2")]
    system = build_system(eqs, reg, {"X"})
    res = solve_integer_system(system.A, system.b,
                               system.var_names, system.row_labels)
    assert isinstance(res, Infeasible)
    print(f"  类型: {res.kind}")
    print(f"  {res.evidence['detail']}")
    ok = verify.check_infeasibility(system.A, system.b, res.evidence)
    print(f"  独立验证证书 c·A=0 且 c·b≠0: {'成立 ✓' if ok else '不成立'}")


def demo_divisibility(reg):
    section("6. 整数整除性：X² = m → 2·X[L] = 1 在 Z 上无解")
    eqs = [Equation(Pow(Leaf("X"), 2), Leaf("m"), "X^2=m")]
    system = build_system(eqs, reg, {"X"})
    res = solve_integer_system(system.A, system.b,
                               system.var_names, system.row_labels)
    assert isinstance(res, Infeasible)
    print(f"  类型: {res.kind}；{res.evidence['detail']}")
    ok = verify.check_infeasibility(system.A, system.b, res.evidence)
    print(f"  独立复现整除失败（含 det(U)=±1、A·U=H 核对）: "
          f"{'成立 ✓' if ok else '不成立'}")


def demo_graph(reg):
    section("7. 版本化转换图：精确换算、矛盾环、历史快照")
    g = ConversionGraph(reg)
    for name in ("m", "km", "mile"):
        g.add_unit(name)
    g.add_conversion("km", "m", conversion_between(
        reg.units["km"], reg.units["m"]), "1 km = 1000 m")
    g.add_conversion("mile", "m", conversion_between(
        reg.units["mile"], reg.units["m"]), "1 mile = 1609.344 m")
    _, amap = g.find("mile", "km")
    print(f"  mile -> km: 因子 {amap.a}（精确有理数，类型 {type(amap.a).__name__}）")
    v_before = g.version
    res = g.add_conversion("mile", "km", AffineMap(Fraction("1.6")), "错误声明")
    assert isinstance(res, Contradiction)
    print(f"  声明 1 mile = 1.6 km -> 拒绝：{res.detail}")
    print(f"  矛盾环复合映射 = {res.composed.a} ≠ 1，"
          f"独立重算一致: {verify.verify_cycle(res.cycle) == res.composed}")
    g.remove_conversion("mile", "m")
    print(f"  删除 mile->m 后: 当前版本 find(mile,km) = {g.find('mile', 'km')}")
    old = g.find("mile", "km", version=v_before)
    print(f"  历史版本 v{v_before} 快照 find(mile,km) 因子 = {old[1].a} ✓")
    print(f"  版本日志: {g.journal}")


def demo_affine_graph(reg):
    section("8. 仿射温度换算（比例与偏移分开）")
    g = ConversionGraph(reg)
    for name in ("degC", "degF"):
        g.add_unit(name)
    g.add_conversion("degF", "degC", conversion_between(
        reg.units["degF"], reg.units["degC"]))
    _, amap = g.find("degF", "degC")
    print(f"  degF -> degC: x -> ({amap.a})*x + ({amap.b})")
    print(f"  32°F = {amap(32)}°C，212°F = {amap(212)}°C（精确）")
    g2 = ConversionGraph(reg)
    g2.add_unit("degC")
    g2.add_unit("dC")
    try:
        g2.add_conversion("degC", "dC", AffineMap(1))
    except ValueError as e:
        print(f"  绝对单位 ↔ 差量单位建边 -> 拒绝: {e}")


def demo_free_dimension():
    section("9. 未知单位的自由维度")
    reg = make_registry()
    x = reg.unknown("X")
    print(f"  注册未知单位 X -> 分配自由维度 {reg.dimensions[-1]!r}，"
          f"指数向量 {x.dim}")
    log = StepLog()
    x2 = core_mul(QType(x.dim), QType(x.dim), log)
    print(f"  X·X 的指数向量 = {x2.dim}（自由维度上指数为 2）")


def demo_exact_rational():
    section("10. 有理数精确计算（无浮点近似）")
    third = Fraction(1, 3)
    print(f"  1/3 * 3 = {third * 3}（严格等于 1，Fraction 精确表示）")
    m = AffineMap(Fraction(1, 3))
    print(f"  仿射映射 (1/3)x 与其逆复合: a={m.then(m.inverse()).a}，"
          f"b={m.then(m.inverse()).b} -> 严格恒等 ✓")


def main():
    reg = make_registry()
    demo_speed_time(reg)
    demo_affine(reg)
    demo_unknown_solve(reg)
    demo_lattice(reg)
    demo_contradiction(reg)
    demo_divisibility(reg)
    demo_graph(reg)
    demo_affine_graph(reg)
    demo_free_dimension()
    demo_exact_rational()
    print()
    print("全部样例运行完毕。")


if __name__ == "__main__":
    main()
