# cdcstream — 确定性 CDC 测试流工具包

用本地夹具生成**确定性**的变更数据（CDC）测试流，包含事务、重复投递、
乱序投递和崩溃点；用一个**独立的全量状态 oracle** 验证增量处理器的最终
状态；无效事件流与处理错误严格分开；失败序列可**自动缩减**，且缩减后仍
保持事务结构完整、可重放。

仅使用 Python 3.11 标准库 + unittest，不接任何消息平台。

## 结构

```
fixtures/changes.json    本地变更夹具：tx_id、表、主键、前后值、提交序号
cdcstream/
  model.py               ChangeEvent / CrashPoint / 投递流模型
  fixtures.py            夹具加载，填充 tx_size 使流自描述
  generator.py           确定性流生成器（重复/乱序/崩溃点 + 无效流变体）
  processor.py           被测增量处理器（含演示用缺陷变体）
  oracle.py              独立全量状态 oracle（不调用被测增量算法）
  validator.py           验证器：ok / invalid_stream / processing_error / mismatch
  shrinker.py            保事务结构的 ddmin 自动缩减器
run_demo.py              端到端演示
tests/                   unittest 测试套件（19 个用例）
```

## 关键设计

- **自描述的流**：每个事件携带 `tx_size`（本事务事件数），流的良构性
  （事务完整性、commit_seq 唯一、每主键 before/after 链连续、重复投递
  内容一致）可仅凭流自身校验，无需外部目录。
- **独立 oracle**：`oracle.full_state` 对逻辑事件集做一次全量归并
  （每主键取最大 commit_seq），不 import、不调用被测增量处理器；
  有 AST 级测试强制这一独立性。
- **错误分类**：`run_and_validate` 先校验流（`invalid_stream`，处理器
  根本不会被执行），再运行处理器（异常 → `processing_error`），最后
  与 oracle 比对（不一致 → `mismatch`，附 missing/unexpected/differing
  明细）。
- **崩溃语义**：`CrashPoint` 只清空处理器的易失去重缓存，持久化状态
  （含删除墓碑）保留；崩溃后事件整体重投，处理器必须幂等。
- **缩减器**：按 事务 → 崩溃点 → 重复投递 三个阶段做 ddmin；事件只按
  整个事务移除，候选流必须通过良构校验且仍复现同类失败，因此结果始终
  可重放。

## 运行

```bash
python3.11 run_demo.py                  # 端到端演示
python3.11 -m unittest discover -s tests -v   # 测试
```

## 运行样例（`python3.11 run_demo.py`）

```
Loaded 8 changes from changes.json (4 transactions)
Oracle full state: {('accounts', 'alice'): {'balance': 120}, ('orders', 'o1'): {'status': 'shipped'}}

Generated delivery stream (seed=7): 38 items (8 logical events, duplicates + reorder + crashes)

=== 1. Correct incremental processor ===
status: ok

=== 2. Buggy arrival-order processor (expect mismatch) ===
status: mismatch (0 missing, 0 unexpected, 1 differing keys)
  ('accounts', 'alice'): processor={'balance': 80} oracle={'balance': 120}

=== 3. Invalid stream is reported separately ===
status: invalid_stream (tx 1: partial transaction (1 of 2 events))

=== 4. Automatic shrinking of the failing stream ===
shrunk: 38 -> 4 items
  ev#4 tx=2 seq=4 orders/o1 None -> {'status': 'new'}
  ev#6 tx=3 seq=6 orders/o1 {'status': 'new'} -> {'status': 'paid'}
  ev#5 tx=3 seq=5 accounts/alice {'balance': 80} -> {'balance': 120}
  ev#3 tx=2 seq=3 accounts/alice {'balance': 100} -> {'balance': 80}
re-validation: mismatch (still failing, transaction structure replayable)
```

缩减结果即最小复现：两个完整事务（tx2、tx3），同一主键的 seq 5 先于
seq 3 到达 —— 正好暴露"按到达顺序覆盖"缺陷，且事务结构完整可重放。

## 测试结果（`python3.11 -m unittest discover -s tests`）

```
Ran 19 tests in 0.006s

OK
```

覆盖：夹具加载与事务分组、生成器确定性/重复/乱序/崩溃点/逻辑内容保持、
三类无效流识别、oracle 手算样例与独立性、增量处理器跨 25 个种子与
oracle 一致、崩溃后重投幂等、验证器四类结果分流、缩减后仍失败且
良构/事务 1-最小、正常流不触发缩减。
