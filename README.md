# 固件区域别名校验

固件镜像中的多个区域描述可能别名同一物理范围，掩盖对引导区的写入。写供Go更新宿主调用的区间编译包，地址由基址、偏移和缩放组成，全部使用精确整数后检查范围；把别名区间规范化并计算实际写集，与保护区求交给出原描述链。用缩小地址域穷举验证回绕与非一一映射。

## 设计

包 `netsec`（模块 `example.org/netsec/q0327`）供更新宿主在落盘前调用。

- 精确整数：起始地址 `Base + Offset*Scale` 与结束地址全部用 `math/big`
  计算，之后才做范围检查；用户可控地址不经过任何机器字宽运算，溢出不会
  静默回绕到引导区小地址。
- 两种回绕策略：
  - `Strict`（默认）：负起始或越界镜像的区间直接返回 `CompileError`。
  - `ModularDecode`：模拟硬件先按 `2^bits` 截断、再按镜像大小 `D` 取模
    （要求 `D` 为整除 `2^bits` 的 2 的幂）。回绕不报错，而是在
    `DescriptorResult` 上分层标记 `Underflow`、`RepresentationWrap`、
    `DomainWrap`、`NonInjective`（长度超过 `D` 时两个逻辑偏移命中同一
    物理字节，即非一一映射）。
- 规范化：所有物理区间经扫描线合并为有序、两两不交的写集；每个
  `Segment` 携带覆盖该字节范围的全部原始描述符名（去重排序的原描述链）。
- 保护区求交：`Violation` 给出与保护区重叠的物理字节及完整原描述链，
  别名描述符即使表面上不碰引导区也会出现在链中。

## 文件

- `compile.go`：类型、编译器构造、单描述符精确编译与回绕分层。
- `normalize.go`：扫描线别名合并、写集规范化、保护区求交与合并。
- `doc.go`：包级文档。
- `compile_test.go`：64 位精确整数、负偏移下溢、别名掩盖引导区等定向测试。
- `exhaustive_test.go` / `exhaustive_cases_test.go` / `pair_test.go`：
  缩小地址域穷举，独立的逐字节 oracle 直接模拟两层取模。
- `example_test.go`：更新宿主典型用法的可运行示例。

## 穷举验证

白盒选项 `withRepresentationBits` 把表示宽度从 64 位缩小到 2/3/4 位：

- W=2：枚举全部 640 个描述符元组 × 模式 × 镜像大小；枚举全部描述符对
  （含自身别名）验证别名链。
- W=3：枚举全部 9216 个描述符元组。
- W=4：固定随机种子采样单描述符与描述符对，覆盖全部回绕组合。

每个用例都与逐字节 oracle 比对规范化写集、每字节原描述链、各层回绕
标记以及保护区违例；严格模式下另比对拒绝/接受判定。

## 使用

```go
compiler, err := netsec.New(imageSize, netsec.WithModularDecode())
report, err := compiler.Compile(descriptors, protectedRegions)
for _, v := range report.Violations {
    // v.Descriptors 是导致该引导区写入的完整原描述链
}
```

运行测试：`go test ./...`（约 6 秒；`-race` 约 30 秒）。
