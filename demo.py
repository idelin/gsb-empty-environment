from fractions import Fraction

from units import (
    AffineUnitError,
    ConversionGraph,
    DimensionVector,
    InconsistentUnitConstraints,
    UnitConstraintSystem,
    UnitRegistry,
)


def main() -> None:
    registry = UnitRegistry()
    for dimension in ("L", "T", "Theta"):
        registry.define_base_dimension(dimension)
    registry.define_unit("m", {"L": 1})
    registry.define_unit("s", {"T": 1})
    registry.define_unit("K", {"Theta": 1})
    registry.define_unit("delta_degC", {"Theta": 1})
    registry.define_unit(
        "degC",
        {"Theta": 1},
        affine=True,
        delta_unit="delta_degC",
    )

    speed = registry.unit("m") / registry.unit("s")
    print("速度 × 时间 =", (speed * registry.unit("s")).dimension)

    try:
        _ = registry.unit("degC") * registry.unit("s")
    except AffineUnitError as error:
        print("绝对温度乘法被拒绝:", error)
    print("温差 / 时间 =", (registry.unit("delta_degC") / registry.unit("s")).dimension)

    constraints = UnitConstraintSystem(["L", "T"], ["x"])
    unknown_x = DimensionVector.symbol("?x")
    constraints.equate(
        unknown_x * DimensionVector.symbol("T"),
        DimensionVector.symbol("L"),
        label="x * s = m",
    )
    solved = constraints.solve()
    print("未知单位 x 的量纲:", solved.dimension_of("x").describe())
    print("原始约束代入残差:", solved.verification.particular_residuals)

    free_constraints = UnitConstraintSystem(["L"], ["u", "v"])
    free_constraints.equate(
        DimensionVector.symbol("?u") / DimensionVector.symbol("?v"),
        DimensionVector.symbol("L"),
        label="u / v = m",
    )
    free_solution = free_constraints.solve()
    print("自由维度参数:", free_solution.free_variables)
    print("u 的自由量纲:", free_solution.dimension_of("u").describe())

    bad_constraints = UnitConstraintSystem(["L"], ["y"])
    bad_constraints.equate(
        DimensionVector.symbol("?y", 2),
        DimensionVector.symbol("L"),
        label="2*y_L = 1",
    )
    try:
        bad_constraints.solve()
    except InconsistentUnitConstraints as error:
        print("整数不相容证据:", error)

    registry.define_conversion("delta_degC", "K", 1)
    registry.define_conversion(
        "degC",
        "K",
        1,
        "5463/20",
        domain="摄氏绝对温度；可加减和求差，不参与乘除",
    )
    converted = registry.convert(Fraction(20), "degC", "K")
    print("20 degC =", converted.value, "K")
    print("转换步骤:\n" + converted.explanation)

    old_version = registry.version
    old_snapshot = registry.snapshot(old_version)
    registry.define_unit("km", {"L": 1})
    registry.define_conversion("km", "m", 1000, domain="长度比例单位；所有有理数")
    print("图版本:", old_version, "->", registry.version)
    print("旧版本包含 km:", "km" in old_snapshot.nodes)
    registry.remove_unit("km")
    print("删除 km 后版本:", registry.version)

    cycle_graph = ConversionGraph()
    for name in ("A", "B", "C"):
        cycle_graph.add_node(name)
    cycle_graph.add_edge("A", "B", 2)
    cycle_graph.add_edge("B", "C", 3)
    cycle_graph.add_edge("A", "C", 7)
    validation = cycle_graph.validate()
    for contradiction in validation.contradictions:
        print(contradiction.describe())


if __name__ == "__main__":
    main()
