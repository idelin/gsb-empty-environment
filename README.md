# containerd镜像站配置准入

containerd 通过 certs.d 下各注册中心的 hosts.toml 配置镜像加速，条目含 capabilities、skip_verify、override_path 与 header，攻击者能写这个目录或控制镜像引用时，可让拉取走明文镜像站，或把凭证头发给不该接收的主机。为 Go 节点配置服务写供配置落盘前调用的校验包：输入解析后的结构（server、host 表、capabilities、skip_verify、ca、client、header、override_path）与调用方策略；skip_verify 为真或使用 http 的 host 拒绝，回环地址除外；Authorization 头只允许出现在与 server 同域的 host 条目中；只有 resolve 能力的 host 不得提供 pull；按主机名规范化（大小写、端口、末尾点）判断同域；按摘要引用拉取时，内容摘要不符须在换下一 host 前丢弃已取内容。用明文镜像、凭证头外发、仅 resolve 提供 pull、同域写法变形和摘要不符验证。

## 包结构

- `types.go`：`Config`/`Host`/`Policy`/`ClientCert`，字段与 hosts.toml 的 capabilities、skip_verify、ca、client、header、override_path 对齐。
- `normalize.go`：端点解析与规范化（小写、默认端口、末尾点、IPv6），同域与回环（`localhost`、`*.localhost`、环回 IP）判定。
- `validate.go`：落盘前准入校验 `Validate(cfg, policy)`，返回的多错误可用 `errors.Is` 匹配 `ErrPlaintext`、`ErrCredentialLeak`、`ErrResolveOnlyPull` 等哨兵错误。
- `pull.go`：`Puller` 按 host 顺序拉取，跳过无 pull 能力及策略标记的 resolve-only 镜像；摘要流式校验，不符即在切换下一 host 前删除临时文件，校验通过后原子改名落盘。

## 用法

```go
policy := netsec.DefaultPolicy()
policy.ResolveOnlyHosts = []string{"disco.example.com"}

cfg := &netsec.Config{
    Server: "https://registry.example.com",
    Hosts: []netsec.Host{
        {Host: "https://mirror.example.com", Capabilities: []string{netsec.CapPull}},
    },
}
if err := netsec.Validate(cfg, policy); err != nil {
    // 拒绝落盘；errors.Is(err, netsec.ErrPlaintext) 等可区分违规类型
    return err
}

puller := &netsec.Puller{Client: client, Policy: policy, Dir: "/var/lib/cache"}
path, digest, err := puller.Pull(ctx, cfg, "repo/image@sha256:...")
```

## 验证场景

- 明文镜像：外部 http 与 skip_verify 拒绝；回环（`localhost`、`127.0.0.1`、`[::1]`）在 `DefaultPolicy` 下放行。
- 凭证头外发：异域 host 携带 `Authorization` 拒绝；大小写、`:443`、末尾点等同域变形放行，异端口拒绝。
- 仅 resolve 提供 pull：策略名单中的 host 带 pull 能力被 `Validate` 拒绝，`Puller` 运行时再兜底跳过。
- 同域写法变形：`REGISTRY.EXAMPLE.COM.:443` 与 `registry.example.com` 规范化后判定同域。
- 摘要不符：恶意 host 返回被篡改内容时丢弃临时文件并切换下一 host；全部不符则返回包裹 `ErrDigestMismatch` 的 `ErrNoPullHost`，目录无残留。

## 测试

```sh
go test ./...
```
