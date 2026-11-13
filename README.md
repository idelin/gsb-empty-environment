# Device Mapper表加载校验

存储组件经 `DM_TABLE_LOAD` 向内核加载设备映射表。本包在加载前解析并校验表，拒绝空洞/重叠、环、越界、不安全 crypt 参数和只读设备泄露。

## 使用

```go
devices := netsec.DeviceSet{
	"/dev/sda": {SizeSectors: 4096},
	"/dev/sdb": {SizeSectors: 2048, ReadOnly: true},
}

err := netsec.ValidateTable(table, devices)
if err != nil {
	// 不调用 DM_TABLE_LOAD
}
```

如果表所属映射的名称已知，传入 `WithMappingName` 以拒绝引用自身的环；只读映射需传入 `WithReadOnlyMapping(true)`：

```go
err := netsec.ValidateTable(
	table,
	devices,
	netsec.WithMappingName("secure"),
	netsec.WithReadOnlyMapping(true),
)
```

也可以先调用 `ParseTable` 取得 `[]Segment`，再用 `ValidateSegments` 校验。错误可用 `errors.Is` 匹配 `ErrNotContiguous`、`ErrCycle`、`ErrOutOfBounds`、`ErrInsecureCipher`、`ErrReadOnlyDevice` 等哨兵错误。

## 校验规则

- 每行格式为 `起始扇区 长度 目标类型 参数...`；首个目标必须从 0 开始，后续目标首尾相接且长度非零。
- 目标类型仅允许 `linear`、`crypt`、`verity`、`thin`、`thin-pool`。
- `linear` 和 `verity` 的底层设备必须已登记；底层偏移加映射长度不得越过设备大小，映射总扇区不得溢出 `uint64`。
- `crypt` 拒绝 `cipher_null` 及 `null` 变体；密钥必须是十六进制，且字节长度匹配算法（例如 AES-CBC 为 16/24/32 字节，AES-XTS 为 32/64 字节）。
- 默认为可写映射，因此 `linear` 不得引用只读设备；使用 `WithReadOnlyMapping(true)` 时允许。
