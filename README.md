# 一次性口令时钟漂移

认证服务容忍设备时钟漂移，但同一个时间步的口令不能被不同允许窗口重复接受。实现供Go登录宿主调用的TOTP验证包，用标准库HMAC，给定时间步和前后窗口逐项匹配，成功原子记录实际匹配步；多个匹配步按离当前最近再较早者选择。用固定向量、时钟回拨及并发登录验证防重放。

## totp 子包

`totp/` 基于标准库 `crypto/hmac`（HMAC-SHA1，RFC 6238 动态截断）实现带防重放的验证器，导入路径为 `example.org/netsec/q0198/totp`。每个 `Verifier` 绑定一个共享密钥，内部用互斥锁记录最近接受的时间步，同一时间步的口令不会因再次落入允许窗口而被重复接受。

```go
v := totp.NewDefault(secret) // 30 秒步长、6 位口令、前后各 1 步窗口
step, err := v.Verify(passcode, time.Now().Unix())
switch {
case err == nil:
    // 登录成功，step 为实际匹配并已记录的时间步
case errors.Is(err, totp.ErrReplay):
    // 匹配到的时间步已被消费（含时钟回拨、跨窗口重放）
case errors.Is(err, totp.ErrInvalidPasscode):
    // 窗口内无匹配
}
```

选择与防重放规则：

- 窗口由 `Config.Before`/`Config.After` 指定，逐个时间步重算口令并用 `hmac.Equal` 常量时间比较。
- 多个匹配步先按距当前步的距离升序选择，距离相同取较早（计数较小）者，见 `chooseStep`。
- 选中步必须严格大于已记录的最近接受步，否则返回 `ErrReplay` 且不更新状态；通过 `LastStep`、`Reset` 可观测或重置。

`go test -race ./...` 覆盖 RFC 6238 固定向量、窗口边界、跨窗口与时钟回拨重放、多匹配步选择及 200 协程并发登录（仅一次成功，其余为 `ErrReplay`）。
