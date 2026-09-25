# 多文件证据包：乱序接收 · 分块校验 · 跨文件引用 · 原子签收

纯标准库实现（Python 3.11，`unittest`，`sqlite3`）。

## 设计

- `evidence/manifest.py` — 清单：版本号 + 全部文件的分块摘要与整体摘要，
  规范序列化（`sort_keys` 紧凑 JSON）的 SHA-256 作为清单整体摘要。
- `evidence/chunk` 接收（`evidence/receiver.py`）— 块可乱序到达；同块重复接收
  幂等；同一 `(file_id, chunk_index)` 摘要不同即 `ChunkConflictError`；旧清单
  版本的迟到尾块返回 `rejected-stale`，绝不并入新清单。
- `evidence/validator.py` — 游标流式逐块校验（同一时刻只驻留一块）：块数、
  逐块摘要、文件整体摘要、跨文件引用目标存在性与摘要一致性。
- `evidence/signoff.py` — 校验通过后在**单个 SQLite 事务**内同时写入签收记录和
  通知 outbox（同一持久化决策）；`before_commit` 钩子模拟"校验完成、发布前
  崩溃"，崩溃后两表都无痕迹，可重试。重复签收幂等，重复通知确认幂等。
  `recover()` 启动时核查"通知必属有效签收"不变量。
- 清单更新（新版本）在一个事务内：旧签收置 `superseded`、新版本清单置
  `pending_reverification`、写入待重核查通知。
- `evidence/domain.py` — 事实/规则/步骤均带稳定 ID 与版本；规则是版本化纯
  函数，推导日志可确定性重放。
- `evidence/exporter.py` — 把已签收包流式导出为目录（大文件不进内存）。
- `evidence/verifier.py` — **独立验证器**，只读导出目录、不依赖接收端数据库：
  定位 `CONTENT_MISMATCH`（内容篡改）、`ORDER_MISMATCH`（同摘要块出现在错误
  位置=顺序篡改）、`MANIFEST_TAMPERED`（清单被改）、`MISSING_FILE`、
  `REFERENCE_TARGET_MISSING`、`REPLAY_MISMATCH`（重放篡改）等原因。
- `evidence/sample.py` — 确定性合成数据（传感器读数、mean/max/min 推导）。

## 运行

```sh
python3.11 -m unittest discover -s tests -v
```

## 测试覆盖

- `tests/test_receiver.py` — 乱序到达 + 重复幂等；同 ID 不同摘要冲突；
  旧清单尾块晚到被拒绝且不补进新清单。
- `tests/test_signoff.py` — 文件全部到齐但引用目标缺失时拒绝签收（且无签收/
  无通知）；清单更新使旧签收失效、新版本待重核查；签收幂等。
- `tests/test_recovery.py` — 校验后发布前崩溃（无签收、无通知、可重试）；
  签收后更正；重复通知确认幂等；恢复不变量（含伪造孤儿通知的防御性检测）。
- `tests/test_verifier.py` — 干净包导出后独立验证通过；篡改内容/块顺序/清单
  分别被定位；推导日志篡改产生 `REPLAY_MISMATCH`。
- `tests/test_large_file_memory.py` — 16 MiB 合成文件、64 KiB 分块，
  `tracemalloc` 断言校验/导出/验证的 Python 内存峰值 < 2 MiB。

## 实际运行结果（Python 3.11.15，macOS arm64）

```
$ python3.11 -m unittest discover -s tests -v
...
Ran 14 tests in 1.771s

OK
```

14 项全部通过；大文件内存实测（16 MiB 文件 / 64 KiB 分块，`tracemalloc`）：
校验+导出峰值约 181 KiB、独立验证峰值约 219 KiB（预算 2 MiB）。
文件型 SQLite 跨进程重开后 `recover()`：`anomalies=[]`、已投递 1 条、
待发 0 条——不存在"已通知却无有效签收"。
