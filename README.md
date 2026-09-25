# iterdriver — 通用迭代驱动器

纯 Python 标准库实现（3.11+），不依赖 NumPy/SciPy。驱动器把"迭代循环"
与"求解器内部状态"解耦，提供可检查点化、可取消、可预算化、副作用幂等
的迭代协议。

## 求解器协议

任何实现以下接口的迭代器都可被驱动：

```python
solver.kind                   # str，写入检查点，防止跨求解器恢复
solver.state_dict()           # -> JSON 可序列化的内部状态
solver.load_state_dict(state) # 恢复内部状态
solver.step(effects)          # 执行一次迭代，返回残差
```

尺寸与索引全部显式传入（`matvec(a, x, n_rows, n_cols)` 等），形状错误
立即抛出 `ShapeError`。

## 检查点内容

```json
{"sha256": "<payload 的 SHA-256>",
 "payload": {
   "format_version": 1,
   "input_digest": "<矩阵+右端项+尺寸+参数的内容摘要>",
   "solver_kind": "heavy-ball",
   "iteration": 5,
   "solver_state": {"x": [...], "x_prev": [...], "steps": 5},
   "residual_history": [...],
   "callback_ledger": {"0:step-size": {"payload": {...}, "result": 0.22}, ...}
 }}
```

- **输入摘要**：矩阵、右端项、尺寸、参数任一变化都会改变 digest；
  恢复时 digest 不匹配即抛出 `CheckpointInputMismatch`，拒绝旧检查点。
- **完整性**：payload 带 SHA-256，损坏/截断/篡改的文件抛出
  `CheckpointCorruptError`。
- **原子保存**：先写同目录临时文件、fsync、再 `os.replace`。保存中断
  不会破坏上一份检查点，残留的 `.ckpt-*.tmp` 文件加载时被忽略。

## 不可重算的外部回调（副作用幂等）

求解器阶段通过 `effects.call(name, payload, fn)` 调用外部服务。结果在
迭代完成前写入 `callback_ledger`（键为 `"<iteration>:<name>"`），成为
检查点证据。恢复后，已确认的回调**不再重复调用**，直接从台账回放结果；
只有新迭代才会真正调用外部服务。因此"取消→恢复"与"连续运行"的轨迹
逐位一致（测试中为完全相等断言）。

## 安全点与并发

预算追加（`Budget.add`）、取消（`request_cancel`）、检查点请求
（`request_checkpoint`）可从任意线程并发到达，**只在迭代边界的安全点
生效**，绝不在 `solver.step` 中途打断。安全点处理优先级：

1. 取消 → 保存最终检查点，以 `CANCELLED` 停止
2. 检查点请求 → 保存后继续运行
3. 收敛判定（`residual <= tolerance`）→ 保存后以 `CONVERGED` 停止
4. 预算 → 耗尽则保存后以 `BUDGET_EXHAUSTED` 停止，否则消耗一个单位

停止原因 `StopReason` 三值区分：取消 / 收敛 / 预算耗尽。残差历史每次
完成的迭代只记录一条，恢复后不重复记录。

## 两种不同内部状态的迭代器

- `JacobiSolver`：状态为单向量 `x`（阻尼 Jacobi）。
- `HeavyBallSolver`：状态为双向量 `x`、`x_prev` 加步数计数器，且每步
  经台账调用外部步长 oracle（不可重算回调的载体）。

## 目录结构

```
iterdriver/
  dense.py       # 显式尺寸的自写稠密实现（matvec/norm/残差核验）
  checkpoint.py  # 输入摘要、检查点模型、原子文件存储
  driver.py      # 驱动器：安全点、预算、取消、EffectInvoker
  solvers.py     # Jacobi 与 Heavy-Ball 两种迭代器
tests/test_driver.py   # 14 个 unittest 用例（含故障注入）
examples/run_sample.py # 取消/恢复端到端样例
```

## 运行

```console
$ python3 -m unittest discover -v   # 测试
$ python3 examples/run_sample.py    # 样例
```

## 样例输出

```text
checkpoint file: /var/folders/.../iterdriver-demo-8yj3xaew/run.ckpt.json
leg 1: stop=cancelled iterations=5 residual=8.559e-02 service_calls=5
leg 2: stop=converged iterations=48 residual=6.073e-13 service_calls=48
ledger entries (confirmed side effects): 48
  replayed on resume: 0..4, newly confirmed: 5..47
  no residual record duplicated: True
dense verification ||b - A x|| = 6.073e-13 (tolerance 1e-12)
final iterate x = [0.07635170, 0.34893764, 0.69131111, 1.42684401]
jacobi reference: stop=converged iterations=26
```

## 测试结果

```text
test_callback_results_are_checkpoint_evidence ... ok
test_cancel_resume_matches_continuous_run ..... ok
test_budget_topup_checkpoint_and_cancel_at_safe_points ... ok
test_heavy_ball_converges_and_dense_check_passes ... ok
test_jacobi_converges_and_dense_check_passes ... ok
test_corrupted_checkpoint_is_rejected ......... ok
test_input_change_invalidates_checkpoint ...... ok
test_interrupted_save_preserves_previous_checkpoint ... ok
test_solver_kind_mismatch_is_rejected ......... ok
test_stray_temp_file_does_not_affect_load ..... ok
test_digest_tracks_every_input ................ ok
test_explicit_sizes_are_validated ............. ok
test_budget_exhaustion_then_resume ............ ok
test_zero_budget_stops_immediately ............ ok

Ran 14 tests in 0.019s
OK
```

故障注入覆盖：损坏检查点（截断/校验和错）、输入矩阵变更后拒绝旧检查点、
保存中断（第 N 次保存写一半崩溃，上一份检查点完好）、遗留临时文件、
求解器类型不匹配。并发用例用事件门控确定性地验证：预算追加、检查点
请求、取消三者跨线程到达时均在安全点生效。
