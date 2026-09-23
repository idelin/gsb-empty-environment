# 后台视图重建与原子切换（纯标准库，Python 3.11+）

不接消息平台，变更来自本地夹具（`fixtures.py`）。每条变更含：事务 ID、
表、主键、前后值、提交序号（`commit_seq`，即水位单位）。

## 流程

1. `Rebuilder` 从 `SnapshotSource` 取固定快照（LSN L0）作为新视图基线；
2. 从 `ChangeLog` 读取 `commit_seq > L0` 的缓存增量，按提交序回放；
3. 追平循环：发布前在同一把锁内检查 `watermark >= log.tip()`，
   未追平则继续追，绝不发布落后视图；
4. `ViewStore.publish_if_caught_up` 原子替换当前版本；
5. 旧读者持有旧版本句柄继续查询，互不干扰；
6. 任何失败（快照/读日志异常）→ 返回 `None`，旧视图保持可用。

## 不双计

`View.apply` 以 `commit_seq <= watermark` 为去重不变量：切换后重放的
变更被丢弃，新变更恰好应用一次。

## 文件

- `cdc.py` — Change / Snapshot / ChangeLog（本地夹具日志）
- `view_store.py` — View / ViewStore（版本化、原子切换）
- `rebuilder.py` — 后台重建与追平发布
- `fixtures.py` — 样例快照与变更流
- `demo.py` — 运行样例：`python3 demo.py`
- `test_rebuilder.py` — 测试：`python3 -m unittest test_rebuilder -v`
