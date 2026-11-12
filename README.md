# 体绘制在步长变化后变得忽明忽暗

体绘制在步长变化后变得忽明忽暗。实现供Go医学体数据渲染宿主调用的射线积分包，体素给吸收系数和线性RGB发射率，单元内按三线性插值；每段用常系数解析透射公式合成，采样步长自适应细分，以整段与两半段结果差控制误差。射线穿越非均匀网格时精确定位单元边界，零吸收取极限值。用均匀介质解析解、不同步长和空体素验证颜色及透射率，达到步数预算明确返回剩余误差估计。

## 实现

- `Grid`：结构化、各轴可非均匀的节点网格；体素数据存于节点，单元内三线性插值，扁平索引为 `(k*ny+j)*nx+i`。
- `analyticSegment`：常系数解析解 `T=exp(-sigma*h)`、`C=em*(1-T)/sigma`；零吸收走精确极限 `T=1, C=em*h`，短光程用 `Expm1` 避免精度损失。
- `Integrator.Integrate`：slab 法求包围盒交点，DDA 按节点坐标精确定位每个单元边界；单元内用“整段 vs 两半段”差值驱动自适应细分，前向合成，结果只随容差收敛而不随采样步长跳动。
- 方向向量无需归一化，内部按物理弧长 `|dir|*h` 计算，结果与方向模长无关。
- `Options.MaxSteps` 限定三线性采样次数；预算耗尽时 `Result.Exhausted=true`，并在 `ColorErr`/`TransErr`/`Err` 返回保守剩余误差估计。

## 宿主调用示例

```go
grid, err := taskpkg.NewGrid(xs, ys, zs, sigma, em)
if err != nil { /* 非法坐标或系数 */ }

integrator, err := taskpkg.NewIntegrator(grid, taskpkg.Options{
    MaxSteps:  4096,
    ColorTol:  1e-4,
    TransTol:  1e-5,
    Background: taskpkg.RGB{0, 0, 0},
})
if err != nil { /* 非法参数 */ }

res := integrator.Integrate(rayOrigin, rayDir)
if !res.Hit { /* 射线未命中体数据 */ }
if res.Exhausted {
    // res.Err 是剩余误差上界，宿主可据此提高预算或降级显示
}
_ = res.Color // 线性 RGB（已含背景衰减项）
_ = res.Trans // [0,1] 透射率
```

## 验证

`go test ./...` 覆盖：常系数段解析解与零吸收极限、均匀介质端到端解析解对比、非均匀网格与斜射边界穿越、不同容差/细分深度的步长不变性、方向模长不变性、空体素与真空前缀、预算耗尽时的剩余误差包络、背景与未命中射线。
