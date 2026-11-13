# APK多态文件与旧签名覆盖范围

旧签名方案只校验压缩包内各条目，不覆盖文件头部的字节，攻击者把一段 dex 拼在安装包前面，让它同时是合法的 dex 和合法的 zip，签名校验通过，系统却加载攻击者的代码。给Go应用分发网关实现可导入的安装包准入包，输入文件前 8 字节、压缩包中央目录与结束记录的偏移信息、第一个本地文件头的偏移、是否存在签名分块（魔数 APK Sig Block 42）及其中的 v2、v3 签名 ID（0x7109871a、0xf05368c0）、目标最低系统版本，以及调用方对 v2 摘要范围的校验回调：文件以 dex 魔数（"dex\n"）开头且同时有合法的结束记录时判为多态并拒绝；第一个本地文件头不在偏移 0 时拒绝；最低系统版本不低于 24 却没有 v2 以上签名时拒绝，只有 v1 签名仅在低于 24 时接受；有 v2 签名时，回调须确认摘要覆盖偏移 0 到签名分块之前、中央目录与结束记录三段，缺任何一段拒绝；签名分块之后必须紧跟中央目录。用 dex 前缀拼接、本地头偏移非零、仅 v1 且最低版本 24、摘要缺中央目录和合法 v2 验证。

## 使用

包不依赖具体 ZIP 解析器。调用方把已经解析出的偏移和签名块信息填入
`netsec.Package`，再调用 `netsec.Admit`：

```go
err := netsec.Admit(netsec.Package{
	Header:                 header, // 文件前 8 字节
	FirstLocalHeaderOffset: firstLocalHeaderOffset,
	CentralDirectory:       netsec.Range{Offset: cdOffset, Size: cdSize},
	EOCD:                   netsec.Range{Offset: eocdOffset, Size: eocdSize},
	SignatureBlock: netsec.SignatureBlock{
		Present: hasSigningBlock, // 文件中有 "APK Sig Block 42"
		Offset:  signingBlockOffset,
		Size:    signingBlockSize,
		IDs:     signatureBlockIDs, // 0x7109871a / 0xf05368c0
	},
	MinSDK:      minSDK,
	V1Signature: hasV1Signature,
}, func(ranges netsec.DigestRanges) bool {
	// 只有确认 v2 摘要实际覆盖以下三段时才返回 true。
	return digestCovers(ranges.Contents) &&          // [0, 签名分块起始)
		digestCovers(ranges.CentralDirectory) &&     // 中央目录
		digestCovers(ranges.EndRecord)               // 结束记录
})
```

`EOCD.Size` 为 0 时按无注释的标准 22 字节结束记录处理。所有偏移都是相对
文件起始位置的绝对偏移。签名块的 `Offset`/`Size` 覆盖完整块（含长度字段和
尾部魔数），因此合法的 v2 包应满足
`SignatureBlock.Offset+SignatureBlock.Size == CentralDirectory.Offset`。

## 判定顺序

- 前 8 字节以 `dex\n` 开头且结束记录合法：返回 `ErrPolymorphic`。
- 第一个本地文件头不在偏移 0：返回 `ErrFirstLocalHeader`。
- 签名块之后不是中央目录：返回 `ErrSignatureBlockPlacement`。
- `MinSDK >= 24` 且没有 v2/v3：仅有 v1 时返回 `ErrV1Only`，否则返回
  `ErrV2Required`。
- `MinSDK < 24` 且没有任何签名：返回 `ErrNoSignature`；仅 v1 可以通过。
- 存在 v2 但回调缺失，或摘要未覆盖内容/中央目录/结束记录任一段：返回
  `ErrDigestCoverage`。
- 偏移为负、范围溢出或结束记录不自洽：返回 `ErrInvalidMetadata`。

`netsec.Validate` 和 `netsec.Check` 是 `netsec.Admit` 的别名。所有拒绝错误
均为哨兵值，可用 `errors.Is` 判断。

## 测试

```sh
go test ./...
```
