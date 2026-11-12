# 签名下载链接不能因中间缓存修改Age而在到期后继续授权

签名下载链接不能因中间缓存修改Age而在到期后继续授权。写供Go资源宿主调用的验证缓存包，签名绑定对象摘要、受众和绝对截止时刻，缓存只复用验签计算结果，每次交付重新用可信时间区间检查全部可能时刻均未到期；Age和Date仅作不可信诊断。时钟区间不确定则拒绝授权，密钥撤销使已有缓存失效。用真实HMAC、回拨时钟、伪造负Age和过期边界并发请求验证缓存命中也不能绕过截止检查。

## 实现：`sigcache` 包

模块路径 `example.org/domainreissue/q016`，代码位于 `sigcache/`。

### 安全边界

- 签名使用真实 HMAC-SHA256，规范化载荷绑定 `算法 / keyID / 绝对截止 Unix 时刻 / 受众 / 对象摘要`；截止时刻是绝对值而非时长，中间缓存无法通过 `Age` 延长授权。
- 缓存只保存验签计算结果（`verifiedEntry`），不保存任何授权或到期结论。每次 `Authorize` 都重新执行受众、摘要绑定检查，并重新向可信时钟取时间区间判断到期。
- 到期判定要求可信区间的**全部**可能时刻都严格早于截止时刻：`latest.Before(expiry)`；只要区间上界触及或越过截止时刻即拒绝（`ErrExpired`）。
- `TrustedClock` 返回 `[earliest, latest]` 不确定区间；时钟报错、零值或上下界倒置时拒绝授权（`ErrIndefiniteClock`），区间跨越截止时刻按过期处理。
- HTTP `Age` 与 `Date` 头仅解析进 `Denial.Diagnostics` 供排障，从不参与决策；负 `Age`（RFC 9111 要求非负 delta-seconds）记录为诊断解析错误。
- `Keyring` 带全局密钥版本号；`Verifier.RevokeKey` 删除密钥并清空缓存，换钥也因版本号递增使旧缓存条目判定为陈旧，旧令牌需重新验签并失败。

### 验证（`sigcache/*_test.go`）

- `TestSignUsesRealHMACSHA256`：独立重算 HMAC，确认真实算法与 32 字节标签。
- `TestAuthorizeCachesSignatureOnly` / `TestRewoundClockRechecksExpiryOnCacheHit`：回拨与前拨时钟，缓存命中下仍逐次重检截止时刻。
- `TestForgedNegativeAgeCannotExtendAuthorization`：伪造负 `Age`、垃圾 `Age` 在到期前后均不影响决策，仅留诊断。
- `TestExpiryBoundaryConcurrentRequests`：32 个 goroutine × 200 次在过期边界并发请求，`-race` 下边界时刻无一放行。
- `TestIndefiniteClockIsRefused`、`TestRevocationInvalidatesCache`、`TestSignatureBindingsCannotBeSubstituted`、`TestCacheEvictionBoundsMemory`：不确定时钟、撤销/换钥失效、绑定不可替换、缓存容量上限。

### 运行

```sh
go vet ./...
go test -race -count=1 ./...
```
