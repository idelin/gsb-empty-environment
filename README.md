# 临时授权范围分裂

云数据服务要把一个宽授权拆成数个互不重叠的工作分片，分片重试不得扩大范围。实现供Go调度宿主调用的授权切分包，范围是多维整数半开盒，先减去拒绝区域，再切成指定最大体积的片；令牌绑定父授权摘要与片区，所有片并集必须精确等于可访问区域。用小网格穷举核对空洞、边界和重复分片。

## 包：`netsec`（module `example.org/netsec/q0289`）

授权几何用多维整数半开盒 `Box{Lo, Hi}` 表示，维度 `i` 覆盖 `Lo[i] <= v < Hi[i]`。

### 流水线

1. `Subtract(accessible, denied...)`：从宽授权中减去任意个拒绝半开盒，返回两两不交、非空的盒子划分。
2. `Split(boxes, maxVolume)`：递归二分最长维，直到每片整数点数（体积）不超过 `maxVolume`。
3. `Mint(key, parent, region)` / `MintShards(key, parent, maxVolume)`：用 HMAC-SHA256 签发令牌，标签同时覆盖父授权摘要与片区。

### 令牌与重试

- `ParentAuth.Digest()` 是规范化 SHA-256 摘要：拒绝盒先排序，与调用方给出的顺序无关。
- `Token.Verify` 校验父摘要与 HMAC；`Token.VerifyFor` 额外证明请求区域被令牌片区完全包含，重试只能重放原片区，扩大范围返回 `ErrRegionOutside`。
- 令牌支持 `MarshalBinary`/`UnmarshalBinary` 与 base64 文本 `MarshalText`/`UnmarshalText`，供宿主持久化与跨进程传递。

### 正确性保证（由测试穷举）

- 所有片两两不交，且并集恰好等于“宽授权减去拒绝区域”：无空洞、无重复覆盖。
- 半开边界（角点、贴边条带、完全覆盖、外部不相交）逐点枚举核对。
- 小网格随机模糊测试（1–3 维、200 组随机几何与 `maxVolume`）逐点验证划分。
- 篡改片区/摘要/标签、错误密钥、错误父授权、越界重试均被拒绝。
- `int64` 体积溢出与畸形盒子在入口处报错。

### 快速上手

```go
parent := netsec.ParentAuth{
    Accessible: netsec.Box{Lo: []int64{0, 0}, Hi: []int64{100, 100}},
    Denied:     []netsec.Box{{Lo: []int64{10, 10}, Hi: []int64{20, 20}}},
}
tokens, err := netsec.MintShards(hmacKey, parent, 1000)
// 重试时：
err = tokens[i].VerifyFor(hmacKey, parent, tokens[i].Region())
```

运行测试：`go test ./...`（含 `-race` 与可运行示例 `ExampleMintShards`）。
