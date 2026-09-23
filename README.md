# txbuffer — 事务变更缓冲器

纯 Python 3.11 标准库实现（`sqlite3` + `unittest`），无消息平台：变更事件来自本地 JSON 夹具。

## 语义保证

- **同事务多条变更缓冲，提交标记到达后原子应用**：变更先写入 WAL，提交时数据写入、WAL 标记、位点推进在同一个 SQLite 事务中完成。
- **回滚丢弃**：`rollback` 标记丢弃该事务全部缓冲变更，并记录事务已完结。
- **序号缺口暂停**：按全局 `seq` 连续消费；缺口出现时暂停（`state=paused`），补齐后自动恢复。
- **重复事务不二次生效**：`applied_tx` 表记录已完结事务；重复 `seq` 与已完结事务的重投事件（含新序号的重投）均被忽略，且以墓碑占位避免序号空洞卡死流。
- **事务内先删后插同键按序**：提交时按 `seq` 顺序应用变更。
- **提交标记早到需等完整清单**：提交标记携带 `expected_changes`，缓冲变更数不足时等待（`state=waiting`）。
- **半事务恢复后不可见**：读者只能读 `data` 表（已提交状态）；未提交变更只存在于 WAL，重启后仍不可见，且可继续补全提交。

## 事件格式

```json
{"kind": "change",   "seq": 1, "tx_id": "tx-1", "table": "users", "pk": "u1", "op": "insert", "before": null, "after": {"name": "Ada"}}
{"kind": "commit",   "seq": 3, "tx_id": "tx-1", "expected_changes": 2}
{"kind": "rollback", "seq": 2, "tx_id": "tx-2"}
```

`op` 取 `insert` / `update` / `delete`；`insert`/`update` 需 `after`，`delete` 需 `before`。

## 结构

- `txbuffer/events.py` — 事件模型与校验
- `txbuffer/store.py` — SQLite 持久层（meta / wal / applied_tx / data）
- `txbuffer/buffer.py` — 核心：去重、顺序消费、原子应用、状态查询
- `txbuffer/loader.py` — 本地夹具加载
- `fixtures/` — 7 个场景夹具
- `main.py` — 演示全部场景
- `tests/test_txbuffer.py` — 12 个 unittest 用例

## 运行

```bash
python3.11 main.py                          # 运行样例
python3.11 -m unittest discover -s tests -v # 运行测试
```
