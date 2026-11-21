依赖下载器跟随镜像重定向时，私有源令牌不能随请求进入公开镜像。给Go下载宿主实现重定向计划包，身份绑定精确来源与请求用途，每跳按规范化目标重新选择凭据；源站响应不得指定新凭据，只有调用方配置可授权。用双站点回调检查跨端口、跨方案、用户名段及回跳时的真实认证头。

## 使用

```go
origin, err := redirectplan.ParseOrigin("https://packages.example")
if err != nil {
	return err
}

client := redirectplan.New(redirectplan.Options{
	Credentials: redirectplan.CredentialSet{
		{Origin: origin, Purpose: "artifact"}: redirectplan.BearerToken(os.Getenv("PRIVATE_TOKEN")),
	},
})

resp, err := client.Get(ctx, "https://packages.example/module.zip", "artifact")
```

镜像需要独立凭据时，为镜像的规范化 origin 配置条目即可。没有条目的目标不会携带
`Authorization`、`Proxy-Authorization` 或 `Cookie`。

## 安全不变量

- 凭据键是 `(scheme, canonical host, effective port, purpose)`，不做域名后缀或通配匹配。
- 每一跳都重新解析 `Location` 并重新查表；禁止从上一跳复制认证材料。
- `http.Client` 的重定向逻辑不参与，避免同一主机跨端口或跨方案时保留 `Authorization`。
- 只读取重定向响应的 `Location`。响应头、URL userinfo 和上一跳凭据都不能产生新凭据。
- `Options.SensitiveHeaders` 可声明额外的认证头；它们同样每跳先剥离，再按目标配置重新附加。
- 跨 origin 只转发协议级安全头，未知自定义头默认丢弃；`Options.ForwardHeaders` 可显式放行非凭据头。
- `Options.Redirects` 可选择把允许的 origin 迁移限制为调用方白名单。

## 测试

`redirectplan_test.go` 使用两个进程内站点处理器接收真实 `*http.Request`，覆盖：
同主机跨端口、同主机同端口跨方案、URL 用户名段、A→B→A 回跳、purpose 隔离、
响应头注入、相对/同源重定向，以及规范化 origin 的精确匹配。
