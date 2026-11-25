# admission：安装包准入控制

面向 Go 应用分发网关的可导入库，防御 Janus 类多态文件攻击（在合法 ZIP/APK
# 前拼接 DEX，使旧 v1 签名仍校验通过、系统却加载前缀代码）。

## 准入规则

- 文件以 `dex\n` 开头，或前 8 字节不是 ZIP 本地文件头魔数（`PK\x03\x04`）
  却存在合法 EOCD 时，判为多态文件并拒绝（`ErrPolymorphic`）。
- 第一个本地文件头不在偏移 0 时拒绝（`ErrLocalHeaderNotAtZero`）。
- EOCD 注释长度越过 EOF（伪造偏移）拒绝（`ErrEOCDCommentOverflow`）；
  任何 Zip64 痕迹（Locator/记录或 `0xffffffff` 哨兵）拒绝（`ErrZip64`）。
- `minSdkVersion >= 24` 却没有 v2/v3 签名时拒绝（`ErrUnsupportedSignature`），
  仅 v1 签名只在 `< 24` 时接受。
- 存在 v2（`0x7109871a`）/v3（`0xf05368c0`）签名时，回调必须确认摘要覆盖
  「偏移 0 到签名分块之前」「中央目录」「结束记录」三段，且签名分块前后
  长度字段满足 `after = before + 8`，缺段或不符拒绝（`ErrDigestCoverage` /
  `ErrSigBlockSizeMismatch`）。
- 签名分块之后必须紧跟中央目录（`ErrSigBlockNotAdjacent`）。

## 用法

```go
import "prescreen.local/ps-57aac11a51/admission"

layout, err := admission.ParseLayout(file, fileSize) // file 实现 io.ReaderAt
if err != nil {
    return reject(err)
}

err = admission.AdmitLayout(layout, minSDK, func(c admission.DigestCoverage, b admission.SigBlock) error {
    // 调用方在此完成 v2/v3 摘要与签名的密码学校验，
    // 并核对实际摘要保护的字节区间与 c 的三段完全一致。
    return verifyCryptographically(file, c, b)
})
```

也可以手工填充 `admission.Input` 后调用 `admission.Admit`，适用于上游已经
解析过 ZIP 布局、只需要复用准入判定的场景。
