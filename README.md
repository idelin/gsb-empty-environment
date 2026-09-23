# keymigrate

分区函数（partition function）版本升级时的键状态迁移。纯 Python 3.11 标准库，
无消息平台：变更事件来自本地夹具 `fixtures/changes.json`，每条事件包含
事务 ID（`tx_id`）、表（`table`）、主键（`pk`）、前/后值（`before`/`after`）
和提交序号（`commit_seq`）。

## 设计

- `keymigrate/partition.py` — 版本化分区函数 `PartitionFunction(version, num_partitions)`，
  基于 SHA-256 的稳定哈希。
- `keymigrate/registry.py` — 所有权注册表。每个键在任意时刻只有一个有效拥有者
  `(partition, version, epoch)`；epoch 作为 fencing token，过期 epoch 的写入
  会被存储拒绝（`FencingError`）。所有权只能通过 begin/commit/rollback 转移。
- `keymigrate/store.py` — 每个分区一个状态存储。按键的 `commit_seq` 水位线去重，
  重复投递/重复迁移/重复回放都不会复制状态。
- `keymigrate/router.py` — 路由器。切换前已发往旧路由的事件留在旧分区并随快照
  一起迁移（不丢失）；迁移进行中的在途事件被缓冲，转移提交后交付新拥有者，
  回滚则退回旧拥有者。路由日志 `journal` 记录每次投递。
- `keymigrate/migration.py` — 迁移器。逐键两阶段转移：缓冲在途事件 → 快照 +
  缓冲事件登台到新分区 → 提交所有权 → 旧分区删除。路由（投递）失败时回滚
  所有权：登台数据丢弃、拥有者不变、缓冲事件退回旧拥有者。重复执行同一
  目标版本的迁移是 no-op。
- `keymigrate/replay.py` — 按 `commit_seq` 顺序回放提交日志即可恢复状态；
  支持 `from_seq` 断点续放，水位线去重保证可重入。

## 运行

```sh
python3.11 main.py                              # 运行演示样例
python3.11 -m unittest discover -s tests -v     # 运行测试
```

演示流程：v1（2 分区）路由切换前事件 → 迁移到 v2（4 分区，期间注入在途事件）
→ 重复迁移（no-op）→ 注入路由失败迁移 v3（回滚，所有权与状态不变）
→ 回放全量日志恢复并与在线状态比对。
