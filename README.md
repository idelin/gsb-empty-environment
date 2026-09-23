# CDC 墓碑回收模拟器

纯 Python 3.11+ 标准库实现（无消息平台依赖），演示变更数据捕获（CDC）场景下
**墓碑（tombstone）的安全回收**：仅当所有相关下游水位越过删除点、且无任何旧快照
引用时才回收；成员变更为显式操作，回收评估会输出具体阻塞原因。

## 语义规则

- 变更来自本地 JSONL 夹具，每条含 `tx_id`、`table`、`pk`、`before`、`after`、`commit_seq`；
  操作类型由前/后值推导（before=null→insert，after=null→delete，两者皆有→update）。
- 删除在物化存储中留下墓碑，记录删除点 `delete_seq`。
- 墓碑可回收 ⟺ 所有已注册消费者的 `watermark >= delete_seq`
  且所有打开快照的 `seq >= delete_seq`；否则逐条输出阻塞原因。
- 消费者注册/注销、快照打开/关闭均为显式调用；水位只能单调推进。
- 存在落后消费者（watermark 未越过删除点）时不得回收。
- 回收后推进 `reclaimed_horizon`；新消费者/新快照必须从
  `seq >= reclaimed_horizon` 的安全快照开始，否则拒绝。
- 迟到旧事件不得复活已删除实体：
  - `commit_seq <= 墓碑删除点` → `blocked_by_tombstone`；
  - 墓碑已回收且 `commit_seq <= reclaimed_horizon` → `below_horizon`；
  - 不晚于当前版本的乱序事件 → `stale`。
  更晚的插入（seq 大于删除点）是新一世代，正常生效并取代旧墓碑。

## 结构

```
cdc_gc/
  events.py    # ChangeEvent 模型、校验、JSONL 夹具加载
  store.py     # MaterializedStore：物化状态 + 墓碑 + 乱序/迟到防护
  group.py     # ConsumerGroup：显式成员变更、水位、快照、安全起点
  gc.py        # GarbageCollector：回收评估（含阻塞原因）与清扫
  fixtures/    # changes.jsonl（主流）/ late_changes.jsonl（迟到事件）
main.py        # 端到端运行样例
tests/         # unittest 测试（22 例）
```

## 运行

```bash
python3 main.py                    # 运行样例
python3 -m unittest discover -s tests -v   # 运行测试
```

## 运行样例（python3 main.py）

```
== 1. 应用本地夹具变更 ==
  seq=1 insert users/u-1 -> applied
  seq=2 insert users/u-2 -> applied
  seq=3 update users/u-1 -> applied
  seq=4 delete users/u-1 -> deleted
  seq=5 delete users/u-2 -> deleted

== 2. 显式成员变更：注册消费者并打开快照 ==
  消费者: analytics@0, audit@0, experimental@0; 快照: snap-report@3
  推进水位: analytics->5, audit->2 (audit 落后)

== 3. 落后消费者与旧快照阻塞回收 ==
  [阻塞]   users/u-1 删除点=4
    - 消费者 'audit' 水位 2 未越过删除点 4
    - 消费者 'experimental' 水位 0 未越过删除点 4
    - 快照 'snap-report' 引用 seq 3 早于删除点 4
  [阻塞]   users/u-2 删除点=5
    - 消费者 'audit' 水位 2 未越过删除点 5
    - 消费者 'experimental' 水位 0 未越过删除点 5
    - 快照 'snap-report' 引用 seq 3 早于删除点 5

  显式注销消费者 'experimental'
  推进水位: audit->5

== 4. 消费者均已越过删除点，快照仍阻塞 ==
  [阻塞]   users/u-1 删除点=4
    - 快照 'snap-report' 引用 seq 3 早于删除点 4
  [阻塞]   users/u-2 删除点=5
    - 快照 'snap-report' 引用 seq 3 早于删除点 5

  关闭快照 'snap-report'

== 5. 回收墓碑 ==
  [已回收] users/u-1 删除点=4 tx=tx-4
  [已回收] users/u-2 删除点=5 tx=tx-5
  回收水位 reclaimed_horizon = 5

== 6. 迟到旧插入不得复活已删除实体 ==
  seq=2 insert users/u-2 -> below_horizon (seq 2 <= 已回收水位 5, 不可补写)
  seq=3 insert users/u-2 -> below_horizon (seq 3 <= 已回收水位 5, 不可补写)
  users/u-2 当前值: None (仍为已删除)

== 7. 新消费者必须从安全快照开始 ==
  [拒绝] 消费者 'bi-new' 起点 seq 2 低于安全快照下限 5: 必须从 seq >= 5 的快照开始
  [接受] 'bi-new' 从安全快照 seq=5 开始

== 8. 最终状态 ==
  存活行: users/u-1=None, users/u-2=None
  待回收墓碑: []
```

## 测试结果（python3 -m unittest discover -s tests）

```
Ran 22 tests in 0.002s

OK
```

覆盖：事件解析与校验、乱序/迟到事件防护、删除先到时墓碑拦截迟到插入、
新世代插入取代墓碑、显式成员变更与水位单调性、安全快照下限、
落后消费者/旧快照阻塞原因输出、注销解除阻塞、按删除点部分回收、
回收后迟到插入不复活的全流程。
