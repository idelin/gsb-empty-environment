# dm-verity挂载准入

dm-verity 设备若声明的数据块数小于实际设备，或启用忽略全零块、忽略损坏，攻击者就能让一部分内容不经验证被读到。为Go挂载服务写供映射表加载前调用的准入包：超级块里的数据块数、块大小须与映射表长度逐项吻合，哈希树设备容量须容纳按层计算的哈希块数，根哈希长度等于算法摘要且签名由调用方注入验证；表参数里的 ignore_zero_blocks、ignore_corruption 一律拒绝，restart_on_corruption 允许。用块数少一、设备尾部多出数据、树设备过小、被禁参数与合规表验证。

## 实现

`netsec.Admit` 在 dm-verity 映射表加载（`DM_TABLE_LOAD`）前调用，拒绝任何
可能让未验证字节被读到的请求：

- `ParseSuperblock` 校验魔数 `verity\0\0`、版本 1 与普通哈希类型，解析算法、
  数据/哈希块大小、数据块数与盐。
- `ParseTable` 解析 v1/v2 目标表行，v2 按选项计数读取参数。
- 表与超级块逐项比对：算法、数据块大小、哈希块大小、数据块数，任一不符即拒绝。
- 数据设备容量必须恰好等于 `num_data_blocks * data_block_size`：少一块报
  `ErrDeviceTooSmall`，尾部多出字节报 `ErrTrailingData`。
- 哈希树大小按 Merkle 层逐层计算
  （每层块数 = `ceil(下层块数 * 摘要长度 / hash_block_size)`，到单根块为止），
  全部乘法/加法带溢出检查；哈希设备须容纳
  `(hash_start_block + 哈希树块数) * hash_block_size`。
- 根哈希长度必须等于算法摘要长度（sha1/sha224/sha256/sha384/sha512）。
- 表选项中的 `ignore_zero_blocks`、`ignore_corruption` 一律拒绝
  （`ErrForbiddenOption`）；`restart_on_corruption` 不削弱验证，允许使用。
- 签名验证通过调用方注入的 `SignatureVerifier` 完成，准入包不内置密钥管理；
  未注入或验证失败均拒绝。

## 用法

```go
err := netsec.Admit(&netsec.AdmissionRequest{
    Superblock: sbBytes,                       // 从哈希设备读出的 512 字节
    Table:      table,                         // netsec.ParseTable 的结果
    DataDevice: netsec.Device{Path: dataPath, Size: dataSizeBytes},
    HashDevice: netsec.Device{Path: hashPath, Size: hashSizeBytes},
    Signature:  rootHashSignature,
}, func(algorithm string, rootHash, signature []byte) error {
    return trustedKey.Verify(rootHash, signature) // 调用方自己的验签逻辑
})
if err != nil {
    // 拒绝加载映射表，可用 errors.Is 分类 ErrTableMismatch / ErrTrailingData 等
}
```

## 测试

`go test ./...` 覆盖：超级块/表各少一块、数据设备尾部多出数据、哈希设备差一字节
与差一块、两个被禁参数、`restart_on_corruption` 放行、合规表通过，以及根哈希
长度、验签失败、层大小溢出与格式错误等边界。
