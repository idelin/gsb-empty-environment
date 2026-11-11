# 隐私查询敏感度传播

安全统计编译器需要推导查询对单条输入变化的最大影响。写供Go编译宿主调用的敏感度包，AST支持有界计数、截断求和、投影、并集和按已知唯一键连接；连接放大由显式重复上界决定，缺少上界返回未知。输出可复核的逐节点推导，用小数据集枚举相邻输入核验上界。

## `sensitivity` 包

实现位于 `sensitivity/`，纯标准库（`math/big.Rat` 精确有理数，无浮点误差），
供 Go 编译宿主以库方式调用。

### 算子与敏感度规则（单条输入 = 某基础关系一行的加入/删除）

| AST 节点 | 语义 | 敏感度 |
| --- | --- | --- |
| `Source` | 基础关系叶子 | 行级 1 |
| `BoundedCount` | `min(count, Cap)` 有界计数 | 标量 = 子行级（截断不降低单行影响） |
| `TruncatedSum` | 单元夹取到 `[Lower, Upper]` 后求和 | 标量 = 子行级 × `max(|Lower|, |Upper|)`；任一端点缺失 → 未知 |
| `Project` | 列投影 | 行级 = 子行级 |
| `Union` | 同模式 `UNION ALL` | 行级 = `max(左, 右)` |
| `Join` | 已知唯一键等值内连接 | 行级 = `max(左行级×RightRepeat, 右行级×LeftRepeat)`；任一重复上界缺失 → 未知 |

未知敏感度沿 AST 向上传播，`Sensitivity.Reason` 与 `Trace.Formula`
记录可复核的具体原因和代入数值的推导依据。

### 宿主调用流程

1. 用 `Source`/`BoundedCount`/`TruncatedSum`/`Project`/`Union`/`Join` 构造 AST。
2. `Derive(root)` 自底向上推导，返回逐节点 `Trace`，`Trace.Render()` 输出缩进推导树。
3. `Eval(root, tables)` 对具体数据精确求值（标量或表）。
4. `Verify(root, base, extraRows)` 枚举全部相邻输入（删除每一行 + 加入候选行），
   对每个节点比较经验最大变化与推导上界，并检查连接声明的重复上界是否与数据相符；
   `VerifyReport.Render()` 输出完整核验报告，任何越界都会置 `Violation=true`。

### 演示与测试

```sh
go run ./cmd/sensitivity-demo   # 打印推导树、核验报告和缺上界时的未知传播
go test ./... -race -cover      # 单元测试 + 小数据集端到端核验
```
