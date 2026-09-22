# 有界管线组合的过滤/转导自动机

本项目仅使用 Python 3.11 标准库实现，不依赖正则表达式或第三方自动机库。每个自动机的状态、字母表、转移、接受状态、结束输出和停止状态都由调用方显式提供。

## 文件

- `src/automata.py`：FST、纯组合参考语义、有界通道、背压和级联取消。
- `src/samples.py`：三个串行自动机以及下游停止示例。
- `src/demo.py`：整串/任意分块一致性和停止取消演示。
- `src/counterexamples.py`：可独立执行的性质检查；若出现反例会非零退出。
- `test_pipeline.py`：`unittest` 测试。

## 语义

- 一条转移是 `(下一状态, 输出元组)`；输出元组按 FIFO 顺序发布。
- 输出元组中的 `EPSILON` 会被过滤，不进入下一级队列。
- 到达输入结束时，仅接受状态执行 `finish_outputs`；非接受状态报错。
- `halting` 状态可在发布当前输出后立即停止下游读取并取消上游。
- 每一级之间是固定容量通道；队列满时上游阻塞，因此中间缓冲有硬上界。
- 输出消费者可抛出 `StopConsumption`，关闭最终读端并逐级取消。
- 输入分块只决定喂入节奏；第一级的字符串分块会拆成单字符，上游输出给下一级时始终是原子符号。

顶层若要把一个 tuple 本身当作单个符号，请把它作为一个分块传入，例如 `[[("A1", "B1")]]`。

## 运行

```sh
python3.11 src/demo.py
python3.11 src/counterexamples.py
python3.11 -m unittest -v
```

## 最近一次验证结果

`python3.11 -m unittest -v`：

```text
Ran 13 tests in 1.749s

OK
```

`python3.11 src/counterexamples.py`：

```text
ok - assert_chunk_invariance
ok - assert_multi_output_order
ok - assert_buffer_capacity
ok - assert_consumer_cancel
ok - assert_invalid_definitions_fail
```

演示对 `['abx']` 与 `['a', 'bx', '', '']` 产生相同输出：

```text
outputs:       ('01', '|', '00', '|', 'END')
peak buffers:  (1, 1, 1, 1)
stopped:       True
```
