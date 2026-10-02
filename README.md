# eventlimiter — 事件驱动精确速率限制桶

精确滑动窗口限流器（Go 标准库实现）：

- 完整保留按**事件发生时间**排序的历史请求时间戳；
- 按事件时间（而非物理到达时间）判定限流：任意 10 秒窗口内最多接受 3 个请求（规则可配置）；
- 迟到请求到达时，将其按事件时间插入历史并**重新评估**所有后续请求的合法性，对判定发生翻转的请求生成**撤销/纠正通知**（`Correction`）。

## 运行样例

```sh
go run ./cmd/demo
```

输出（验收场景：t=1,2,8 通过；迟到的 t=0 到达后，t=0,1,2 占满 3 个配额，t=8 被撤销）：

```
rule: at most 3 requests per 10s (event-time sliding window)
arrive req-1    event t=1 s -> ACCEPTED
arrive req-2    event t=2 s -> ACCEPTED
arrive req-8    event t=8 s -> ACCEPTED
-- late request with event time t=0 arrives --
arrive req-0    event t=0 s -> ACCEPTED
    !! correction: request "req-8" (t=00:00:08) ACCEPTED -> REJECTED (late arrival reshaped window: 3/3 slots taken in preceding 10s)
final history (ordered by event time):
    req-0    t=0 s ACCEPTED
    req-1    t=1 s ACCEPTED
    req-2    t=2 s ACCEPTED
    req-8    t=8 s REJECTED
```

## 测试

```sh
go test ./... -v
```

覆盖：验收边界（迟到撤销）、精确窗口边界（t=10 时 t=0 过期）、历史有序性、按事件时间判定、撤销可被更晚到达的事件再次翻转。

## API

- `New(max int, window time.Duration) *Limiter` — 每 `window` 事件时间最多 `max` 个请求。
- `Submit(id string, t time.Time) (Verdict, []Correction)` — 提交事件时间为 `t` 的请求，返回本次判定及被翻转的历史请求的纠正通知。
- `VerdictOf(id string) (Verdict, bool)` / `History() []Entry` — 查询当前判定与有序历史。
