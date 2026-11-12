# HTTP缓存新鲜度年龄

安全代理的认证结果缓存不能因多跳Age值异常而延长有效期。实现供Go缓存宿主调用的新鲜度计算包，输入请求发送、响应接收、Date、Age及最大寿命，按非负时差求修正年龄，所有算术饱和并允许注入时钟；时钟回拨不能降低已记录年龄。用时间轴穷举测试响应延迟、缺失Date与Age溢出。

## 包 `freshness`

位于 `freshness/`，仅依赖标准库，按 RFC 9111 的 corrected age 模型计算：

```
apparent_age = max(0, received_at - date)
corrected    = max(apparent_age, age_value + max(0, received_at - sent_at))
```

- `NewEntry(clock, Params)` 记录响应并得到修正初始年龄，`Age/Remaining/Fresh` 供宿主后续判定。
- `Clock` 接口与 `ClockFunc` 支持注入时钟；传 `nil` 使用 `time.Now`。
- 所有时差按非负截断，加法/减法均为饱和运算；`Age` 达到或超过 `2^31` 秒按 RFC 上限处理，直接判为过期。
- 驻留时间逐次累计并对负值归零，时钟回拨不会降低已记录年龄；`Entry` 方法并发安全。
- 缺失 `Date`（或零值时间）按表观年龄 0 处理，多跳 `Age` 与响应延迟仍计入，不会因此延长有效期。

## 测试

```
go test ./... -race -count=1
go test -cover ./freshness/
```

`TestExhaustiveTimeline` 对请求延迟、Date 偏移、Age 取值做笛卡尔组合，并沿含时钟回拨与重复观测的时间轴逐步断言；另有响应延迟、缺失 Date、Age 溢出、时钟回拨、饱和算术与并发读写的聚焦用例，语句覆盖率 100%。
