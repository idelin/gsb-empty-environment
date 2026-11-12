# 二进制签名覆盖空隙

签名格式允许排除某些文件区间，加载器可能仍执行未签覆盖字节。给Go验收宿主实现覆盖核验包，输入签名覆盖区间、加载映射和可执行地址区间，组合偏移转换求所有可执行但未认证字节；映射可重叠，任一可执行来源缺失都报告。用小文件字节枚举核对零填充、段外填充和别名映射。

## 包说明

`netsec.VerifyCoverage(signed, mappings, execRanges)` 输入三类半开区间并返回所有
“可执行但未被签名认证”的地址区间 `[]Gap`：

- `FileRange`：签名覆盖的文件字节区间 `[Start, End)`。
- `Mapping`：加载映射，`[VAddr, VAddr+MemSize)` 中前 `FileSize` 字节来自文件
  `[FileOff, FileOff+FileSize)`，其余为加载器零填充（如 `.bss`）。
- `AddrRange`：加载器标记为可执行的虚拟地址区间。

缺口原因（`GapReason`）：

- `unsigned-file`：文件支撑字节的文件偏移落在签名覆盖之外。
- `zero-fill`：超出文件内容的加载器零填充字节，不可能出现在签名文件中。
- `unbacked`：可执行地址没有任何映射来源。

映射允许重叠或别名同一文件区域；每个映射作为独立可执行来源单独核验，任一来源
未认证都会单独报告，不会被另一个已签名的重叠映射掩盖。区间内部做排序、合并与
溢出校验，输出按地址排序并合并相邻同类缺口。

## 验证方式

`coverage_test.go` 内置一个独立的逐字节枚举预言机（不复用生产代码的区间运算），
在小文件上逐地址分类，并与区间实现逐字节比对：

- 零填充：`TestZeroFillBeyondFile`
- 段外填充：`TestPaddingOutsideSegment`
- 别名/重叠映射：`TestAliasOverlap`、`TestOverlappingMappingsOneUnsigned`
- 偏移转换与排除区间：`TestOffsetConversionSignedSubrange`、`TestExcludedIntervals`
- 随机化属性测试：`TestRandomizedAgainstOracle`（500 个随机小布局）

运行：

```sh
go test ./... -v
go test -cover ./...
```
