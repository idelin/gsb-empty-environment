# 下载重定向凭据隔离

依赖下载器跟随镜像重定向时，私有源令牌不能随请求进入公开镜像。给Go下载宿主实现重定向计划包，身份绑定精确来源与请求用途，每跳按规范化目标重新选择凭据；源站响应不得指定新凭据，只有调用方配置可授权。用双站点回调检查跨端口、跨方案、用户名段及回跳时的真实认证头。

## 实现

包 `netsec` 由四个文件组成：

- `origin.go`：`Origin` 与 `CanonicalOrigin`，把 scheme/host/port 规范化（小写、补默认端口、IPv6/末尾点处理），URL userinfo 一律忽略。
- `credential.go`：`Purpose`、`Credential`、`Rule`、`CredentialStore`。凭据只能由调用方通过显式规则授权，规则按精确来源 + 用途匹配，首条命中生效。
- `planner.go`：`Planner.Do` 手动驱动重定向循环，每一跳先剥离 `Authorization`、`Proxy-Authorization`、`Cookie`，再按规范化目标来源与请求用途从 store 重新选择凭据；`Location` 中的 userinfo 被丢弃，`WWW-Authenticate` 等响应内容不会产生凭据，无匹配规则时匿名发送。
- `planner_test.go`：基于 `net.Pipe` 的内存双站点（私有 TLS 源站 + TLS/明文镜像），不绑定任何 socket，回调记录每跳真实认证头。

## 安全不变量

- 凭据绑定规范化来源（scheme、host、port）与请求用途，跨端口、跨方案、跨主机均不自动携带。
- 每跳重新选证，前一跳的令牌不会复制到下一跳；镜像回跳源站时由 store 重新发券。
- 源站响应无法引入或升级凭据：重定向 URL 的用户名段被剥离且不会转成 `Authorization` 头。
- 只有调用方的 `CredentialStore` 规则能授权；未配置的来源（含首跳）保持匿名。

## 测试

```sh
go test ./... -race
```

覆盖场景：跨端口、跨方案（https→http）、`Location` 用户名段注入、镜像回跳、用途绑定、来源规范化、跨来源规则隔离、匿名首跳、重定向环上限。
