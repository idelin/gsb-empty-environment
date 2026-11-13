# 固件密钥清单的阈值与撤销代次

引导ROM信任一个根密钥签发的固件密钥清单，攻击者可能重放旧清单恢复已撤销密钥，或重复同一签名者凑阈值。给Go引导准入宿主实现可导入的清单验证包：根信任集合由宿主固定，每根有Ed25519公钥和唯一ID；清单规范编码包含设备型号、单调代次、有效时间窗（左闭右开）、固件签名阈值t、固件密钥条目(keyID、公钥、用途、启止代次)及撤销keyID集合。根签名覆盖完整规范编码，按根ID去重，至少达到宿主当前根阈值；未知根不计票，重复根不同签名为冲突。可信存储保存已接受最高代次和清单摘要，较低代次拒绝，同代次仅允许完全相同摘要。固件清单签名覆盖型号、镜像摘要、固件版本和清单代次；计票只使用当前清单内未撤销、用途含firmware且代次在条目窗口内的不同keyID，公钥材料相同的别名只计一次，达到t才启动。新清单的撤销集合必须包含旧清单全部撤销项，且旧清单中仍有效的keyID不能在新清单中换公钥材料；阈值不得低于宿主最低值；提交最高代次与清单须原子，崩溃恢复不能出现新代次配旧内容。用真实签名测试旧清单重放、别名密钥、撤销回退、阈值降低、同代分叉和最后一笔持久化失败。

## 包与接口

实现位于根包 `netsec`，导入路径为 `example.org/netsec/q0093`。

- `NewHost` 固定设备型号、根集合、根阈值、最低固件阈值和 `TrustedStore`。
- `Host.AcceptManifest` / `AcceptManifestAt` 验证根签名、时间窗和代次策略，并通过一次 `Commit` 原子提交新状态。
- `Host.AdmitFirmware` / `AdmitFirmwareAt` 只使用可信存储中的当前清单，按固件阈值计票。
- `Manifest.SignRoot` 与 `FirmwareAttestation.Sign` 用真实 Ed25519 私钥生成测试或发布签名。
- `FileStore` 将代次、摘要和规范清单正文写入同一个临时记录，`fsync` 后原子 `rename`，再同步目录。

```go
host, err := netsec.NewHost(netsec.HostConfig{
    Model:                    "router-x",
    Roots:                    fixedRoots,
    RootThreshold:            2,
    MinimumFirmwareThreshold: 2,
    Store:                    store, // netsec.TrustedStore
})

manifest := netsec.Manifest{
    Model:             "router-x",
    Generation:        7,
    ValidFrom:         from,
    ValidUntil:        until,
    FirmwareThreshold: 2,
    Keys:              firmwareKeys,
    RevokedKeyIDs:     revoked,
}
err = manifest.SignRoot("root-1", root1Private)
err = manifest.SignRoot("root-2", root2Private)
_, err = host.AcceptManifest(manifest)

attestation := netsec.FirmwareAttestation{
    Model:              "router-x",
    ImageDigest:        sha256.Sum256(image),
    Version:            "2.4.0",
    ManifestGeneration: 7,
}
err = attestation.Sign("fw-1", fw1Private)
err = attestation.Sign("fw-2", fw2Private)
_, err = host.AdmitFirmware(attestation)
```

## 安全语义

- 根签名覆盖规范清单正文；签名本身和宿主根阈值不在正文中。编码使用长度前缀、大端整数、UTC 纳秒时间，并对 keyID、用途和撤销集合排序。
- 同一根 ID 的完全重复签名只计一票；同一根 ID 的不同签名直接冲突。未知根和无效签名不增加票数。
- 已接受代次只能推进；同代次正文摘要必须完全一致，否则视为分叉。重复接受同一摘要为幂等操作，不重写存储。
- 新清单必须保留旧清单全部撤销项。旧清单中仍覆盖新代次的未撤销 keyID，如继续出现在新清单中，必须保持相同公钥材料。
- 固件计票会过滤撤销、非 `firmware` 用途、代次窗口外和未知 keyID；相同公钥材料的多个 keyID 只计一个唯一签名者。
- 清单代次、SHA-256 正文摘要和规范正文存放在同一条存储记录中。恢复时三者互相校验，任何“新代次配旧正文”或被截断的记录都会被拒绝。

## 测试

```sh
go test -race ./...
```

测试使用真实 Ed25519 密钥与签名，覆盖旧清单重放、根/固件重复签名、公钥别名、撤销回退、阈值降低、活跃密钥材料替换、同代分叉、最后持久化失败，以及文件存储恢复与混合记录拒绝。
