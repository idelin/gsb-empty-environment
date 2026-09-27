# 区间算术配置验证（Interval Arithmetic Config Checker）

Python 3.11 标准库实现，无第三方依赖。对带有限整数区间变量的算术表达式做两层验证：

1. **保守区间分析**：计算结果的可靠整数区间。区间一定包住真实取值，但会因丢失变量相关性而变宽（例如 `x - x` 被估为 `[-1, 1]`，而实际恒为 `0`）。
2. **精确枚举**：当笛卡尔域大小不超过阈值时，逐点枚举所有赋值，判定是否存在真实违约或除零，并给出见证。

**保守告警绝不等于真实缺陷。** 只有精确枚举找到见证时才报告 `confirmed_failure`；枚举完整覆盖且未发现见证时，保守告警标记为 `refuted`。

所有内置示例均为手工构造的合成数据。

## 表达式与断言

- 常量：十进制整数（如 `0`, `17`, 一元负号 `-3`）
- 变量：字母/下划线开头的标识符
- 运算：`+`、`-`、`*`、`//`（向下整除，向负无穷取整，与 Python `//` 一致）
- 括号与一元 `+/-`；不支持真除法 `/`（会报错）
- 断言形如 `<表达式> <关系符> <整数常量>`，关系符为 `== != < <= > >=`

## 运行

需要 Python 3.11：

```sh
python3.11 -m interval_check examples/xx_acceptance.json
# 或从标准输入
cat examples/xx_acceptance.json | python3.11 -m interval_check
# 调整枚举上限（命令行覆盖输入中的值）
python3.11 -m interval_check --limit 10000 spec.json
```

## 输入格式（JSON）

```json
{
  "expression": "x - x",
  "variables": {"x": [0, 1]},
  "assertion": "x - x >= 0",
  "enumerate_limit": 1000000
}
```

- `expression`（必填，字符串）：待分析表达式。
- `variables`（必填，对象）：变量名到闭整数区间 `[lo, hi]`（要求 `lo <= hi`，两端均为整数）的映射；表达式用到的变量必须全部给出。
- `assertion`（可选，字符串）：结果需满足的断言；省略时只检查除零。
- `enumerate_limit`（可选，非负整数，默认 1000000）：所有变量域大小的乘积超过该值时跳过枚举。

## 输出格式（JSON）与状态

```json
{
  "status": "conservative_warning_refuted",
  "expression": "x - x",
  "assertion": "x - x >= 0",
  "interval": [-1, 1],
  "assertion_guaranteed": false,
  "enumeration": {
    "performed": true,
    "completed": true,
    "domain_sizes": {"x": 2},
    "total_assignments": 2,
    "assignments_checked": 2,
    "limit": 1000000,
    "reason": "all assignments checked; no witness found"
  },
  "findings": [
    {
      "kind": "assertion_violation_possible",
      "certainty": "conservative",
      "refuted": true,
      "message": "...",
      "interval": [-1, 1]
    }
  ]
}
```

- `status` 四种取值：
  - `safe`：区间即可证明，或完整枚举无违约/无除零。
  - `conservative_warning`：区间无法证明，且域过大未完成枚举（**不是**已证实失败）。
  - `conservative_warning_refuted`：区间发出告警，但完整枚举逐一排除（相关性损失属此类）。
  - `confirmed_failure`：枚举找到具体见证，随顶层 `witness` 返回。
- `interval`：结果区间；若某除数区间可能含 0 导致无法可靠传递，则为 `null`。
- `findings`：逐条发现，`certainty` 为 `conservative` 或 `confirmed`；保守项被完整枚举排除时 `refuted: true`。
- 见证 `witness.type`：
  - `assertion_violation`：含 `assignment`、`expression_value`、`assertion`。
  - `division_by_zero`：含触发除零的 `assignment`。

退出码：`0` 安全（含被推翻的告警）、`1` 未排除的保守告警、`2` 已证实失败、`3` 输入/解析错误。

## 验收边界

`x ∈ [0,1]` 时 `x - x` 恒为 `0`：朴素区间只能得到 `[-1, 1]`，对断言 `x - x >= 0` 只能产生**保守告警**；枚举两个点后告警被标记 `refuted`，退出码为 `0`，绝不会报告已证实失败。见 `examples/xx_acceptance.json`。

## 示例

| 文件 | 场景 | 状态 |
| --- | --- | --- |
| `examples/xx_acceptance.json` | `x-x` 相关性损失 | `conservative_warning_refuted` |
| `examples/correlation_safe_despite_warning.json` | 除数恒为 1 但区间看似含 0 | `conservative_warning_refuted` |
| `examples/proven_safe.json` | 区间直接证明 | `safe` |
| `examples/confirmed_counterexample.json` | 真实违约见证 `x=0` | `confirmed_failure` |
| `examples/divisor_zero.json` | 真实除零见证 `b=0` | `confirmed_failure` |

## 测试

```sh
python3.11 -m unittest discover -s tests -v
```

## 范围与非最优说明

- 区间算术是刻意的朴素（非关系型）分析：不追踪变量相关性，这是保守告警的来源。
- 枚举是精确的，但仅适用于有限且乘积不超过阈值的域；超阈值时结论停留在保守层，不声称安全也不声称失败。
- 枚举按字典序在第一个见证处停止；因此“首个见证”并非违约值数量或极端值的最优刻画，枚举只回答“是否存在”。
