# containerd镜像站配置准入

containerd 通过 certs.d 下各注册中心的 hosts.toml 配置镜像加速，条目含 capabilities、skip_verify、override_path 与 header，攻击者能写这个目录或控制镜像引用时，可让拉取走明文镜像站，或把凭证头发给不该接收的主机。为 Go 节点配置服务写供配置落盘前调用的校验包：输入解析后的结构（server、host 表、capabilities、skip_verify、ca、client、header、override_path）与调用方策略；skip_verify 为真或使用 http 的 host 拒绝，回环地址除外；Authorization 头只允许出现在与 server 同域的 host 条目中；只有 resolve 能力的 host 不得提供 pull；按主机名规范化（大小写、端口、末尾点）判断同域；按摘要引用拉取时，内容摘要不符须在换下一 host 前丢弃已取内容。用明文镜像、凭证头外发、仅 resolve 提供 pull、同域写法变形和摘要不符验证。

## 包结构

- `types.go`：`Config` / `HostEntry`（对应 server 与 `[host."..."]` 表）和调用方 `Policy`。
- `validate.go`：落盘前静态准入 `Validate(cfg, policy)`，含主机名规范化、回环判定与能力归一化。
- `pull.go`：`PullVerified` 摘要校验拉取，按 host 顺序回退。
- `errors.go`：`ErrInsecureHost`、`ErrCredentialLeak`、`ErrResolveOnlyPull`、`ErrDigestMismatch` 等哨兵错误，均支持 `errors.Is`。

## 准入规则

- `http://` 或 `skip_verify=true` 的条目拒绝；`localhost`、`127.0.0.0/8`、`::1`、`.localhost` 在默认策略下豁免，`Policy.AllowInsecureLoopback=false` 可连回环一并拒绝。
- `Authorization` 头仅允许出现在与 `server` 同域的 host 上；同域按规范化后的 `host:port` 比较（大小写、默认端口、末尾点折叠）。
- 空 capabilities 按 containerd 默认视为 `pull,resolve,push`；未知或重复能力报错；`resolve` 专属镜像不会被选为 pull 候选。
- 可选 `AllowedHosts` 白名单与摘要算法白名单（默认 `sha256`、`sha512`）。

## 用法

```go
cfg := &netsec.Config{
    Server: "https://registry-1.docker.io",
    Hosts: []netsec.HostEntry{
        {URL: "https://mirror.example.com", Capabilities: []string{"pull", "resolve"}},
    },
}
if err := netsec.Validate(cfg, netsec.DefaultPolicy()); err != nil {
    // 拒绝落盘；errors.Is(err, netsec.ErrInsecureHost) 等可定位具体违规
}

// 摘要拉取：摘要不符的内容在尝试下一 host 前整体丢弃
blob, err := netsec.PullVerified(ctx, cfg, nil, "sha256:...", fetcher)
```

`fetcher` 实现 `netsec.Fetcher`，负责按 host 条目（含 `override_path`、`header`）发起实际请求并返回响应体。
