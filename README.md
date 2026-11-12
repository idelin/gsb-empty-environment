# ISO 8583位图与PIN块域准入

攻击者在ISO 8583位图里置未定义字段，或给不该带PIN块的消息夹带52域。给Go收单网关写供报文解码调用的位图准入包：位1置位才读第二位图，字段表由调用方给出，LLVAR与LLLVAR长度不得超过剩余字节和字段上限；52域须恰8字节且只出现在调用方登记的消息类型，日志输出必须置空。用未定义位、长度越界和越权52域验证。

## 用法

准入器在网关启动时按报文规范一次性构造，解码每个报文前先调用 `Admit`：

```go
adm, err := netsec.NewAdmission(
    []netsec.FieldSpec{
        {Number: 2, Kind: netsec.LLVAR, MaxLength: 10},
        {Number: 3, Kind: netsec.Fixed, MaxLength: 6},
        {Number: 52, Kind: netsec.Fixed, MaxLength: 8},
    },
    []string{"0200"}, // 只有这些 MTI 允许携带 52 域
)
if err != nil { /* 配置错误，拒绝启动 */ }

// body 为去掉 MTI/消息头后、从主位图开始的原始字节。
if err := adm.Admit(mti, body); err != nil {
    // errors.Is 可匹配 ErrUndefinedBit / ErrLengthOverflow /
    // ErrPINForbidden / ErrTruncated / ErrInvalidMTI
}
```

## 准入规则

- 主位图固定 8 字节；仅当位 1 置位时才继续读取 8 字节次级位图，位 1 未置位时第 9 字节起按字段数据处理。
- 字段表由调用方登记；位图置位但字段表未登记的字段一律拒绝（`ErrUndefinedBit`），字段按升序消费。
- 定长字段按 `MaxLength` 精确消费；LLVAR 使用 1 字节二进制长度前缀，LLLVAR 使用 2 字节大端长度前缀。
- 变长声明长度同时受字段 `MaxLength` 与报文剩余字节约束，任一越界返回 `ErrLengthOverflow`；前缀或定长数据本身截断返回 `ErrTruncated`。
- 52 域在构造期强制为定长 8 字节（否则 `NewAdmission` 返回 `ErrInvalidConfig`），且只允许出现在调用方登记的 MTI 中，越权携带返回 `ErrPINForbidden`。
- 包内日志出口固定为 `io.Discard` 且不对外暴露，拒绝原因只通过 error 返回，敏感报文内容不落日志。

## 验证

```sh
go test ./... -race -cover
```

测试覆盖未定义主/次级位图位、LLVAR/LLLVAR 剩余字节与字段上限越界、未登记 MTI 夹带 52 域、次级位图门控、截断报文及日志置空断言。
