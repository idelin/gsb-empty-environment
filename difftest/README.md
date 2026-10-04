# difftest — 双执行器差分测试工具

对同一输入分别用两个执行器运行，记录各自的**返回值 / 错误 / 超时**，
由差分判定器**只按题目给定的规范化规则**比较，并把最小可重放样本与
环境摘要落盘。

## 构建与测试

```sh
go build -o difftest ./cmd/difftest
go test ./...
./difftest selftest   # 故障注入自检
```

## 判定契约（核心不变量）

判定优先级自上而下，超时**永远不是值不等**：

| 条件 | 判定 | 说明 |
| --- | --- | --- |
| 任一侧超时 | `timeout` | 超时说明不了"本该产出什么值"，不构成值不等 |
| 任一侧非零退出/无法启动 | `run_error` | 基础设施失败，不是语义差异 |
| 输出违反题目格式规则（如应为数字） | `parse_error` | 解析失败 ≠ 语义差异 |
| 规范化后的值不同 | `semantic_diff` | 唯一会被最小化的"有趣"判定 |
| 规范化后的值相同 | `equal` | |

比较只发生在规范化之后。规范化规则（`--rules rules.json`）：

```json
{
  "trim_line_trailing_space": true,
  "ignore_trailing_blank_lines": true,
  "collapse_whitespace": false,
  "case_insensitive": false,
  "numeric_tokens": true,
  "float_tolerance": 1e-6
}
```

`numeric_tokens` 要求每个 token 可解析为浮点数，否则记 `parse_error`；
`float_tolerance` 为数值比较的绝对容差。

## 用法

```sh
# 单次差分运行，保存可重放样本
difftest run --a "./sol_a" --b "./sol_b" --input in.txt \
    --timeout 2s --rules rules.json --save cases/001

# 最小化触发 semantic_diff 的输入（ddmin：行级 + 字节级），保存最小样本
difftest shrink --a "./sol_a" --b "./sol_b" --input in.txt --save cases/min

# 重放保存的样本，校验判定与记录一致
difftest replay --case cases/min

# 故障注入自检：证明解析失败 / 运行失败 / 超时 / 语义差异互不混淆
difftest selftest
```

`--a`/`--b` 按空白切分为 argv（无 shell 引号展开）。

## 样本目录结构

```
case/
├── input.bin   # 最小可重放输入（sha256 校验）
└── case.json   # 执行器命令、规则、双侧原始 outcome、判定结果、
                # 环境摘要（OS/架构/Go 版本/主机/UTC 时间）
```

## 代码结构

- `internal/runner` — 执行器抽象与命令执行，区分 value/error/timeout
- `internal/normalize` — 题目给定的规范化规则与解析失败检测
- `internal/judge` — 差分判定器（上述判定契约）
- `internal/shrink` — ddmin 最小化（行级后字节级）
- `internal/casestore` — 样本与环境摘要的保存/加载/校验
- `internal/faultinj` — 故障注入执行器（测试与 selftest 用）
