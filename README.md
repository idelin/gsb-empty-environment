# 后台重建物化视图（快照重建 + 增量追平 + 原子切换）

纯 Python 3.11 标准库实现，变更来自本地 JSON 夹具，不接消息平台。

## 流程

1. **固定快照**：`BaseStore.snapshot()` 打出 `(表数据, 快照水位)`，后台线程据此构建新视图。
2. **增量缓存**：构建期间到达的变更进入缓冲区，同时仍应用到旧视图（旧读者看到的数据持续新鲜）。
3. **追平**：构建完成后，按 `commit_seq` 升序回放缓冲区中水位之后的增量。
4. **原子切换**：仅当新视图水位 >= 当前视图水位才切换指针；旧视图冻结，旧读者句柄仍按旧版本可读。
5. **失败兜底**：构建异常时状态记为 `FAILED`，旧视图继续对外服务、继续接受增量。

## 不变式

- 事件按 `commit_seq` 升序应用；`commit_seq <= watermark` 或 `(txn_id, table, pk)` 重复的事件幂等跳过 —— 切换前后无双计。
- 未追平（新视图水位落后）绝不发布；快照过旧永远追不平时保持 `BUILDING`，可 `cancel_rebuild()`。

## 变更格式（fixtures/changes.json）

```json
{"txn_id": "T1", "table": "orders", "pk": 4,
 "before": null, "after": {"id": 4, "amount": 400}, "commit_seq": 101}
```

`before=null` 为插入，`after=null` 为删除，否则为更新。

## 运行

```bash
python3.11 main.py                        # 演示：重建->缓存->追平->切换->失败兜底
python3.11 -m unittest discover -s tests -v   # 9 个用例
```

## 结构

- `cdc_view/events.py` — `ChangeEvent`（事务 ID/表/主键/前后值/提交序号）与夹具加载
- `cdc_view/store.py` — `BaseStore` 源库夹具，应用变更、打固定快照
- `cdc_view/view.py` — `MaterializedView` 按表聚合计数/金额，幂等应用
- `cdc_view/manager.py` — `ViewManager` 重建调度、增量缓存、追平校验、原子切换
- `tests/test_rebuild.py` — 全链路 unittest
