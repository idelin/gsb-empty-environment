# 车载固件双源目标交叉核对

车载 ECU 安装更新前须同时核对 Director 与 Image 两套元数据，攻击者只攻破其中一个仓库来推送恶意固件。为Go ECU 更新客户端写可导入的目标核对包：Director 的目标须点名本 ECU 序列号与硬件标识，其文件名、长度与 SHA-256 须同 Image 仓库同名目标完全一致；任一仓库的版本号低于本地已见值、元数据过期或签名数不足阈值即拒绝；只在一侧列出的目标不得安装。

## `targetverify` 包

导入路径：`example.org/netsec/q0857/targetverify`（仅依赖标准库）。

一个目标只有在同时通过下列检查时才会被放行，任一失败均返回可经
`errors.Is` 匹配的哨兵错误并拒绝安装：

- 两套 `targets` 元数据各自的有效签名数（不同受信密钥）达到独立阈值。
- 两套元数据在当前时间均未过期（`Now` 可注入，便于测试与单调时钟对接）。
- 两套元数据版本号均不低于本地已见版本，防止版本回滚；成功核对后才推进状态。
- Director 中恰有一个目标在 `custom` 中点名本 ECU 的序列号与硬件标识。
- 该目标在 Image 仓库存在同名条目，且 `length` 与 `sha256` 完全一致。
- 仅出现在一侧的目标通过 `Result.SkippedOneSided` 上报审计，绝不安装；
  本 ECU 的 Director 目标若在 Image 侧缺失，直接拒绝（`ErrImageSideMissing`）。

签名针对信封内原始 `signed` 字节校验，任何对已签名负载的篡改都会使签名失效。

### 用法

```go
import "example.org/netsec/q0857/targetverify"

directorRing, _ := targetverify.NewKeyring(
    mustKey("director-1", pub1), mustKey("director-2", pub2))
imageRing, _ := targetverify.NewKeyring(
    mustKey("image-1", pub3), mustKey("image-2", pub4))

verifier, err := targetverify.New(targetverify.Config{
    ECU:               targetverify.ECUIdentity{Serial: "ECU-0001", Hardware: "HW-A"},
    DirectorThreshold: 2,
    ImageThreshold:    2,
    DirectorKeys:      directorRing,
    ImageKeys:         imageRing,
})
if err != nil { /* 配置无效，fail-closed */ }

// 启动时恢复本地持久化的已见版本号。
_ = verifier.SetState(targetverify.State{DirectorVersion: 7, ImageVersion: 9})

result, err := verifier.Verify(directorEnvelopeJSON, imageEnvelopeJSON)
if err != nil {
    // 拒绝更新：回滚、过期、签名不足、ECU 不匹配、双源不一致等。
    return err
}

// 下载完成后、安装前再核对固件实体的长度与 SHA-256。
f, _ := os.Open(result.Target.Name)
defer f.Close()
if err := result.Target.VerifyArtifact(f); err != nil {
    return err
}

// 核对成功后持久化 verifier.State()，作为下次启动的回滚保护基线。
```

### 元数据信封格式

```json
{
  "signed": {
    "_type": "targets",
    "version": 8,
    "expires": "2026-12-01T00:00:00Z",
    "targets": {
      "firmware-v2.bin": {
        "file": {"length": 1234, "hashes": {"sha256": "<hex>"}},
        "custom": {"ecuIdentifier": "ECU-0001", "hardwareIdentifier": "HW-A"}
      }
    }
  },
  "signatures": [{"keyid": "director-1", "sig": "<hex>"}]
}
```

Image 仓库的 `targets` 条目结构相同，但不需要 `custom` ECU 绑定。

### 验证

`go test ./... -race`，覆盖正常放行、ECU/硬件不匹配、长度与哈希不一致、
回滚重放、过期、签名阈值不足、篡改签名负载、畸形文档与 fail-closed 配置。
