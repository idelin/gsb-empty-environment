# 段级 GC / Safe ICF 引擎

本仓库提供一个可导入的 Go 包（模块根包，包名 `linker`），实现：

- `--gc-sections`：基于段图有向可达性的精确段回收；
- `--icf=safe` / `--icf=all`：基于 Moore 分区细化的同构代码折叠；
- 折叠后的符号与重定位规范化；
- 内置栈式 IR / 段解释器，用于比较裁剪、折叠前后的函数返回值与
  `ADDR_TAKEN` 指针身份关系。

所有变换都不修改输入 `Image`，而是返回新 `Image` 和包含
`OldToNew` / `Merges` 映射的结果对象。

## 数据模型

```go
import linker "prescreen.local/ps-3df6821022"

img := &linker.Image{
    Sections: []linker.Section{
        {Name: ".text.main", Kind: linker.KindCode, Data: mainCode},
        {Name: ".text.tmpl", Kind: linker.KindCode, Data: tmplCode},
        {Name: ".init_array", Kind: linker.KindData, Flags: linker.SHFKeep},
    },
    Symbols: []linker.Symbol{
        {Name: "main", Section: 0, Value: 0},
        {Name: "tmpl", Section: 1, Value: 0},
    },
    Relocations: []linker.Relocation{
        {From: 0, Offset: 1, Type: linker.RelocCall, Symbol: "tmpl", Size: 4},
        {From: 2, Offset: 0, Type: linker.RelocAddrTaken, Symbol: "tmpl", Size: 8},
    },
    EntrySymbols: []string{"main"},
}
```

- `Sections` 的下标就是 `SectionID`。
- `KindCode` / `KindReadOnlyData` 是 ICF 候选段；`KindData` 不是。
- `SHFKeep`（等价 `SHF_KEEP`、`FlagKeep`）是 GC 强制根。
- `SHFNoICF`、`SHFWritable` 段严禁折叠。
- 重定位 `Type` 必须是 `RelocCall` 或 `RelocAddrTaken`。`ADDR_TAKEN`
  表示目标地址可被程序观察，因此参与 Safe ICF 的函数指针身份保护。
- `Relocation.Size` 是重定位槽宽度；未设置时 `CALL` 默认 4 字节，
  `ADDR_TAKEN` 默认 8 字节。

多目标文件入口：

```go
objects := []*linker.Object{o1, o2, o3}
result, err := linker.LinkObjects(objects, linker.LinkOptions{
    GCSections: true,
    ICF:        linker.ICFSafe,
})
```

`MergeObjects` 会拼接段空间、重绑局部段 ID，并按符号名解析重定位。

## GC：`--gc-sections`

```go
alive, gcRes, err := linker.GCSections(img)
```

存活集合为以下集合的并集：

1. `Image.RootSections`；
2. 带 `SHF_KEEP` 的段；
3. `Image.EntrySymbols` 的定义段；
4. 从上述段出发，沿 `CALL` / `ADDR_TAKEN` 重定位边可达的所有段。

输出只含存活段及这些段中的完整重定位。指向外部未定义符号的重定位原样保留；
被删除段中的重定位随段一起消失。`GCResult` 提供 `Live`、`Dead`、
`OldToNew`、`RootReason` 等结果。

## ICF：Moore 分区细化

```go
folded, icfRes, err := linker.ICF(alive, linker.ICFSafe)
```

### 初始划分

候选段先按以下键做初始划分：

1. 段类别；
2. 把重定位槽清零后的原始字节；
3. 按偏移排序的 `(offset, type, width)` 重定位形状序列。

初始划分**不比较目标符号名**，因此 `f` 调 `f`、`g` 调 `g` 这类自递归段会
落在同一初始类。

### 细化到不动点

每轮把每个重定位目标替换为：

- 目标段当前等价类 ID（候选段）；或
- 目标段固定 ID（不可折叠段）；或
- 外部符号名（未定义符号）；

并带上目标偏移。重新分组后继续迭代，直到等价类数量不再增加。该过程可捕获
自递归、任意长度相互递归环以及多层调用链上的同构关系。

### Safe 规则

