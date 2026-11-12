# ionfilter

读取侧 Ion 安全过滤包，在把不可信摄取流交给普通 Ion 解码器之前做策略校验。
零第三方依赖，同时支持文本 Ion 与二进制 Ion。

## 威胁模型

攻击者在上传内容中写入带 `$ion_symbol_table` 注解的结构，重绑后续符号 ID，
使原本表示角色的字段解析到另一个名字。过滤器在解码前强制执行三条不变量：

1. 符号表指令（`$ion_symbol_table` / `$ion_shared_symbol_table`，含用符号 ID
   `$3` / SID 3 的写法）只在调用方显式标记为受信的段内生效；出现在用户数据
   中一律拒绝，返回 `KindSymbolTable`。
2. IVM（文本 `$ion_1_0`、二进制 `E0 01 00 EA`）只能出现在逻辑流最前面；流中
   再次出现即拒绝，返回 `KindIVM`。
3. 任何值上的注解都必须能通过受信符号表解析，且文本在调用方提供的白名单内；
   无法解析或不在白名单的注解拒绝，返回 `KindAnnotation`。

无法满足 Ion 结构的输入返回 `KindMalformed`。所有错误均为 `*Violation`，可用
`errors.As` 或 `IsViolation` 识别。

## 用法

```go
filter := ionfilter.New(
    []string{"audit"}, // 允许出现在值上的注解文本
    ionfilter.TrustedSegment(serviceHeader), // 服务自管前缀，允许 LST 指令
    ionfilter.UserSegment(uploadedBytes),    // 上传数据，禁止任何 LST 指令
)
if err := filter.Validate(); err != nil {
    // 拒绝摄取，记录违规类型与偏移
}
```

单段便捷入口：

```go
err := ionfilter.Validate(data, false /* untrusted */, []string{"audit"})
```

## 设计说明

- 段边界按字节偏移维护，策略判定使用值的起始偏移，因此跨段空白不会改变归属。
- 受信 LST 的 `symbols` 列表会真实绑定符号，使后续符号 ID 注解按受信映射解析；
  未解析的符号 ID 一律失败关闭，不会因解析不出文本而绕过白名单。
- 过滤器只做结构与策略校验，不替代下游解码器对标量语义的解析。
