RPKI 依赖方（Relying Party）从不可信发布点同步 CA 证书、Manifest（RFC 9286）、CRL 与 ROA/ASPA 签名对象时，若验证器忽略 Manifest 序列号单调性、放过缺失文件的部分清单，或未验证子 CA 的 RFC 3779 IP/ASN 资源集严格包含于父 CA 资源集内，恶意镜像仓库可发起降级回滚攻击（重放旧有效 Manifest 隐藏新撤销记录）或子 CA 越权签发其他运营商前缀。为 Go RPKI 依赖方宿主构建可导入的离线仓库自顶向下验证与防回滚状态机包，严禁联网。组件从信任锚（TAL）出发逐层验证资源证书树（真实 ECDSA-P256 签名核验），强制执行：（1）RFC 3779 精确 IPv4/IPv6 前缀区间与 ASN 区间继承判定——子 CA 及其 EE 证书声明的每一段 IP/ASN 资源必须完全落于父 CA 授权集合内（支持 `inherit` 继承展开，严禁子 CA 扩大一丁点范围），发现超界立即作废该子树；（2）RFC 9286 严格 Fail-Closed Manifest 校验——每个发布点须存在有效 EE 证书签发的 Manifest 与 CRL，清单中列出的全部文件必须全部存在且 SHA-256 摘要精确匹配，缺任一文件或任一哈希不符即整点判定失效（拒绝部分采用）；（3）跨周期防回滚状态保存——按 CA 持久化上次成功验证的 `(manifestNumber, thisUpdate)`，新轮次中 `manifestNumber` 倒退或过期立即拒绝并保留/回退至明确定义的失败安全策略，拒绝循环 `sia` 指针与超深委派链。附完整对抗测试覆盖资源越界子 CA、单文件缺失 Manifest、旧序列号重放回滚与吊销 EE 证书全部拒收。

## 实现说明

根包 `rpki` 只依赖 Go 标准库，不包含任何网络访问。宿主可将镜像同步到本地目录后使用 `NewOSRepository`，或直接把对象放入 `MapRepository`。

```go
tal, err := rpki.ParseTAL("example", talBytes)
repo, err := rpki.NewOSRepository("/var/lib/rpki/offline-repo")
states := rpki.NewFileStateStore("/var/lib/rpki/watermarks.json")

validator := rpki.NewValidator(repo, rpki.Options{
    States:   states,
    Now:      time.Now,
    MaxDepth: 16,
})
report, err := validator.ValidateTAL(context.Background(), tal)
```

失败安全轮次可使用 `RoundMachine`：

```go
machine := &rpki.RoundMachine{
    States:    states,
    Snapshots: rpki.NewFileSnapshotStore("/var/lib/rpki/snapshots"),
    Policy:    rpki.FailurePolicyRestoreSnapshot,
}
result, err := machine.RunRound(ctx, tal, repo)
```

关键哨兵错误包括 `ErrOutOfScope`、`ErrManifestMissing`、`ErrManifestHash`、`ErrRollback`、`ErrManifestExpired`、`ErrRevoked`、`ErrSIACycle` 与 `ErrDepth`，可用 `errors.Is` 判定。

测试：

```sh
go test -race -count=1 ./...
```

测试夹具会生成真实 ECDSA-P256 CA/EE 密钥、X.509 证书、CRL、RFC 9286 CMS Manifest，以及 ROA/ASPA 对象；对抗用例覆盖越界子 CA、越界 EE、文件缺失、摘要篡改、未列文件、旧序列号重放、`thisUpdate` 倒退、过期 Manifest、四类吊销 EE、SIA 循环和委派深度限制。