ICF 本身把传入镜像中的段视为存活段；`Link` 流水线会先执行 GC，因此 Safe
保护只统计存活段中的 `ADDR_TAKEN` 边。需要独立调用时，应先使用
`GCSections` 的结果作为输入。

设某最终等价类中有：

- `addressed`：存在来自存活段的 `ADDR_TAKEN` 重定位指向它；
- `unaddressed`：不存在这种重定位。

`ICFSafe` 的合并规则：

- 若类中所有段都未取地址：合并为一个代表段；
- 若类中只有一个段被取地址：所有未取地址段都并入该代表段；
- 若多个段都被取地址：这些段各自保留独立地址；所有未取地址段并入其中一个
  已取地址代表段。

因此 Safe ICF 不会把两个可观察的函数指针折叠到同一地址，同时仍允许把任意
多个未取地址的同构模板函数折叠到已取地址代表上。

`ICFAll` 忽略地址身份，把同一等价类全部合并；可写段和 `SHFNoICF` 段仍被
排除。

折叠后会：

- 删除被合并段；
- 把被合并段中的符号改为指向代表段；
- 把所有指向被合并段的重定位（符号式或直接 `Target` 式）重写到代表段；
- 保留代表段上的重定位，并输出 `ICFResult.Canonical` / `Merges` /
  `OldToNew`。

## 解释器与等价性校验

包内置一个确定性栈式 VM。段字节可由 `Assemble` 生成，`CALL` / `ADDR`
操作数就是重定位槽：

```go
code, err := linker.Assemble([]linker.Instruction{
    {Op: linker.OpDup},
    {Op: linker.OpPush, Imm: 0},
    {Op: linker.OpEq},
    {Op: linker.OpJz, Jump: "recurse"},
    {Op: linker.OpDrop},
    {Op: linker.OpPush, Imm: 0},
    {Op: linker.OpRet},
    {Op: linker.OpDup, Label: "recurse"},
    {Op: linker.OpPush, Imm: 1},
    {Op: linker.OpSub},
    {Op: linker.OpCall, Symbol: "sum", Args: 1},
    {Op: linker.OpAdd},
    {Op: linker.OpRet},
})
```

校验入口：

```go
report, err := linker.Validate(before, after, icfRes.OldToNew, linker.ValidateOptions{
    Probes: []linker.CallProbe{
        {Symbol: "sum", Args: []int64{0}},
        {Symbol: "sum", Args: []int64{5}},
        {Symbol: "main"},
    },
})
```

`Validate` 会：

- 对每个 probe 分别在折叠前后执行，比较返回值；指针返回按段映射后比较；
- 枚举所有 `ADDR_TAKEN` 目标地址，逐对比较折叠前后的相等关系；
- 返回 `CallsOK`、`PointersOK`、`OK` 与详细差异。

`Link` 在 `Validate: true` 时会用 GC 前的镜像与最终镜像对比，并只统计 GC
存活段中的调用和 `ADDR_TAKEN` 边，从而同时覆盖裁剪和折叠。Safe 模式下指针
身份改变会被视为错误。`AutoProbes(img)` 可按 `Section.Arity` 为每个代码段
生成默认 probe；设置 `DisableAutoProbes` 可关闭自动 probe。`ICFAll` 模式下
指针身份差异通过 `Validation.OK=false` 报告，而不作为流水线错误。

## 端到端

```go
result, err := linker.Link(img, linker.LinkOptions{
    GCSections:   true,
    ICF:          linker.ICFSafe,
    EntrySymbols: []string{"main"},
    Validate:     true,
})
```

`result.Image` 是最终镜像；`result.GC` / `result.ICF` 包含映射和统计；
`result.SectionMap` 把原始段 ID 映射到最终段 ID。

## 测试

```sh
go test ./...
go test ./... -race -cover
```

测试覆盖不可达段裁剪、`SHF_KEEP` 根保留、外部/直接重定位保留、自递归与互递归
折叠、Moore 多轮细化、`safe` 双取地址保护、未取地址并入已取地址、
`NO_ICF` / 可写段排除，以及解释器返回值与函数指针身份校验。
