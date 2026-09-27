# 队列并发历史的线性化检查器

针对小型并发队列操作历史，寻找符合**实时关系**（real-time order）的顺序解释。
Python 3.11，仅使用标准库与 unittest。

## 模型与判定

- 验证对象：给定有限状态模型 `BoundedQueue(capacity)`（有界 FIFO 队列），
  调用为 `("enq", v)` / `("deq",)`，响应为 `("ok", v)` 或异常 `("exc", "Full" | "Empty")`。
- **实时序**：操作 i 的响应时间不晚于 j 的调用时间（`t1_i <= t0_j`）时，i 必须排在 j 前；
  重叠操作（后发先返）允许重排，检查器枚举所有合法交错，**不按完成时间排序**。
- **成功出队值必须来自合法入队**：模型状态转移保证 `deq` 只能返回队头元素。
- **未完成调用 vs 异常返回**：pending 调用（`t1 is None`）可生效（取模型允许的任意响应）
  或被丢弃；异常返回是已完成的调用，必须精确匹配模型异常。
- **未决**：搜索配置数超过 `max_states` 预算时返回 `UNKNOWN`，不妄下结论。

## 输出

- `LINEARIZABLE`：输出具体顺序见证（含 pending 生效标记）。
- `NOT_LINEARIZABLE`：`minimize_counterexample` 给出 1-最小反例
  （值闭合子历史意义下最小，避免退化为单个无源出队），并可序列化为 JSON 供 `replay` 重放。
- `UNKNOWN`：预算耗尽，保持未决。

## 文件

- `linearizability.py`：检查器、最小化器、JSON 重放、演示（`python3.11 linearizability.py`）。
- `test_linearizability.py`：24 个 unittest 用例（`python3.11 -m unittest -v`）。

## 运行样例

```
$ python3.11 linearizability.py
=== sample 1: overlapping ops, legal reorder (linearizable) ===
verdict: linearizable (explored 5 states)
witness (enq B linearized before the earlier-invoked enq A):
  0: op1 enq('B') -> ok
  1: op0 enq('A') -> ok
  2: op2 deq() -> ok -> 'B'

=== sample 2: FIFO violation (not linearizable, minimal counterexample) ===
verdict: not_linearizable (explored 3 states)
minimal counterexample keeps ops [0, 1, 2] (dropped noise ops)
replayable counterexample (JSON):
{
  "model": {
    "capacity": 4,
    "type": "bounded-queue"
  },
  "ops": [
    {
      "call": [
        "enq",
        "A"
      ],
      "oid": 0,
      "response": [
        "ok",
        null
      ],
      "t0": 0,
      "t1": 1
    },
    {
      "call": [
        "enq",
        "B"
      ],
      "oid": 1,
      "response": [
        "ok",
        null
      ],
      "t0": 2,
      "t1": 3
    },
    {
      "call": [
        "deq"
      ],
      "oid": 2,
      "response": [
        "ok",
        "B"
      ],
      "t0": 4,
      "t1": 5
    }
  ]
}
replay verdict: not_linearizable

=== sample 3: pending call vs exception return ===
verdict: linearizable (explored 3 states)
  0: op0 enq('X') -> ok [pending, took effect]
  1: op1 deq() -> ok -> 'X'
  2: op2 deq() -> raise Empty

=== sample 4: search budget exhausted (verdict stays undecided) ===
verdict: unknown (explored 3 states, budget 3)
```

## 测试结果

```
$ python3.11 -m unittest -v
...
test_witness_formatting_mentions_pending_and_exceptions (test_linearizability.TestSerialization.test_witness_formatting_mentions_pending_and_exceptions) ... ok

----------------------------------------------------------------------
Ran 24 tests in 0.001s

OK
```
