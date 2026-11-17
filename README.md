# 下载重定向凭据隔离

依赖下载器跟随镜像重定向时，私有源令牌不能随请求进入公开镜像。给Go下载宿主实现重定向计划包，身份绑定精确来源与请求用途，每跳按规范化目标重新选择凭据；源站响应不得指定新凭据，只有调用方配置可授权。用双站点回调检查跨端口、跨方案、用户名段及回跳时的真实认证头。

`netsec` 把“来源 + 用途”作为凭据的精确作用域：

```go
planner, err := netsec.NewPlanner(netsec.Config{
    Credentials: []netsec.Credential{{
        Identity: "private-download",
        Source:   netsec.MustParseOrigin("https://packages.internal:8443"),
        Purpose:  netsec.PurposeDownload,
        Headers: http.Header{
            "Authorization": {"Bearer source-token"},
        },
    }},
})
if err != nil {
    return err
}

req, err := planner.NewRequest(ctx, http.MethodGet, sourceURL, netsec.PurposeDownload)
if err != nil {
    return err
}
resp, err := planner.Client().Do(req)
```

也可以把计划器接到已有客户端上；`WrapClient` 会在传输层重新清洗每个出站请求，
并关闭 cookie jar，避免环境中的 cookie 或后加的认证头绕过按来源配置的授权。

```go
client := planner.WrapClient(&http.Client{Transport: transport})
```

安全约束：

- 来源按小写 scheme/host、默认端口、尾随根点和规范化 IP 比较；端口或 scheme
  不同就是不同来源。
- 重定向 URL 中的 userinfo 一律删除，不能变成 `Authorization: Basic ...`。
- 每次重定向先清除 `Authorization`、`Proxy-Authorization`、`Cookie` 以及所有
  配置过的凭据头，再只为规范化后的目标来源和原请求用途重新选择凭据。
- 重定向响应里的 `WWW-Authenticate`、`Set-Cookie` 或自定义头不会成为新凭据。
- `Prepare` 会先清除调用方直接放在初始请求上的认证头；只有 `Config` 中的规则
  可以授权。

`redirect_test.go` 使用两个独立站点（各自端口，包含 HTTPS→HTTP 和 HTTPS
回跳）在服务端记录真实请求头，覆盖跨端口、跨方案、URL userinfo 和回跳场景。
