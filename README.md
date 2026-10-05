# difftest — 双执行器差分测试工具

对同一输入分别运行两个执行器（A/B），记录各自的**返回值、运行错误或超时**，
按题目给定的规范化规则做差分判定，发散时自动最小化输入并保存可重放样本与环境摘要。

## 构建与测试

```sh
go build ./cmd/difftest
go test ./...
```

## 使用

```sh
# 差分运行；发散样本保存到 cases/
difftest run --a "./progA" --b "./progB" --input in.txt \
    --timeout 2s --norm trim,collapse,float --float-tol 1e-9 \
    --strip 'pid=\d+' --cases cases

# 回放已保存的样本，验证是否复现
difftest replay cases/20261005T030434-semantic_diff
```

`--norm` 可选规则：`trim`（去首尾空白）、`collapse`（压缩空白）、`casefold`（大小写折叠）、
`sortlines`（行序无关）、`float`（按 token 数值比较，容差 `--float-tol`）、
`float-strict`（非数值 token 判为解析失败）。`--strip` 可重复，先删除匹配正则再比较。
规则之外的比较一律按字面进行。

## 判定类别（互斥，不会混淆）

| 判定 | 含义 |
|---|---|
| `agree` | 规范化后值相等 |
| `semantic_diff` | 两侧都产出值，但规范化后不等 |
| `timeout_diff` | 恰好一侧超时（**不是值不等**） |
| `both_timeout` | 两侧都超时，无语义结论 |
| `run_error_diff` | 恰好一侧运行失败，或退出码不同 |
| `both_run_error` | 两侧以相同退出码失败（不算发散） |
| `parse_error` | 某侧输出无法按规则解析（如 strict float 下出现非数值） |

判定按结果类别先行分流：超时与运行错误永远不会落入值比较；
解析失败与语义差异是不同的判定类别。

## 样本保存

每个发散样本保存为一个目录：

- `input.txt` — 经 ddmin 风格最小化后的最小可重放输入（`--no-minimize` 可关闭）
- `case.json` — 工具版本、环境摘要（OS/架构/内核/Go 版本/时间戳）、
  两侧执行器命令与超时、规范化规则、原始结果、判定结论、输入 SHA-256

`difftest replay` 校验输入校验和后按保存的配置重跑，报告是否复现原判定。

## 故障注入测试

`difftest/difftest_test.go` 通过重执行测试进程注入故障
（`exit:N` 运行失败、`sleep:X` 超时、垃圾输出触发解析失败、不同值触发语义差异），
验证各类结果两两不被混淆，并覆盖最小化与样本保存/回放往返。
