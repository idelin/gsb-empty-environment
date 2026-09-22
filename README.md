# fsm — 显式状态转移表 + 增量帧解析会话运行时

纯 Go 标准库实现（兼容 Go 1.11+），无第三方依赖。

## 设计

- `machine.go` — 显式转移表（`Row{From, On, To, Action}`）经 `Compile` 校验编译为不可变
  `Machine`：拒绝空/未声明初态、重复状态、引用未声明状态、重复 `(From, On)` 行。
  `Step` 先执行动作，仅当动作成功才同时提交输出与状态变更（原子执行）；动作失败保持旧状态。
- `frame.go` — 线格式 `[2字节大端长度][1字节类型][body]`，`DATA` 帧 body 前缀 4 字节序号。
  `Parser` 支持任意分块输入；`ErrNeedMoreData`（缺数据，可续传）与 `*BadFrameError`
  （坏数据，应断流）严格区分；长度额度在分配/切片 body 之前检查。
- `session.go` — `Session` 组合解析器与状态机：
  - 握手未完成时业务帧（PING/DATA/BYE）无表项命中，返回 `*NoTransitionError`，状态不变；
  - 未知类型字节、空闲超时（`Tick` 注入）、握手重入分别映射为独立事件
    `UNKNOWN` / `TIMEOUT` / `REENTER`；动作执行期间的重入调用被 `ErrReentrantCall` 拒绝；
  - 去重规则（显式）：`DATA` 序号 ≤ 最后一个**已应用**序号的帧视为重放，
    返回缓存输出，动作不重跑、状态不变更；序号不要求连续；失败的序号未应用，可重试。

## 运行

```sh
go test ./...        # 测试
go run ./cmd/demo    # 样例
```

## 样例输出（go run ./cmd/demo）

```
== 1. business frame before handshake is rejected ==
DATA seq=1 (pre-handshake)   -> REJECTED: fsm: no transition for ("INIT", "DATA") (state stays INIT)
== 2. chunked byte input: HELLO+PING fed in 1-byte pieces ==
byte 2 completes frame       -> output WELCOME, state=ESTABLISHED
byte 5 completes frame       -> output PONG, state=ESTABLISHED
== 3. DATA applies, replay is deduplicated ==
DATA seq=1 "hello"           -> output ECHO:HELLO, state=ESTABLISHED
DATA seq=1 replay            -> DEDUP replay, cached output ECHO:HELLO (state stays ESTABLISHED)
DATA seq=2 "world"           -> output ECHO:WORLD, state=ESTABLISHED
== 4. action failure keeps old state ==
DATA seq=3 "fail"            -> REJECTED: action: simulated failure (state stays ESTABLISHED)
== 5. unknown type, re-entry, timeout are independent events ==
type 0x7f                    -> output ERR: unknown type 0x7f, state=ESTABLISHED
HELLO again                  -> output ERR: already handshaked, state=ESTABLISHED
Tick after idle              -> output IDLE-TIMEOUT, state=CLOSED
== 6. length quota checked before allocation ==
len=0xffff, quota=64         -> frame: bad frame: declared length 65535 exceeds quota 64 (need-more? false)
```

## 测试结果（go test ./...）

```
=== RUN   TestEncodeDecodeRoundTrip                  --- PASS
=== RUN   TestParserArbitraryChunking                --- PASS  (1/2/3/5/n-1/n 字节分块)
=== RUN   TestParserDistinguishesNeedMoreFromBad     --- PASS
=== RUN   TestParserLengthQuotaCheckedBeforeAllocation --- PASS
=== RUN   TestParserRejectsZeroLengthAndShortData    --- PASS
=== RUN   TestCompileValid                           --- PASS
=== RUN   TestCompileRejectsBadTables                --- PASS  (7 类非法表)
=== RUN   TestStepCommitsOutputAndStateAtomically    --- PASS
=== RUN   TestStepFailureKeepsOldState               --- PASS
=== RUN   TestStepNoTransition                       --- PASS
=== RUN   TestHandshakeGatesBusinessFrames           --- PASS
=== RUN   TestAtomicCommitAndFailureKeepsState       --- PASS
=== RUN   TestReplayFollowsDedupRule                 --- PASS
=== RUN   TestUnknownTypeIsIndependentEvent          --- PASS
=== RUN   TestHandshakeReentryIsIndependentEvent     --- PASS
=== RUN   TestTimeoutIsIndependentEvent              --- PASS
=== RUN   TestReentrantHandleRejected                --- PASS
=== RUN   TestSessionNextOverChunkedBytes            --- PASS
PASS
ok  	fsm	0.882s
```
