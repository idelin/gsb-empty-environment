# CDC 处理器差分测试框架

纯 Python 3.11 标准库 + unittest, 不连接任何消息平台。变更事件来自本地夹具
(`cdc/fixtures.py`), 每条 Change 携带事务ID、表、主键、前/后值、事务内序号,
每个 Commit 携带全局提交序号 commit_seq。

## 组成

- `cdc/generator.py` — 确定性测试流生成器: 由 seed 生成含事务、重复投递、
  事务内乱序、崩溃点(崩溃后整体重放在途事务)的事件流; 同 seed 必同流。
- `cdc/processor.py` — 被测的增量物化处理器 `IncrementalProcessor`
  (乱序缓冲、重复去重、崩溃恢复); 另附故意写错的
  `ArrivalOrderProcessor` 用于演示失败与缩减。
- `cdc/oracle.py` — 独立 oracle: 对整条流一次性重算最终全量状态,
  不 import、不调用被测增量算法(有源码级测试保证)。
- `cdc/harness.py` — 差分判定, 四类结果严格分开:
  `ok` / `mismatch` / `invalid_stream`(流本身无效) /
  `processor_error`(流有效但处理器报错)。
- `cdc/shrinker.py` — 自动缩减: 只做结构保持变换(整事务删除并重编号
  commit_seq、删崩溃点、去重、恢复顺序、删单个 Change 并重编号 seq),
  每个候选重新过完整判定, 因此缩减结果始终有效、可重放。

## 运行

```bash
python3 demo.py          # 演示: 生成 -> 差分 -> 注入缺陷 -> 自动缩减 -> 错误分类
python3 -m unittest discover -s tests -v
```

## 运行样例 (`python3 demo.py`)

```
================================================================
[1] 生成确定性测试流 (seed=0)
    事务 10 个, 事件 53 条, 重复 7 条, 乱序事务 2 个, 崩溃点 2 个
    流校验: 通过 (结构有效)
================================================================
[2] 被测处理器 vs 独立 oracle (最终全量状态)
    结果: ok
    最终状态: 8 行
      orders/o1 = {'amount': 460, 'status': 'shipped'}
      orders/o2 = {'amount': 17, 'status': 'shipped'}
      orders/o3 = {'amount': 223, 'status': 'paid'}
      orders/o5 = {'amount': 163, 'status': 'shipped'}
      orders/o6 = {'amount': 487, 'status': 'paid'}
      users/u4 = {'balance': 504, 'tier': 'vip'}
      users/u5 = {'balance': 955, 'tier': 'pro'}
      users/u6 = {'balance': 537, 'tier': 'pro'}
================================================================
[3] 注入缺陷实现 (按到达顺序应用, 忽略 seq 乱序)
    结果: mismatch
    差异 orders/o3: 期望 {'amount': 223, 'status': 'paid'}, 实际 None
================================================================
[4] 自动缩减失败序列 (保持事务结构可重放)
    53 条事件 -> 4 条事件
    缩减后流校验: 通过 (事务结构完整, 可重放)
      Begin(txn_id=1)
      Change(txn_id=1, seq=1, table='orders', pk='o3', before=None, after=(('amount', 223), ('status', 'paid')))
      Change(txn_id=1, seq=0, table='orders', pk='o3', before=(('amount', 72), ('status', 'new')), after=None)
      Commit(txn_id=1, commit_seq=0)
    缩减后复测: mismatch (仍复现失败)
================================================================
[5] 无效事件流与处理错误分开归类
    未知表事件        -> invalid_stream: 事件[1]: 未知表 '不存在的表'
    同 seq 内容冲突   -> processor_error: 事务 0 seq=0 内容冲突: ...
```

缩减出的最小反例精确指向缺陷: 同一事务对同一主键的两个 Change 乱序到达
(seq=1 先于 seq=0), 按到达顺序应用的实现把终值覆盖错了。

## 测试结果 (`python3 -m unittest discover -s tests`)

```
.............................
----------------------------------------------------------------------
Ran 29 tests in 0.029s

OK
```

覆盖: 生成器确定性与扰动存在性、100 个 seed 下处理器与 oracle 一致、
崩溃恢复重放、事务内乱序缓冲、重复事件幂等、ProcessorError 各语义违规、
invalid_stream 与 processor_error 分类、oracle 独立性(源码不引用被测实现)、
缩减后仍失败/更小/结构有效可重放/对事务删除 1-极小。
