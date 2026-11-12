# CO-RE重定位记录校验

CO-RE 加载器按目标内核的 BTF 重写字段偏移，攻击者提供的 BPF 对象可以带伪造的重定位记录，让偏移落到结构体之外或把 u8 字段当 u64 读。为Go加载器写供程序加载路径调用的重定位校验包，BTF 与重定位记录由调用方给出：访问串如 0:1:2 必须逐段落在成员内，字段类型种类与大小须与本地兼容，候选类型有多个且结果不一致时报错，字段不存在只允许用于存在性检查；重写后的偏移加访问宽度不得超出结构体大小。用越界索引、尺寸不兼容、多候选冲突、位域和合法样例验证。

## 用法

调用方把解析后的本地 BTF、目标内核 BTF 和重定位记录交给 `Validate`。
每个 `Relocation` 的 `LocalTypeID` 指向本地类型，`Access` 是
`0:1:2` 形式的本地成员索引串，`AccessSize` 是加载/存储指令的访问宽度
（字节）。目标候选默认按根类型的 kind 与名称查找，也可以用
`TargetTypeIDs` 显式指定。

```go
local := netsec.NewBTF()
target := netsec.NewBTF()

relos := []netsec.Relocation{{
	Kind:        netsec.RelocFieldByteOffset,
	LocalTypeID: 6,
	Access:      "1:1:1",
	AccessSize:  8,
}}

patches, err := netsec.Validate(local, target, relos)
if err != nil {
	// 拒绝加载对象
}

for _, patch := range patches {
	// patch.Offset/patch.BitOffset 是目标根类型内的新位置
	// patch.Size 是目标字段占用的字节数
}
```

## 校验规则

- 本地访问串的每一段都必须是现有成员索引，且成员完整落在所属结构/联合体内。
- 目标成员按名称匹配；缺失只允许出现在 `RelocFieldExists` 中，其余重定位返回 `ErrFieldNotFound`。
- 字段 kind、整数编码/位宽、枚举和浮点大小必须与本地兼容；复合类型目标尺寸不得小于本地尺寸。
- 所有候选类型必须得到一致结果，否则返回 `ErrAmbiguousCandidates`。
- 重写偏移加访问宽度不得越过目标根结构大小；位域要求从字节对齐位置开始的加载区间完整覆盖位域，同时不能越过根结构。
- 所有错误都可用 `errors.Is` 对 `ErrInvalidBTF`、`ErrIndexOutOfBounds`、`ErrTypeIncompatible`、`ErrAccessOutOfBounds` 等哨兵值检查。
