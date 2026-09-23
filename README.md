# keymigrate

分区函数版本变化时的键状态迁移。纯 Python 3.11 标准库实现，无消息平台依赖；
变更来自本地 JSON 夹具（事务 ID、表、主键、前后值、提交序号 lsn）。

## 语义保证

- **单实体唯一有效拥有者**：`OwnershipRegistry` 中每个键恒有一条 `OwnerRecord`；
  挂起的迁移在提交前不改变有效拥有者，存储层按 epoch 围栏拒绝过期拥有者。
- **切换期间旧路由事件不丢**：迁移挂起期间，事件仍投递给旧拥有者，同时被捕获到
  迁移的 inflight 缓冲，提交时转发给新拥有者（幂等去重）。
- **重复迁移不复制状态**：迁移 ID 幂等（已 active/completed 直接跳过）；存储层按
  每键 `(lsn, txid)` 水位线去重，状态切片重复导入是 no-op。
- **路由失败回滚所有权**：提交期间向新拥有者转发失败时，回滚所有挂起迁移，
  新拥有者上的拷贝被清除，旧拥有者仍是唯一有效拥有者，事件不丢。
- **回放可恢复**：所有路由与迁移记录写入追加式日志（JSONL），`replay()` 用同一套
  Router/Coordinator 代码路径确定性重建注册表与状态。

## 结构

- `keymigrate/events.py` — `ChangeEvent`、夹具加载、追加式 `EventLog`
- `keymigrate/partition.py` — 版本化分区函数（sha256 → 分区槽位）
- `keymigrate/store.py` — 单拥有者状态存储（幂等 apply、epoch 围栏、切片导入导出）
- `keymigrate/ownership.py` — 所有权注册表（begin/commit/rollback 迁移）
- `keymigrate/router.py` — 事件路由（挂起期间捕获旧路由事件）
- `keymigrate/migration.py` — 迁移协调器（begin/commit/rollback、失败回滚）
- `keymigrate/recovery.py` — 日志回放恢复
- `fixtures/changes.json` — 本地变更夹具（12 条事件，含更新与删除）
- `demo.py` — 运行样例；`tests/` — unittest 测试

## 运行

```bash
python3 demo.py                      # 运行样例（迁移 + 校验 + 回放）
python3 -m unittest discover -s tests -v   # 运行测试
```
