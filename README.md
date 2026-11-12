# SBOM可达依赖许可门禁

供应商可把运行时依赖在CycloneDX清单里标成excluded，躲过许可门禁。为Go发布门禁实现可导入的范围推导包：沿dependencies边从根求可达集合，忽略自报scope；bom-ref重复或同purl不同版本冲突即拒；SPDX的AND、OR、WITH按语义求值，OR有一支被允许即可。用重复ref、隐藏GPL依赖和OR分支样例测试。

## 包结构

- 根包 `netsec`（`example.org/netsec/q0318`）：CycloneDX 解析、可达范围推导、许可门禁。
- 子包 `spdx`（`example.org/netsec/q0318/spdx`）：独立可导入的 SPDX 表达式解析与求值。

## 门禁规则

- **忽略自报 scope**：`scope=excluded/optional/required` 不影响结论。范围由
  `dependencies` 图从根（`metadata.component`，缺失时取无父边的孤立 ref）BFS 推导，
  图上环可安全终止；根组件本身不计入第三方依赖。
- **结构性冲突即拒（fail closed）**：重复 `bom-ref`、同 purl 坐标不同版本、
  依赖边指向不存在的 ref，直接返回错误而不是静默跳过。
- **SPDX 语义**：`AND` 要求两支都允许，`OR` 任一支允许即通过，`WITH` 可用
  `id WITH exception` 精确授权或由基础 id 授权覆盖；支持括号与优先级。
- **无 license 声明**：记为违规，不默认放行。

## 用法

```go
import (
    "os"

    "example.org/netsec/q0318"
)

data, _ := os.ReadFile("release.cdx.json")
bom, err := netsec.ParseBOM(data)
if err != nil { /* 信封不合法 */ }

policy := netsec.NewPolicy("MIT", "Apache-2.0", "BSD-3-Clause",
    "GPL-2.0-only WITH Classpath-exception-2.0")
report, err := policy.Evaluate(bom)
if err != nil {
    // 结构性问题：重复 bom-ref / purl 版本冲突 / 悬空边
}
if !report.Allowed() {
    for _, v := range report.Violations {
        // v.BOMRef / v.Name / v.Version / v.License / v.Reason
    }
}
```

仅需要范围推导时可直接调用 `bom.ReachableComponents()`；需要单独解析表达式时使用
`spdx.Parse("GPL-3.0-only OR Apache-2.0")`。

## 测试

```sh
go test ./...
```

覆盖三类关键样例：重复 `bom-ref` 拒收、被标成 `excluded` 的传递性 GPL 依赖仍被
门禁拦截、`OR` 表达式一支被允许即通过；另含 purl 版本冲突、悬空边、环图、
`AND`/`WITH`/括号优先级等用例。
