# CIP路径规范化准入

攻击者用16位逻辑段表示同一个CIP类，绕开只认8位编码的策略去设置EtherNet/IP的TCP/IP接口对象。给Go网关写供请求准入调用的路径规范化包：先把EPATH解成类、实例、属性并核对路径字长，再按调用方的服务码与对象白名单判断；Unconnected Send内嵌消息递归检查且深度有限；0xF5类的Set_Attribute_Single默认拒绝。用16位段、嵌套转发和字长错位验证。

## 包结构（`package netsec`）

- `epath.go`：`NormalizeEPATH(pathSizeWords, data)` 按 CPF 字长解析 EPATH，
  支持 8/16/32 位逻辑段，统一还原成 `LogicalPath{Class, Instance, Attribute}`，
  并对字长错位、截断、非法填充、非逻辑段、段顺序/重复做严格校验。
- `policy.go`：`Policy.Admit(*Request)` 按服务码 + 类白名单做默认拒绝判断；
  `DefaultPolicy()` 内置身份(0x01)、消息路由(0x02)、连接管理器(0x06)、
  TCP/IP 接口(0xF5) 规则，其中 0xF5 的 `Set_Attribute_Single`(0x10) 硬拒绝。
  `Unconnected Send`(0x52) 只允许发往连接管理器，内嵌消息递归检查，
  深度受 `Policy.MaxDepth`（默认 `DefaultMaxDepth=4`）限制。

## 网关接入示例

```go
policy := netsec.DefaultPolicy()
decision := policy.Admit(&netsec.Request{
    Service:       serviceCode,       // 自动屏蔽最高位响应标志
    PathSizeWords: cpfPathSizeWords,  // 报文中的 PATH Size（16 位字数）
    PathData:      cpfPathAndBody,    // 路径原始字节，可携带后续请求体
})
if !decision.Allowed {
    // decision.Reason / decision.Err 用于审计与 CIP 错误码映射
    return decision
}
```

## 验证

`go test ./...` 覆盖：8/16/32 位同类号规范化等价（绕过复现）、混合编码、
字长偏大/偏小与段截断、非法填充、嵌套 Unconnected Send 递归拦截与深度上限、
0xF5 写操作硬拒绝等 26 个用例。
