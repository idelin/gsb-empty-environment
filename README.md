# BuildKit 源策略准入

`netsec` 提供一个无状态、并发安全的 Go 包，用于在 solve 请求准入阶段检查
BuildKit 构建图中的 source 操作。零值策略默认拒绝；规则按声明顺序求值，首个
命中的 `EXACT`、`WILDCARD` 或 `REGEX` 规则决定结果。

## 用法

```go
policy := netsec.Policy{Rules: []netsec.Rule{
    {
        ID:        "pin-alpine",
        MatchType: netsec.MatchWildcard,
        Pattern:   "docker.io/library/alpine:*",
        Action:    netsec.ActionConvert,
        ConvertTo: "docker.io/library/alpine@sha256:<digest>",
    },
    {
        ID:        "deny-pinned",
        MatchType: netsec.MatchExact,
        Pattern:   "docker.io/library/alpine@sha256:<digest>",
        Action:    netsec.ActionDeny,
    },
}}

graph := &netsec.Graph{Vertices: []netsec.Vertex{
    {ID: "source", Source: "alpine:latest"},
}}

result, err := policy.Evaluate(graph)
if err != nil {
    // 规则或图结构非法。
}
if !result.Allowed {
    // result.FirstDenial() 给出顶点、最终标识和命中的规则。
}

// 准入边界可直接使用：
if err := policy.Authorize(graph); err != nil {
    // 拒绝 solve 请求。
}
```

## 规范化和校验

- 镜像引用补齐 `docker.io/` 与官方镜像的 `library/` 前缀；标签和摘要同时存在
  时只保留摘要。摘要和标签会做严格格式校验，多个或伪造的 `@` 分隔符直接拒绝。
- git URL 必须带完整提交哈希（40 或 64 位十六进制）；分支、标签、短哈希和缺失
  引用均拒绝。URL 和 `git.fullurl`/`git.url` 属性不得包含用户信息。
- HTTP 源必须提供合法的 `http.checksum`（兼容 `http.chksum`、`checksum`）。
  校验和绑定内容，因此重定向不能替换为未获准的内容。URL 用户信息和片段不会参与
  准入身份。
- 本地上下文接受 `local://`、`context://` 或显式 `KindLocal` 标识。

## 转换语义

首个命中为 `ALLOW` 或 `DENY` 时立即结束。首个命中为 `CONVERT` 时，转换目标会
重新规范化；之后继续扫描后续规则，任何命中的 `DENY` 都会拒绝结果，即使中间先
命中了 `ALLOW`。后续的 `CONVERT` 也可继续串联。

## 图去重

同一顶点 ID 被多个父顶点依赖时只计算一次。重复且完全一致的顶点声明会折叠；
同一 ID 出现不同定义会作为非法图拒绝。`Deps`、`Dependencies` 和显式 `Edge`
均可表示依赖关系。设置 `Graph.Roots` 时只判定从根可达的子图；未设置时判定图中
全部顶点。

运行验证：

```sh
go test ./...
go vet ./...
```
