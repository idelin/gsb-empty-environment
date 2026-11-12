# containerd镜像站配置准入

containerd 通过 certs.d 下各注册中心的 hosts.toml 配置镜像加速，条目含 capabilities、skip_verify、override_path 与 header，攻击者能写这个目录或控制镜像引用时，可让拉取走明文镜像站，或把凭证头发给不该接收的主机。为 Go 节点配置服务写供配置落盘前调用的校验包：输入解析后的结构（server、host 表、capabilities、skip_verify、ca、client、header、override_path）与调用方策略；skip_verify 为真或使用 http 的 host 拒绝，回环地址除外；Authorization 头只允许出现在与 server 同域的 host 条目中；只有 resolve 能力的 host 不得提供 pull；按主机名规范化（大小写、端口、末尾点）判断同域；按摘要引用拉取时，内容摘要不符须在换下一 host 前丢弃已取内容。用明文镜像、凭证头外发、仅 resolve 提供 pull、同域写法变形和摘要不符验证。

## 包结构

- `types.go`：解析后的输入结构 `Config`/`HostEntry`（含 capabilities、skip_verify、ca、client、header、override_path）与调用方 `Policy`。
- `validate.go`：落盘前调用的 `Validate`，汇总全部问题并以 `HostError` 标注来源条目。
- `hosts.go`：端点解析与规范化（小写、末尾点、默认端口）、`SameDomain`、回环判定、`HostsForPull`（排除仅 resolve 的 host）。
- `digest.go`：`Digest` 解析与 `PullByDigest`，摘要不符时先 `Discard` 再切换下一 host。
- `errors.go`：哨兵错误，调用方可用 `errors.Is` 匹配根因。

## 校验规则

- `http` 明文 host 与 `skip_verify=true` 一律拒绝；仅当端点是回环地址（`localhost`、`127.0.0.0/8`、`::1`）且 `Policy` 显式放行时例外，server 自身同样受约束。
- 端点必须是绝对 `http(s)` URL 且不得携带 userinfo；client 的 cert/key 必须同时提供；capabilities 只允许 `resolve`/`pull`/`push`。
- `Authorization` 头只允许出现在与 server 规范化后主机标识相同的 host 上；header 名大小写不敏感。
- 同域比较对主机名转小写、去除末尾根点并补齐 scheme 默认端口，因此 `HTTPS://REGISTRY.Example.COM`、`https://registry.example.com.` 与 `https://registry.example.com:443` 视为同域。
- `HostsForPull` 只返回可 pull 的 host：capabilities 为空时按 containerd 默认拥有全部能力，仅含 `resolve` 的镜像站被排除，全部不可 pull 时返回 `ErrNoPullHost`。
- `PullByDigest` 按 host 顺序拉取：传输失败或摘要不符即切换；摘要不符的已取内容在切换前经 `Content.Discard` 丢弃并关闭，绝不 commit；全部 host 失败时返回包含 `ErrDigestMismatch` 的汇总错误。

## 用法

```go
cfg := &netsec.Config{
	Server: "https://registry.example.com",
	Hosts: []netsec.HostEntry{
		{Host: "https://mirror.example.net", Capabilities: []netsec.Capability{netsec.CapabilityPull}},
	},
}

// 落盘前准入
if err := netsec.Validate(cfg, netsec.DefaultPolicy()); err != nil {
	return err
}

// 按摘要拉取，失败自动切换 host
err := netsec.PullByDigest(ctx, cfg, netsec.DefaultPolicy(), digest, puller, committer)
```

`Puller` 与 `Committer` 由调用方实现：前者负责向单个 host 发起请求并返回 `Content`，后者负责落盘通过摘要校验的内容；测试见 `netsec_test.go` 与 `digest_test.go`。
