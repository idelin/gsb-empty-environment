# libvirt磁盘密钥引用归属校验

libvirt 用 secret 对象向虚拟机磁盘与网络配置注入密码、Ceph 密钥或 LUKS 口令，域 XML 里引用的 secret 由租户填写，攻击者想引用别的租户的 secret，或用 usage 名称而不是 UUID 绕过归属检查。为 Go 管理面写供域定义与启动前调用的 secret 引用校验包：输入域 XML 片段（磁盘的认证与加密元素里的 secret：type、uuid 或 usage）、secret 注册表（uuid、usage、所属租户、private 与 ephemeral 标志）及租户身份；只允许引用本租户的 secret；按 usage 引用时若匹配多个，只考虑本租户的并要求无歧义；private 的 secret 值不得出现在任何查询结果；ephemeral 的 secret 不落盘且管理面重启后不可用；域的 UUID 与 secret 的 UUID 相同不得混淆。用引用他人 UUID、usage 歧义、private 读取和合规引用验证。

## 用法

在域定义或启动前调用 `Registry.ValidateDomainSecrets`，传入域 XML 片段与租户身份：

```go
reg := netsec.NewRegistry()
reg.Register(netsec.Secret{UUID: "...", Usage: "ceph-rbd", TenantID: tenantID /* ... */})

infos, err := reg.ValidateDomainSecrets(domainXML, tenantID)
if errors.Is(err, netsec.ErrCrossTenant) { /* 引用了其他租户的 UUID */ }
if errors.Is(err, netsec.ErrUsageAmbiguous) { /* 本租户内 usage 重名 */ }
```

## 安全边界

- **归属隔离**：UUID 引用命中其他租户的 secret 时返回 `ErrCrossTenant`；不存在的引用返回 `ErrSecretNotFound`，不泄露其他租户 secret 是否存在。
- **usage 租户内解析**：`usage` 只在调用租户的 secret 中匹配，跨租户同名 secret 既不能满足也不能污染结果；本租户内多条命中返回 `ErrUsageAmbiguous`。
- **private 不可读**：`SecretInfo` 只含元数据不含密钥材料；`GetValue` 对 private secret 一律返回 `ErrPrivateValueNotReadable`。
- **ephemeral 不落盘**：`PersistableSecrets` 排除 ephemeral 项；`Restart` 后其引用返回 `ErrEphemeralUnavailable`，重新 `Register` 后恢复。
- **UUID 命名空间分离**：域 UUID 仅解析但不参与 secret 查找，域 UUID 与 secret UUID 相同不会授权或拒绝访问，显式 `<secret>` 引用仍走归属校验。

## 测试

```sh
go test -race ./...
```

覆盖引用他人 UUID、usage 歧义与跨租户同名、private 读取拒绝、ephemeral 重启失效、域/secret UUID 混淆及合规引用等场景。
