# gradacc — 微批梯度累积训练器（纯标准库）

Python 3.11，仅使用标准库，CPU 小规模张量自行实现，不依赖自动微分或任何张量库。
多个微批累积梯度后一次提交更新；支持溢出检测与整批跳过、乱序/重试/撤回的微批、
按批次 ID 与参数版本管理、跨分片两阶段（prepare/publish）提交、包含未确认批次的
checkpoint，以及可控故障调度 + 独立逐批参考实现的一致性验证。

## 运行

```bash
python3.11 -m unittest discover -s tests -t .   # 运行全部测试
python3.11 run_demo.py                          # 运行故障调度演示 + 一致性校验
```

## 模块结构

- `gradacc/tensor.py` — 手写 CPU 张量（扁平 float 列表 + shape），加/减/缩放/matmul/outer/有限性检查。
- `gradacc/model.py` — 线性回归模型，手写前向与 MSE 反向（无自动微分）。
- `gradacc/accumulator.py` — 梯度累积器：按 `(batch_id, 参数版本)` 管理微批，原子提交。
- `gradacc/shard.py` — 参数分片：持有参数与动量，显式 PREPARED → PUBLISHED 两阶段状态。
- `gradacc/coordinator.py` — 跨分片两阶段提交协调器，prepare 与 publish 之间设故障注入点。
- `gradacc/trainer.py` — 组装累积器与协调器，对外暴露训练 API。
- `gradacc/checkpoint.py` — JSON checkpoint，含未确认（open）批次与已 prepare 未 publish 的批次。
- `gradacc/faults.py` — 可控故障调度器（乱序、重试、撤回、注入非有限梯度、prepare 后崩溃）。
- `gradacc/reference.py` — 独立逐批参考实现（自己的梯度与 SGD 代码，直接从数据集重算）。
- `tests/` — unittest 测试（43 个）；`tests/scenario.py` 为演示与一致性测试共用的故障场景。

## 核心语义

**累积与提交**
- `begin_batch(batch_id)` 绑定当前参数版本；微批按 `micro_id` 去重：乱序到达可接受，
  同 `micro_id` 重投（重试）替换旧副本，`revoke_microbatch` 撤回。
- `commit_batch` 聚合（按样本数加权的均值）后一次性应用：先算完所有新值再整体换入，
  失败不会部分更新参数。同批次 ID 重复提交幂等——直接返回首次结果，不重复应用。

**清零语义**
- 提交（无论应用还是跳过）即消费该批次的累积梯度；`zero_batch` 显式清空一个开放批次
  并将其版本重新对齐到当前参数版本；`abort_batch` 直接作废。

**溢出跳过**
- 任一微批梯度非有限（inf/nan）→ 整批跳过：所有参数保持旧值，优化器步数不推进，
  动量缓冲不写入（分片在 prepare 阶段不做任何修改，SKIP 决策在 publish 时为空操作）。

**参数版本**
- 每次成功应用使版本 +1；提交时批次版本落后于当前版本 → `rejected_stale`，梯度丢弃，
  不应用任何更新。不同批次的梯度完全隔离，互不串扰。

**跨分片两阶段提交**
- `prepare`：记录决策（PUBLISH/SKIP）与梯度，不改状态；`publish`：原子应用（单分片内
  全部参数要么全更新要么全不更新）。prepare 之后、publish 之前崩溃：checkpoint 中存有
  该未确认批次，恢复后 `resume_pending()` 按已记录决策确定性重放，随后幂等重试提交。

**checkpoint**
- JSON 序列化，包含：参数、动量、各分片步数、参数版本、已 prepare 未 publish 的批次、
  未确认（open）批次及其微批梯度、已关闭批次台账（保证恢复后重复提交仍幂等）。

## 运行样例（`python3.11 run_demo.py` 摘要）

```
--- event log ---
  corrupt A m3        # 坏副本 ...
  micro A m3          # ... 被干净重试替换
  revoke A m9 True    # 撤回
  commit A committed
  commit A committed  # 重复提交：幂等，未二次应用
  commit D rejected_stale
  corrupt B m2        # 非有限梯度
  commit B skipped_overflow
  arm_crash
  crash C             # prepare 后、publish 前崩溃
  recover C committed # 从 checkpoint 恢复并重放

--- final trainer state ---
step=3 version=3
params:   {'w': [2.189112, -0.210477], 'b': [0.969158]}
momentum: {'w': [-19.930456, -0.896892], 'b': [-5.833181]}
batches:  {'A': 'committed', 'B': 'skipped_overflow', 'D': 'rejected_stale',
           'C': 'committed', 'E': 'committed'}

--- parity check vs independent reference ---
  [OK] step count
  [OK] param version
  [OK] batch statuses
  [OK] params+momentum (max abs diff 0.00e+00)

--- checkpoint with an unconfirmed batch ---
  open (unconfirmed) batches in checkpoint: ['pending-batch']
  restored and committed: committed, step=1

=== ALL CHECKS PASSED ===
```

## 测试结果（`python3.11 -m unittest discover -s tests -t .`）

```
Ran 43 tests in 0.011s
OK
```

覆盖要点：

- **独立数值验证**：`test_model.py` 用中心有限差分核对解析梯度；`test_numerical.py`
  用人工推导的期望值（w0=0.5, b0=0.1, lr=0.1, momentum=0.5 下逐批手算）核对
  累积-提交全链路，包括溢出跳过后第三步的动量递推。
- **溢出整批跳过**：`test_trainer.py::test_nonfinite_second_micro_keeps_all_params_old`
  验证第二微批非有限时所有参数保持旧值、动量与步数不变。
- **幂等**：同批 ID 重复提交只应用一次（崩溃恢复后、checkpoint 恢复后同样成立）。
- **乱序/重试/撤回/隔离**：`test_accumulator.py` 验证乱序聚合、重试替换、撤回剔除、
  批次间梯度不串扰、过期版本拒绝、清零重对齐。
- **两阶段提交**：`test_shard.py` 验证 prepare 不改状态、publish 原子且幂等、SKIP 不动
  步数与动量、prepare/publish 之间崩溃后可重放。
- **checkpoint**：`test_checkpoint.py` 验证未确认批次入档、恢复后继续与未中断运行一致、
  已 prepare 未 publish 批次可恢复。
- **端到端一致性**：`test_parity.py` 在含全部故障类型的调度下，对比训练器与独立逐批
  参考实现的最终参数、动量与计数（步数/版本/批次状态），结果完全一致。
