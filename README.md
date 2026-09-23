# CDC 双副本对账工具

比较两个 CDC 副本在**共同水位**下的实体摘要，定位缺事件、顺序冲突与值分歧。
变更数据来自本地夹具文件（JSON），每条事件含事务 ID、表、主键、前后值与提交序号，不依赖消息平台。

## 规则

- 共同水位 = 两副本已应用提交序号进度的较小值，只比较水位（含）以下的事件；
- 一方落后时，水位之上的事件只报"未追平"，不产生任何数据错误误报；
- 缺事件：水位内一方有而另一方没有的事件，按实体单列；
- 顺序冲突：同一实体事件集合相同但应用顺序不同；
- 值分歧：同一实体水位内最终值不同；
- 最终值相同但缺事件：单独归类（`same_value_missing_events`），不算值分歧；
- 共同水位只允许前进，回退抛 `CommonWatermarkRollbackError`。

## 运行

```bash
python3 -m cdc_reconcile fixtures/replica_a.json fixtures/replica_b.json
```

## 测试

```bash
python3 -m unittest discover -s tests -v
```

要求 Python 3.11+，仅使用标准库。
