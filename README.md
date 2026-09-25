# 可干预、可重放的确定性推理引擎

在不改变基线的前提下临时移除/替换事实，重新推导目标并输出变化支持链。
纯 Python 3.11 标准库实现，测试使用 unittest，样例数据为合成数据。

## 文件

- `engine.py` — 核心引擎：事实/规则/步骤模型、推导、干预、重放
- `demo.py` — 合成样例（费用报销审批）
- `test_engine.py` — 17 个单元测试

## 运行

```bash
python3 demo.py            # 运行样例
python3 -m unittest test_engine -v   # 运行测试
```

## 设计要点

- **稳定 ID 与版本**：`Fact(id, assertion, value, version)`、`Rule(id, version, ...)`、
  `Step(id, rule_id, rule_version, inputs=[(fact_id, version)], output)`。
- **基线不可变**：`Engine` 持有不可变基线；`intervene()` 在副本上推导，
  返回前后基线摘要（SHA-256）以证明基线未变。
- **原子隔离**：干预先整体校验再应用，任一失败则全部不生效；场景之间互不泄漏。
- **矛盾预拒**：同一事实既移除又替换、或被替换为两个不同值 → `InterventionError`；
  完全相同的重复操作幂等去重。
- **替换不留旧值**：事实按 ID 唯一存储，替换即覆盖并版本 +1。
- **证据可重放**：`replay()` 用相同基线事实与规则重新推导，逐步比对已记录步骤，
  篡改或截断会抛出 `ReplayError`。
- **确定性**：规则与事实均排序后匹配，推导结果与输入顺序无关，摘要稳定。
