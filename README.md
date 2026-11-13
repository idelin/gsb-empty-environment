# 节点密钥过期与轮换剔除

协调服务给每个节点的 WireGuard 式密钥设了过期时间，攻击者持有过期设备时，可凭缓存的网络图继续被对端接受，或拿旧轮换请求重放。给Go协调服务写供网络图生成与更新调用的密钥生命周期包，以下为本题规定：当前时间大于等于过期时间即视为过期，网络图里不再包含该节点，已发给其他节点的增量必须带删除条目；过期时间为空只在节点被管理员标记免过期时允许；轮换请求须带新节点密钥与旧密钥对新密钥的签名，签名验证由调用方注入，且只在旧密钥未过期时受理，同一旧密钥只能轮换一次；轮换后访问策略沿用但旧密钥立即从网络图删除。用过期边界、缓存图后的增量、旧轮换请求重放和过期后轮换验证。

## API

- `NewStore(Options{Now, Verify})` creates a concurrency-safe lifecycle store. Inject `Now` for deterministic expiry and `Verify` to authenticate rotation signatures.
- `AddNode` rejects a nil expiry unless `AdminExempt` is true, and rejects reused or retired keys.
- `Graph` / `GraphAt` emit only keys valid at the requested instant: `now >= ExpiresAt` is expired.
- `DeltaSince` / `DiffGraphs` compare a peer's cached graph by key, so expiry, removal, and rotation always emit `DeltaDelete` for the stale key.
- `Rotate` accepts an old key only while it is current, unexpired, unused for a prior rotation, and its injected signature check passes. The replacement keeps the access policy and expiry settings; the old key is retired immediately.

`Policy` is opaque caller data retained across rotation. Callers should replace it with `SetPolicy` rather than mutate shared values in place.
