旧签名方案只校验压缩包内各条目，不覆盖文件头部的字节，攻击者把一段 dex 拼在安装包前面，让它同时是合法的 dex 和合法的 zip，签名校验通过，系统却加载攻击者的代码。给Go应用分发网关实现可导入的安装包准入包，输入文件前 8 字节、压缩包中央目录与结束记录（EOCD，含拒绝 Zip64 与伪造 EOCD 注释长度溢出的校验）的偏移信息、第一个本地文件头的偏移、是否存在签名分块（魔数 APK Sig Block 42）及其中的 v2、v3 签名 ID（0x7109871a、0xf05368c0）、目标最低系统版本，以及调用方对 v2/v3 摘要范围的校验回调：文件以 dex 魔数（"dex\n"）开头或前 8 字节非 ZIP 本地文件头魔数且同时有合法的结束记录时判为多态并拒绝；第一个本地文件头不在偏移 0 时拒绝；最低系统版本不低于 24 却没有 v2 以上签名时拒绝，只有 v1 签名仅在低于 24 时接受；有 v2/v3 签名时，回调须确认摘要覆盖偏移 0 到签名分块之前、中央目录与结束记录三段且签名分块前后长度字段一致，缺任何一段拒绝；签名分块之后必须紧跟中央目录。用 dex 前缀拼接、本地头偏移非零、仅 v1 且最低版本 24、签名分块长度不一致、摘要缺中央目录和合法 v2 验证。

## 安装包准入包 `apkverify`

导入路径：`formal.local/w28-57aac11a51/apkverify`

`apkverify.Admit` 为纯函数式准入检查，不自行解析文件：全部偏移与解析结果由网关的
ZIP/APK 解析器通过 `apkverify.Input` 传入，v2/v3 摘要范围由调用方回调
`apkverify.DigestVerifier` 完成实际校验。

### 准入规则

1. 拒绝 Zip64；EOCD 注释长度必须使记录恰好终止于文件末尾，否则按伪造 EOCD 拒绝。
2. 多态攻击：以 `dex\n` 开头，或前 8 字节不是 ZIP 本地文件头魔数却存在合法 EOCD，拒绝。
3. 第一个本地文件头必须位于偏移 0，否则拒绝（DEX 前缀拼接会使其后移）。
4. `MinSDKVersion >= 24` 且无 v2/v3 签名（ID `0x7109871a` / `0xf05368c0`）时拒绝；
   仅 v1（JAR）签名只在 API 24 以下接受。
5. 存在 v2/v3 签名时，签名分块前后两个 uint64 长度字段必须一致，且分块后必须紧跟中央目录。
6. 摘要必须恰好覆盖三段：`[0, 签名分块)`、中央目录、EOCD 到文件末尾；回调返回错误即拒绝。

### 调用示例

```go
import "formal.local/w28-57aac11a51/apkverify"

verifier := apkverify.DigestVerifier(func(segs []apkverify.DigestSegment) error {
    if len(segs) != 3 {
        return errors.New("digest must cover exactly three ranges")
    }
    // 调用方依据 v2/v3 签名块中的摘要，逐段重新计算并比对。
    return verifyContentDigest(file, segs)
})

err := apkverify.Admit(apkverify.Input{
    Head8:                  head8,
    FirstLocalHeaderOffset: firstLocal,
    CentralDirOffset:       cdOffset,
    EOCDOffset:             eocdOffset,
    EOCDCommentLength:      commentLen,
    FileSize:               size,
    Zip64:                  zip64,
    HasSigBlock:            hasSigBlock,
    SigBlockOffset:         sigOffset,
    SigBlockSizeBefore:     sizeBefore,
    SigBlockSizeAfter:      sizeAfter,
    SignatureIDs:           ids,
    MinSDKVersion:          minSDK,
    VerifyDigest:           verifier,
})
```

错误均为包级哨兵错误（如 `apkverify.ErrDexPrefix`、`apkverify.ErrMissingV2Signature`），
使用 `errors.Is` 匹配。测试：`go test ./...`。
