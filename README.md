# 旧版接口没有挂上新的鉴权

平台给v2接口加了租户与角色校验，v1仍可调用同一后端且只做登录校验，攻击者用旧路径读取新权限数据。请实现供Go API网关调用的可导入版本路由组件。每个版本显式声明能力与鉴权策略，路由前先确定版本再执行统一鉴权；弃用版本只保留声明能力，未知版本拒绝。字段级差异按版本映射，旧版本不得访问新增敏感字段。用v1越权、未知版本、版本头与路径冲突、合法v2请求验证每个版本都经过对应鉴权。

## 用法

```go
router := netsec.NewRouter(netsec.DefaultRegistry(), backend)
rec, err := router.Serve(&netsec.Request{
    Path:          "/v2/users/u-1",
    VersionHeader: "v2",            // 可选；与路径不一致即拒绝
    Identity:      id,              // 登录中间件解析出的身份，可为 nil
}, "users.read.sensitive")
```

- `version.go`：`Version` 显式声明 `Policy`、`Capabilities`、`FieldMap`；`Registry` 只路由已注册版本。
- `router.go`：`ResolveVersion` 先定版本（未知拒绝、头/路径冲突拒绝），`Serve` 再统一执行该版本的鉴权、能力校验与字段映射。
- `fields.go`：`FilterFields` 按版本 `FieldMap` 投影响应，未映射的敏感字段对旧版本不可见。
- `router_test.go`：覆盖 v1 越权、未知版本、头/路径冲突、合法 v2、v2 策略强制、弃用版本能力冻结。
