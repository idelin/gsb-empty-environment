# CDC 一致性快照引导（纯标准库，Python 3.11）

读取带一致性水位的全量快照，同时缓存后续变更；安装快照后仅回放水位
之后的事件，防双计、防漏变更。变更来自本地夹具（事务ID、表、主键、
前后值、提交序号），不接消息平台。

## 正确性保证

- **快照包含的更新不再应用**：`commit_seq <= 水位` 的事件直接跳过，
  同一事务ID重复投递也会被去重。
- **扫描期间的删除最终删除**：删除事件在水位之后，回放时移除目标行。
- **快照失败不发布混合状态**：快照装入暂存区，成功后一次性原子发布；
  失败则目标库保持原状。

## 结构

- `cdc/events.py` — `ChangeEvent`（txn_id / table / pk / before / after / commit_seq）
- `cdc/fixture.py` — 本地夹具数据源：版本化行 + 仅追加日志 + 一致性快照
- `cdc/replicator.py` — 变更缓存、水位过滤、事务去重、按提交序号回放
- `cdc/store.py` — 目标表存储与原子发布
- `demo.py` — 运行样例
- `test_replicator.py` — unittest 测试

## 运行

```sh
python3.11 demo.py
python3.11 -m unittest -v
```
