# tagunion — 标签联合编解码（仅标准库）

每个标签分支有独立字段 schema；编码前验证标签与载荷一致；未知标签原样透传，
绝不假装理解；分支 schema 通过显式演进映射升级；批量对象原子验证，
错误定位分支路径与字节偏移。

## 帧格式

```
tag      uvarint
version  uvarint
length   uvarint          载荷字节数，先验证 ≤ MaxPayloadLen 再分配
payload  [length]byte
checksum uint32 小端      CRC32-IEEE(tag‖version‖length‖payload)
```

批量格式：`uvarint 对象数` + 各对象帧顺序拼接。

## 支持的序列化范围（显式定义）

- 字段类型仅 `uint`(uvarint)、`int`(zigzag varint)、`bool`、`string`、`bytes`
- 整数值域 uint64 / int64 全范围；string ≤ 64 KiB；bytes/载荷 ≤ 1 MiB
- 批量对象数 ≤ 65536；不支持浮点、嵌套、指针/接口等宿主类型
- 解码为纯数据解析：不执行宿主代码，所有长度先验证再分配

## 语义要点

- **标签与载荷一致性**：`Registry.Encode` 要求字段名/类型/数量与 schema 完全匹配，
  标签 A 搭标签 B 的字段会被拒绝；空分支（零字段 schema）合法。
- **显式演进**：`(tag, version)` 注册 schema，`RegisterMigration` 注册 v→v+1 迁移函数；
  缺失迁移链即报错，旧 schema 绝不按巧合字段解读新数据。
- **未知标签透传**：未知标签/版本解码为 `KindUnknown`，保留原始版本号、载荷与完整帧
  （含校验），`Marshal` 逐字节重放，重编码与原字节一致。
- **批量原子性**：`DecodeBatch` 任一对象失败则整体失败、不返回部分结果；
  错误携带 `obj[i]` / `obj[i].field(name)` 路径与字节偏移。

## 运行

```sh
go test ./tagunion/ -v   # 测试（含多版本往返矩阵与独立解析器交叉验证）
go run ./cmd/demo        # 运行样例
```
