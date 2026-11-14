# 出站 HTTP(S) 目标校验组件

`example.org/netsec/q0300` 是一个可导入的 SSRF 防护组件。它只使用
`net/url` 解析目标一次，随后把同一个 `*url.URL` 交给 `net/http` 发起请求，
因此校验目标与实际请求目标不存在正则/解析器差异。

## 安全属性

- 只接受 `http` 和 `https`，拒绝 opaque URL、userinfo（包括空 userinfo）、
  fragment、反斜杠、控制字符和空端口。
- 默认只允许 HTTP 80 和 HTTPS 443。其他端口必须通过 `AllowedPorts` 或
  `AllowNonStandardPorts` 显式放行。
- 主机先做 UTS #46/IDNA 规范化，再识别十进制、八进制、十六进制和缩写形式
  的数字 IPv4，以及 IPv4-mapped IPv6；规范化完成后才做地址策略判断。
- 每个请求只解析一次域名。解析结果全部通过地址策略后，连接时只拨号这些
  已校验的 IP，不把域名再次交给拨号器，防止 DNS 重绑定。
- 默认拒绝私网、回环、链路本地、未指定、组播和保留地址。可通过
  `AllowedCIDRs`、`AllowedHosts`、`AllowPrivate`、`AllowLoopback`、
  `AllowLinkLocal` 显式放宽。
- `Validator` 实现 `http.RoundTripper`，每一个重定向 hop 都会重新做静态
  校验、DNS 解析、地址策略判断和 IP 固定。
- 默认不复用 keep-alive 连接，确保每次请求都连接到本次校验得到的地址。

## 用法

```go
package main

import (
	"context"
	"io"
	"net/http"

	netsec "example.org/netsec/q0300"
)

func fetch(ctx context.Context, rawURL string) ([]byte, error) {
	v, err := netsec.NewValidator(netsec.Config{})
	if err != nil {
		return nil, err
	}

	// 目标只在这里解析一次；返回的 request 已绑定本次解析并校验的 IP。
	req, target, err := v.NewRequest(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := v.Client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	_ = target // target.IP / target.IPs 是实际允许拨号的地址集合
	return io.ReadAll(resp.Body)
}
```

也可以直接使用默认策略：

```go
target, err := netsec.Validate("https://example.com/")
```

## 显式放行

测试、内网代理或固定上游需要放行非常规目标时，应使用最小范围的显式配置：

```go
v, err := netsec.NewValidator(netsec.Config{
	// 仅允许测试服务所在端口。
	AllowedPorts: []int{8443},
	// 仅允许指定网段；不要放行 0.0.0.0/0。
	AllowedCIDRs: []string{"10.20.30.0/24"},
	// 也可按规范化后的主机名放行。裸主机名放行该主机的任意端口；
	// host:port 形式只放行该端口。
	AllowedHosts: []string{"internal.example.test:8443"},
})
```

`AllowedHosts` 是运维侧的信任决定：该主机仍只解析一次且 IP 会被固定，但
允许它解析到默认禁止的私网、回环或链路本地地址。

## 校验

```sh
CGO_ENABLED=0 go test ./...
CGO_ENABLED=0 go test -race ./...
```

测试覆盖 userinfo、多重编码、数字 IP、IPv4-mapped IPv6、IDNA、非常规端口、
重定向到 userinfo/数字内网地址、重定向 hop 的 DNS 变化，以及合法放行目标
上实际拨号地址与 `Target.IP/IPs` 一致。

## 依赖

`third_party/golang.org/x/net/idna` 与所需的
`third_party/golang.org/x/text/*` 来自 Go 工具链的 vendored 源码，仅用于
在离线环境中执行标准 IDNA 处理；原始 BSD 许可证和 PATENTS 文件一并保留。
