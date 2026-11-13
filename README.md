# Trailer参与路由

尾随头里的Host或长度在路由完成之后才被拿来改信。尾随头禁止host、content-length、transfer-encoding和任何伪头；出现则流失败。声明过的自定义尾随头可以在正文结束后到达。协议网关调用可导入的Go库做这次裁决。在尾随部分写入host。

## 裁决规则

`netsec` 是供协议网关导入的 Go 库（模块 `example.org/netsec/q0085`，包 `netsec`），
对 HTTP 尾随头（trailer）做一次安全裁决。尾随头在正文结束、路由已经完成之后才到达，
因此绝不允许再用它改写目标主机或报文长度/分帧：

- 禁止字段（大小写不敏感）：`host`、`content-length`、`transfer-encoding`。
- 禁止任何伪头：字段名去掉首尾空白后以 `:` 开头（如 `:path`、`:authority`、`:method`、`:scheme`、`:status`）。
- 命中任一禁止字段即判定流失败，返回 `*netsec.StreamError`，其底层哨兵为 `netsec.ErrForbiddenTrailer`，网关应中止该流而不是转发。
- 已在 `Trailer` 头里声明过的自定义尾随头允许在正文结束后到达并放行。
- 可选开启 `WithRequireDeclared(true)` 严格模式：未提前声明的尾随头返回 `netsec.ErrUndeclaredTrailer`。

## 用法

一次性裁决（最常见）：

```go
import "example.org/netsec/q0085"

if err := netsec.ValidateTrailers(req.Trailer); err != nil {
    // 命中禁止尾随头（例如在尾随部分写入 host），流失败，不要转发。
}
```

携带声明信息、可复用的裁决器：

```go
validator := netsec.NewTrailerValidator(
    []string{"X-Checksum"},
    netsec.WithRequireDeclared(true),
)

err := validator.ValidateFields([]netsec.TrailerField{
    {Name: "Host", Value: "evil.example"}, // 路由完成后才到达 → 流失败
})
// errors.Is(err, netsec.ErrForbiddenTrailer) == true
```

也可直接用谓词判断单个字段：

```go
if netsec.ForbiddenTrailer(name) { // host / content-length / transfer-encoding / :伪头
    // 拒绝
}
```

## 测试

```sh
go test ./...
```

其中 `TestHostInTrailerFailsStream` 覆盖“在尾随部分写入 host”的场景：
尾随块中的 `Host` 会让整个流失败，而不会在路由完成后被用来改信。
