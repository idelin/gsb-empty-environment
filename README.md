# Counterfactual Intervention Engine

在不改变基线的前提下，对事实库做临时干预（移除/替换事实），重新推导目标并输出变化的支持链。纯 Python 3.11 标准库实现，测试使用 unittest。所有样例数据均为合成数据。

## 设计

- `counterfactual/core.py`
  - `Fact` / `Rule` / `Step`：不可变对象，均带稳定 ID 与 `version`；派生事实与步骤的 ID 由内容哈希确定，天然稳定。
  - `derive()`：确定性前链推导至不动点，产出 `Derivation`（可重放证据）。
  - `Derivation.support_chain(key)`：从目标事实回溯，输出有序步骤与源事实。
  - `Derivation.summary()` / `replay(rules)`：内容哈希摘要；用记录的源事实重跑并比对步骤序列与摘要，验证证据可重放。
  - `Engine`：持有不可变基线；干预永不修改基线。
- `counterfactual/intervention.py`
  - `RemoveFact` / `ReplaceFact` 操作；`Intervention` 在构造时**预先拒绝**互相矛盾的操作（同一事实被替换为两个不同值、同时移除又替换、重复移除）。
  - `Intervention.apply()` 原子执行：先整体校验（未知事实、替换保留旧值的 no-op 均拒绝），再构建全新事实元组，任何失败都不会部分生效。
  - 替换通过 `Fact.replaced()` 完成：同一事实 ID、版本 +1、旧值被丢弃，不会同时保留旧值。
  - `InterventionSession`（上下文管理器）：每个会话在独立的事实副本上推导，多个干预彼此原子隔离；退出时校验基线摘要不变。

## 运行

```sh
python3.11 run_sample.py            # 合成样例：基线 + 3 个干预 + 矛盾拒绝
python3.11 -m unittest discover -s tests -v
```
