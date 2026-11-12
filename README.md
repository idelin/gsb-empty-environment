# LUKS2元数据与keyslot绑定

攻击者改写 LUKS2 头部 JSON，让 keyslot 区域重叠、digest 绑到别的 keyslot，或塞超大 KDF 参数拖垮解锁。为Go磁盘库写可导入的元数据校验包：JSON 同名键即拒；keyslot 的 area 须在 keyslots 区内且互不重叠；digest 引用的 keyslot 与 segment 须存在且双向一致；PBKDF 的 time、memory、cpus 超调用方上限拒绝；头部 SHA-256 校验和按校验和字段置零后重算。测重复键、区域重叠、悬空引用、超限 KDF。

## `luks2` 包

导入路径：`example.org/netsec/q0557/luks2`

一条入口完成全量校验，校验和不通过时不会信任任何 JSON 内容：

```go
limits := luks2.KDFLimits{
    MaxTime:   10,
    MaxMemory: 1 << 20, // KiB
    MaxCPUs:   8,
}
bin, meta, err := luks2.ParseHeader(rawHeader, limits)
```

也可按需调用单级 API：

- `VerifyChecksum(raw)`：将偏移 448 起的 32 字节校验和字段置零，对前 `header_size` 字节重算 SHA-256 并常量时间比对。
- `ParseMetadata(json)`：逐 token 扫描，任何层级出现同名键即返回 `ErrDuplicateKey`。
- `ValidateAreas(meta, headerSize)`：area 必须落在 `[2*headerSize, 最早 segment 偏移)`，且两两区间不重叠（相邻允许）。
- `ValidateBindings(meta)`：digest 引用的 keyslot/segment 必须存在且未回收，同时每个活动 keyslot/segment 都必须被某个 digest 反向引用。
- `ValidateKDF(meta, limits)`：`time`、`memory`、`cpus`（及 PBKDF2 的 `iterations`）超过调用方上限返回 `ErrKDFLimit`，零值表示不限。

错误均为可 `errors.Is` 分类的哨兵错误：`ErrDuplicateKey`、`ErrAreaOutOfBounds`、`ErrAreaOverlap`、`ErrDanglingReference`、`ErrBindingInconsistent`、`ErrKDFLimit`、`ErrBadHeader`。

运行测试：`go test ./...`
