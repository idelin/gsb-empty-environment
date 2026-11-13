# Critical-CH重试与缓存键

服务端用 Critical-CH 要求客户端带上某些提示后重发请求，攻击者可诱使客户端无限重试，或让缓存把带不同提示的响应混在一起。为Go反向代理写供缓存与重试逻辑调用的 Critical-CH 处理包：响应头 Critical-CH 的每个字段必须同时出现在 Accept-CH 中，否则忽略该字段；仅当请求原本没带该提示且客户端能发送时才重试，同一次导航最多重试一次；缓存键要把响应 Vary 列出的提示头的值纳入；响应依赖了 Critical-CH 列出的提示却没有在 Vary 里列出时，该响应不得进入共享缓存；重试请求的结果不得覆盖首次响应的缓存条目。用不在 Accept-CH 中的 Critical-CH、第二次要求重试、Vary 缺失和不同提示值的缓存分离验证。

## `criticalch` 包

实现位于 `criticalch/`，供反向代理的重试与共享缓存逻辑调用。

- `EffectiveCriticalHints` / `Evaluate`：解析 `Accept-CH` 与 `Critical-CH`，不在
  `Accept-CH` 中的 Critical-CH 字段一律忽略（既不重试也不影响可缓存性）。
  `Decision.Retry` 仅在请求原本未携带该提示、客户端能发送（`ClientHints`）且本次
  导航尚未重试过（`WithRetryMarker` / `AlreadyRetried`）时为真。
- `BuildRetryRequest`：构造唯一一次重试请求，附加上提示并写入重试标记；
  `RoundTrip` 封装“首响 + 至多一次重试”的完整往返。
- `VaryKey` / `CacheKey`：把响应 `Vary` 列出的请求头值编码进缓存键，区分“未携带”
  与“携带空值”，并采用长度前缀避免键碰撞；`Vary: *` 纳入全部请求头。
- `SharedCacheable`：响应依赖的 Critical-CH 提示未出现在 `Vary` 中时返回 false。
- `Cache.Store`：拒绝不可共享缓存的响应（`ErrNotCacheable`）；重试响应
  （`retried=true`）遇已有条目返回 `ErrEntryExists`，绝不覆盖首次响应。

验证见 `criticalch/criticalch_test.go`：`go test ./...`（含竞态检测
`go test -race`）覆盖四类要求场景——未在 Accept-CH 中的 Critical-CH、第二次重试
  被抑制、Vary 缺失导致不可缓存、不同提示值的缓存键/条目分离，以及重试结果不覆盖
  首次条目。
