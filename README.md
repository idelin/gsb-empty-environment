# 未知字段被原样带进了下游

消息服务把未知字段保留并转发，攻击者利用旧版本不校验的字段注入权限或路由信息，新版本读取后生效。请实现供Go消息服务调用的可导入字段策略组件。每个消息版本声明已知字段与保留字段，未知字段按策略丢弃或隔离；字段编号、类型与重复次数校验。跨版本转换显式映射。用未知字段注入、编号复用、类型冲突、合法消息和版本转换验证下游只读已知字段。

## 组件说明

`netsec` 是零第三方依赖的可导入 Go 包，在消息服务边界强制执行字段策略，
确保下游只能读取已声明、已校验的已知字段。

- `schema.go`：`VersionSchema` 声明每个版本的已知字段（编号、名称、类型、
  单值/重复、重复上限）与保留字段编号区间；构造时即拒绝重复编号、重复名称、
  保留编号占用等非法声明。`Registry` 管理多个版本。
- `policy.go`：`Sanitizer` 校验字段编号、动态值类型、单值字段重复与重复字段
  次数上限；未知字段按 `UnknownPolicy` 处理：
  - `PolicyReject`（默认）：出现未知字段即返回 `*ValidationError`；
  - `PolicyDiscard`：未知字段直接丢弃，统计在 `MessageStats`；
  - `PolicyQuarantine`：未知字段移出可读视图，单独保留在隔离桶供审计。
  保留编号、类型冲突、重复次数等硬违规在任何策略下都会拒绝。
- `message.go`：`KnownFieldReader` 是下游唯一应依赖的接口，仅暴露已知字段；
  隔离桶不在该接口上，返回切片均为拷贝，防止内部状态被篡改。
- `conversion.go`：`ConversionPlan` 只按显式 `FieldMapping` 跨版本搬运字段，
  绝不按相同编号隐式转发；类型变更必须提供 `Transform`，并对结果再次做类型校验。

## 典型用法

```go
registry := netsec.NewRegistry()
schema, _ := netsec.NewVersionSchema("v1", []netsec.FieldSpec{
    {Number: 1, Name: "user_id", Type: netsec.TypeInt64, Cardinality: netsec.CardinalitySingular},
}, []netsec.NumberRange{{From: 100, To: 109}})
_ = registry.Register(schema)

sanitized, err := netsec.NewSanitizer(schema, netsec.PolicyQuarantine).Sanitize(raw)
if err != nil {
    var ve *netsec.ValidationError
    if errors.As(err, &ve) { /* 记录 ve.Violations 并拒绝消息 */ }
}
var downstream netsec.KnownFieldReader = sanitized // 下游只能读到已知字段

plan, _ := registry.NewConversionPlan("v1", "v2", []netsec.FieldMapping{
    {From: 1, To: 1}, // 显式映射；未列出的字段不会被转发
})
target, _, _ := plan.Convert(sanitized)
```

## 验证

`go test ./...` 覆盖要求的全部场景（外部测试包，模拟真实导入方）：

- 未知字段注入：拒绝 / 丢弃 / 隔离三种策略，下游均读不到注入字段；
- 编号复用：保留编号在任何策略下硬拒绝，跨版本同编号字段不隐式转发；
- 类型冲突与重复次数：类型不符、单值重复、重复超 `MaxCount` 全部拒绝；
- 合法消息：完整通过，仅暴露已知字段且统计正确；
- 版本转换：仅显式映射字段跨版本，类型变更须提供 `Transform`，
  转换结果经目标版本重新校验，下游接口无法触达隔离/未知字段。
