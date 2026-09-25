# factver: versioned facts, precise invalidation, replayable evidence

纯 Python 3.11 标准库实现，测试基于 unittest，示例数据全部为合成数据。

## 模型

- 事实 (Fact)：稳定 fact_id + 单调递增 version。每个版本是不可变快照
  (FactSnapshot)，旧版本永久保留；每个字段值携带来源 FieldValue(value, source)。
  备注 (remark) 是与数据字段分离的元数据通道。
- 规则 (Rule)：稳定 rule_id + version，全部版本保留在 RuleRegistry，
  供证据重放时按录制版本取用。
- 步骤 (DerivationStep)：稳定 step_id（规则 + 输入绑定）+ 递增 version，
  记录规则版本、输入事实版本、实际读取集合 reads、输出与证据哈希。

## 关键语义

- 字段级精确失效：规则通过 FactView 读取，每次读取记录为
  FieldRead(fact_id, version, kind, field)。更正后，只有读取集合与变更字段
  相交的步骤才失效。
- 通配读取：FactView.all() 记录 field="*"，视为依赖该事实的全部数据字段。
- 备注隔离：改备注只使读取该备注的步骤失效，仅读数值字段的规则不受影响。
- 存在性条件：has(field) 同样记录对该字段的读取，删除字段会使其失效。
- 原子批量更正：correct_batch 先校验全部操作并构建新快照，再统一提交；
  任一操作非法则整批拒绝，所有事实保持原版本。
- 可重放证据：evidence_hash = sha256(规则版本, 输入事实版本, 读取集合, 输出)。
  Engine.replay 用录制的规则版本与历史事实快照重算并比对，篡改输出或读取
  集合都会被检出。

## 运行

    python3.11 demo.py                           # 运行合成数据样例
    python3.11 -m unittest discover -s tests -v  # 运行测试

## 文件

- factver/model.py — FieldValue / ChangeSet / FactSnapshot
- factver/store.py — FactStore：版本历史、更正、原子批量
- factver/rules.py — FactView（读取追踪）、RuleRegistry
- factver/engine.py — Engine：推导、失效判定、证据重放
- demo.py — 可运行样例（合成订单/客户数据）
- tests/test_factver.py — 18 个 unittest 用例

## 样例输出（节选）

    == after qty correction: only qty readers + wildcard go stale ==
      line-total[order=order-1001]     v1  STALE  output=59.7
      gift-check[order=order-1001]     v1  valid  output={'has_gift': True}
      order-digest[order=order-1001]   v1  STALE  output={'qty': 3, ...}
      remark-length[order=order-1001]  v1  valid  output=20
      shipping[customer=customer-77]   v1  valid  output=free

    -- batch correction with one bad op (delete missing field on customer)
       batch rejected: fact 'customer-77' has no field 'no-such-field'
       versions before=(4, 1) after=(4, 1) -> originals kept: True

    -- replay evidence of line-total step v1 (recorded before corrections)
       ok=True detail='replayed OK'

## 测试结果

    Ran 18 tests in 0.002s
    OK
