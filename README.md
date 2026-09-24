# Boundary-Aware Change Compaction（边界感知变更压缩）

合并同键连续变更，同时保证在所有声明可见提交点上与原始流严格等价。
纯 Python 3.11 标准库实现，测试基于 `unittest`，输入来自本地 JSON 夹具
（含事务 ID、表、主键、前后值、提交序号），不依赖任何消息平台。

## 语义规则

- **同键连续合并**：`A→B→C` 合并为 `A→C`，保留最早前值与最终状态。
- **事务可见性边界**：同一事务内的变更可随时合并；跨事务合并仅当
  前一提交点**未**被声明为可见时允许——跨提交不得隐藏中间可见版本。
- **身份语义**：`delete` 后接 `insert`（删除重建）是身份边界，禁止合并
  （否则会把"换了一个实体"伪装成一次更新）。
- **插后删消去**：`insert` 后接 `delete` 可整体消去；`audit=True` 时
  在审计日志中保留证据（表、主键、值、源变更序号）。
- **schema 屏障**：schema 版本不同的相邻变更禁止合并。
- **前值验证**：压缩前后都对流做链式校验（`before` 必须等于重放出的
  当前值），不一致即抛 `ChainError`。
- **预算**：`budget` 限制合并次数；超限后剩余片段原样保留（未压缩）。

## 等价性证书

`build_certificate()` 生成自包含 JSON 证书：输入/输出变更、可见提交点、
每个可见点的可见状态（含实体身份=存活键集合）与下游增量聚合
（`row_count`、`total`）、审计证据及 SHA-256 摘要。
`verify_certificate()` 独立重放：重新执行压缩、逐可见点比对等价性、
校验全部摘要，任何篡改（包括伪造可见点声明）都会抛 `CertificateError`。

## 穷举核验与反例

`exhaustive_verify()` 穷举小变更序列（长度 ≤4、值域 {0,1}、提交 {1,2}
及其全部可见点子集）：**1704 个案例，边界感知压缩 0 失败**；同时统计出
**408 个"朴素压缩"（无视可见边界）会被判定不等价的案例**。
`find_counterexample()` 给出最终状态相同但中间语义不同的反例：
`[ins A@c1, upd A→B@c2]` 与 `[ins B@c2]` 终态均为 `B`，但在可见提交 1
处状态不同——证明"只比终态"的压缩是不安全的。

## 文件

- `compactor.py` — 压缩、重放、等价校验、证书、穷举、反例、夹具加载
- `fixtures/*.json` — 本地变更夹具（tx_id/table/pk/before/after/commit_seq/schema）
- `run_demo.py` — 九段演示（合并、消去、身份、边界、预算、证书、反例、穷举）
- `test_compactor.py` — 19 个 unittest 用例
- `demo_output.txt` — 演示实际输出

## 运行

```bash
python3 run_demo.py        # 演示（输出见 demo_output.txt）
python3 -m unittest -v     # 测试
```

## 测试结果

```
Ran 19 tests in 0.031s
OK
```

穷举核验：`cases: 1704, failures: 0, naive violations caught: 408`
（`max_len=4, values=(0,1), commits=(1,2)`）。
