# evpkg — 多文件证据包接收、校验与原子签收

纯 Python 3.11 标准库实现（`sqlite3` / `hashlib` / `json` / `unittest`），
合成样例数据，证据可重放。

## 设计

- `evpkg/model.py` — 事实 / 规则 / 步骤均带稳定 ID 与版本；`replay()` 确定性
  重放（白名单 AST 表达式求值），`replay_digest` 固化进清单。
- `evpkg/manifest.py` — 清单含自身版本与整体摘要（版本 + 全部文件条目 +
  每块摘要 + 跨文件引用 + 重放摘要的规范 JSON 之 SHA-256）。
- `evpkg/store.py` — SQLite 单文件持久化；`transaction()` 显式
  `BEGIN IMMEDIATE / COMMIT / ROLLBACK`。签收记录与通知 outbox 在**同一
  事务**提交，恢复后不可能出现"已通知却无有效签收"。
- `evpkg/receiver.py` — 乱序分块接收（逐块校验、重复幂等、同位不同摘要
  冲突、旧清单尾块拒绝）；`validate()` 流式校验；`publish()` 原子签收。
  新清单版本使旧签收失效、旧签收通知撤回（retracted），并生成
  `pending_reverification` 状态与 `signoff_invalidated` 通知。
- `evpkg/exporter.py` / `evpkg/verifier.py` — 导出为
  `manifest.json` + 按序号命名的块文件；独立验证器只读导出目录，
  能区分清单篡改 / 内容篡改 / 顺序错乱 / 引用缺失 / 重放不符。
- `evpkg/sample.py` — 确定性合成数据（`synthetic_bytes` 可复现大文件）。

## 不变量

- 重复接收同一 `(file_id, chunk_index)` 且摘要一致 → 幂等 no-op；摘要不一致
  → `ConflictError`。同一 `package_id@version` 不同清单摘要 → `ConflictError`。
- 文件全部到齐但引用目标缺失 → `SignoffBlocked`，不产生任何签收/通知。
- 崩溃恢复后 `check_invariants()` 必须为空：不存在未撤回的
  `package_signed_off` 通知指向无效签收。

## 运行

```sh
python3.11 -m unittest -v tests.test_evpkg
```

实际运行结果见 `RESULTS.md`。
