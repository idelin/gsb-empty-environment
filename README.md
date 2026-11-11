# 证书吊销响应绑定

TLS接入服务缓存吊销结论时曾把同序列号的另一签发者结果复用。写可嵌入Go服务的状态缓存包，键绑定签发者公钥摘要、证书序列号和响应者身份；验签由注入回调执行，状态有效期为thisUpdate到nextUpdate，未知不得当作有效。并发刷新合并，但每个等待者独立取消；撤销结论在有效期内不得被更旧的良好结论覆盖。用可控时钟测试乱序刷新与同序列号不同CA。

## 实现

`revcache` 子包（`example.org/netsec/uid1261/revcache`）提供可嵌入的吊销状态缓存：

- `Key` 绑定签发者公钥摘要、证书序列号、响应者身份三元组，同序列号不同 CA 不复用结论。
- `VerifyFunc` 由调用方注入，负责获取并验签响应；缓存本身不做密码学验签。
- 有效期为 `[thisUpdate, nextUpdate)` 半开区间；`StatusUnknown` 与验签错误一律不缓存、不当作有效。
- 同键并发刷新合并为一次 `VerifyFunc` 调用，每个 `Get` 等待者用自己的 `context` 独立取消。
- 有效期内的 `revoked` 结论不会被 `thisUpdate` 更旧的 `good` 结论覆盖。
- `Clock` 接口可注入可控时钟；测试用 `fakeClock` 覆盖乱序刷新与同序列号不同 CA 场景。

运行测试：`go test -race ./...`
