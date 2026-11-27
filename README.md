SQL:2011 窗口帧规范同时支持 `ROWS`、`RANGE`、`GROUPS` 三种帧模式以及 `EXCLUDE CURRENT ROW`、`EXCLUDE GROUP`、`EXCLUDE TIES`、`EXCLUDE NO OTHERS` 四种排除子句，各模式在存在重复排序键（Peers）与 `NULL` 时行为迥异，极易混淆：`RANGE` 模式按排序键的值域偏移界定帧，当当前行的排序键为 `NULL` 时，即使帧界写成 `RANGE BETWEEN 5 PRECEDING AND 5 FOLLOWING`，也**只能**包含同分区的全部 `NULL` 行而绝不可纳入任何非空数值行！`GROUPS` 模式按互异排序键的 peer 组个数前后偏移（同样把全部 `NULL` 视作一个独立 peer 组，遵循 `NULLS FIRST`/`NULLS LAST` 位置！）；`EXCLUDE TIES` 会从帧中剔除与当前行排序键相等的所有其他 peer 但保留当前行本身，而 `EXCLUDE GROUP` 连当前行一并剔除——若整帧被排除为空，则 `COUNT` 须返回 `0` 而 `SUM`/`AVG`/`MIN`/`MAX`/`FIRST_VALUE`/`LAST_VALUE`/`NTH_VALUE` 须返回 `NULL`！为分析型查询执行引擎交付可导入的 Go SQL:2011 窗口帧求值包。支持多列 `PARTITION BY`（`NULL` 归同一分区）、多列 `ORDER BY`（独立 `ASC`/`DESC` 与 `NULLS FIRST`/`NULLS LAST`）、默认帧规则（有 `ORDER BY` 无帧子句时默认为 `RANGE BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW`；无 `ORDER BY` 时为整分区！）、全部帧起止组合（含 `N PRECEDING`/`M PRECEDING` 与 `N FOLLOWING`/`M FOLLOWING` 反转空帧检测、非法负偏移报错）及聚合/取值/排名函数（`SUM`/`COUNT`/`AVG`/`MIN`/`MAX`/`FIRST_VALUE`/`LAST_VALUE`/`NTH_VALUE`/`ROW_NUMBER`/`RANK`/`DENSE_RANK`/`NTILE`/`LAG`/`LEAD`）。验收用含大量重复键、`NULL`、负值、`GROUPS` 跨组偏移与四种 `EXCLUDE` 的随机数据集对照暴力逐行定义参考器，验证每个函数与每行输出逐项完全一致。

## 交付内容

可导入包 `windowframe`（导入路径 `prescreen.local/ps-f6cb3f0fdc/windowframe`）：

- `value.go`：标量值类型 `Value`（NULL / 整数 / 浮点 / 布尔 / 字符串）、构造器与排序比较。
- `spec.go`：`WindowSpec`、`OrderSpec`、`FrameSpec`、`FrameBound`、`FuncExpr` 及静态校验（负偏移、非整数 ROWS/GROUPS 偏移、RANGE 单列约束、无 ORDER BY 的 RANGE 限制、NTILE/NTH_VALUE 参数等）。
- `engine.go`：多列分区（NULL 同区）、稳定多列排序与 peer 组构建，ROWS / RANGE / GROUPS 三种帧的区间计算，四种 EXCLUDE 掩码。
- `functions.go`：顶层 `Evaluate` 入口与全部聚合、取值、排名、LAG/LEAD 函数。

## 用法

```go
import "prescreen.local/ps-f6cb3f0fdc/windowframe"

rows := [][]windowframe.Value{
    {windowframe.Int(1), windowframe.Int(10)},
    {windowframe.Int(1), windowframe.Int(20)},
}
spec := windowframe.WindowSpec{
    PartitionColumns: []int{0},
    Order:            []windowframe.OrderSpec{{Column: 1}},
}
out, err := windowframe.Evaluate(rows, spec, windowframe.FuncExpr{
    Func:   windowframe.FuncSum,
    Column: 1,
})
```

帧边界用 `UnboundedPreceding()`、`UnboundedFollowing()`、`CurrentRow()`、`Preceding(v)`、`Following(v)` 构造；`Frame.End` 为 `nil` 时按 SQL 默认补 `CURRENT ROW`，`Frame` 为 `nil` 时按有无 `ORDER BY` 选择默认帧。

## 验收

- `acceptance_test.go`：1000 组随机数据集（高重复键、NULL、负值、混合类型、GROUPS 跨组偏移、四种 EXCLUDE、全部帧起止组合），每个函数、每一行都与 `reference_test.go` 中的暴力逐行定义参考器逐项比对。
- `deterministic_test.go`：题面强调的关键语义（RANGE NULL 当前行隔离、EXCLUDE GROUP 空帧的 COUNT=0/其余 NULL、GROUPS 的 NULL peer 组与 NULLS FIRST/LAST、默认帧、反转空帧、排名/NTILE、DESC RANGE、LAG/LEAD IGNORE NULLS、非法参数报错）。

运行：`go test ./...`（另以 `-race` 与 `-count` 重复验证）。
