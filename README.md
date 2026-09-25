# iterdrive — 通用迭代驱动器（检查点 / 取消恢复 / 并发控制）

纯 Python 3.11 标准库实现（不依赖 NumPy/SciPy），尺寸与索引全部显式传递，
小规模问题用自写稠密高斯消元（`iterdrive/dense.py: solve_dense`）独立核验。

## 结构

- `iterdrive/driver.py` — 驱动器主循环、安全点、回调证据日志、停止原因
- `iterdrive/checkpoint.py` — 原子 checkpoint 存取（tmp + fsync + rename）、SHA-256 完整性校验
- `iterdrive/control.py` — 线程安全控制通道（取消 / 追加预算 / checkpoint 请求）
- `iterdrive/digest.py` — 输入规范化序列化与摘要（绑定矩阵、右端项、尺寸、迭代器名）
- `iterdrive/iterators.py` — 两种内部状态不同的迭代器：Richardson（状态仅 `x`）与 CG（状态 `x, r, p, rs`）
- `iterdrive/problems.py` — Poisson 样例问题与稠密直接解核验
- `tests/test_driver.py` — 17 个 unittest 用例
- `examples/run_sample.py` — 端到端样例

## 协议

**Checkpoint 内容**：输入摘要、迭代器名、已完成迭代计数、残差历史、迭代器完整状态、
回调证据日志、停止原因。加载时校验 `len(residual_history) == iteration`，
恢复不会重复记录已完成迭代。

**安全点**（控制到达与持久化只在这些点生效）：

1. *迭代开始点* — 轮询控制通道：取消在此生效、有效预算（基础 + 并发追加）在此重估、
   排队的 checkpoint 请求在此执行。
2. *回调确认点* — 不可重算外部回调返回后，结果先写入证据日志并**立即持久化**，
   求解器才允许继续；恢复时从证据回放结果，绝不重复调用已确认的副作用。
3. *迭代结束点* — 完整状态原子落盘（tmp 文件 + fsync + `os.replace`），
   保存中断只会留下被忽略的 `.tmp` 文件，目标文件永远是上一个完整版本。

**停止原因**：`converged` / `cancelled` / `budget_exhausted` 三者区分，
并写入最终 checkpoint。

**恰好一次语义**：回调确认保存是提交点。若崩溃发生在"副作用已发生但确认保存未完成"
的窗口内，该次调用未被确认，恢复时会且仅会重试这一次（at-most-once 对已确认调用、
at-least-once 对未确认调用）—— 见 `test_crash_at_callback_confirmation_retries_only_unconfirmed`。

**输入变更**：矩阵/右端项/尺寸/迭代器任一变化都会改变输入摘要，
旧 checkpoint 在恢复时以 `InputMismatch` 拒绝。损坏或篡改的 checkpoint
以 `CheckpointCorrupt` 拒绝。

## 迭代器协议

求解器需实现：`name`、`state_dict()` / `load_state(state)`（JSON 可序列化）、
`step(ctx) -> float`（推进一步并返回残差范数）。外部回调必须经
`ctx.invoke_external(name, fn)` 调用，且在修改内部状态**之前**发起，
保证回调确认点落盘的状态始终处于迭代边界。

## 运行

```bash
python3.11 -m unittest discover -s tests -v
python3.11 examples/run_sample.py
```

## 样例输出

```
[cg]         reason=converged        iters=  4 residual=0.000e+00 err_vs_direct=2.483e-16
[continuous] reason=budget_exhausted iters=120 residual=9.015e-04 oracle_calls=120
[cancelled]  reason=cancelled        iters= 50 residual=2.875e-02 oracle_calls=50
[resumed]    reason=budget_exhausted iters=120 residual=9.015e-04 oracle_calls=120
[resumed]    identical to continuous run: True (side effects not repeated: True)
[stale]      checkpoint rejected after matrix change: checkpoint inputs differ from current problem/iterator
```

## 测试结果

```
Ran 17 tests in ~2s — OK
```

覆盖：两种迭代器收敛并与稠密直接解一致；取消/恢复与连续运行逐位一致；
外部副作用每次迭代恰好一次；证据进入 checkpoint；损坏/篡改/输入变更拒绝；
保存中断保持上一个有效版本；崩溃后恢复一致；预算并发追加；
预算/取消/checkpoint 请求并发到达；三种停止原因区分。
