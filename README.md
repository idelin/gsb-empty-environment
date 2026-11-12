# ACPI表覆盖与地址准入

内核在启动早期会接受来自 initrd 的 ACPI 表覆盖，攻击者控制 initrd 或篡改 XSDT 项就能换掉 DSDT/SSDT 注入 AML。为Go固件检查组件写供启动流程调用的 ACPI 表准入包：表头校验和为 0、长度不小于 36 且不超过所在区域，XSDT 指向的物理地址必须落在内存图的 ACPI 回收或 NVS 区；锁定模式为完整性时拒绝任何来自 initrd 的覆盖；同一签名重复出现、地址回绕与区域外表都拒绝。用错误校验和、地址指向普通 RAM、长度越区、锁定模式切换与合法表验证。

## 启动流程调用

候选表放在 `Candidate` 中，`Data` 是包含 36 字节公共表头的完整原始表，`PhysicalAddress` 是表当前所在的物理地址，`Source` 标明来源。启动流程传入内存图和当前锁定模式：

```go
admitted, err := netsec.Admit(tables, memoryMap, netsec.LockModeIntegrity)
if err != nil {
    // 拒绝启动阶段的 ACPI 覆盖，err 可用 errors.Is 匹配具体原因
}
```

只需要准入结论时可调用 `netsec.Validate(tables, memoryMap, mode)`。`Admit` 是全有或全无：任一候选表不合法就返回 `nil` 和错误。成功后返回 `AdmittedTable`，其中 `Data` 是已校验部分的副本，后续修改调用方切片不会影响已准入的表。

## 强制规则

- 表头校验和按声明的表长计算，所有字节之和必须为 0；声明长度之外的填充字节不参与校验和。
- 声明长度必须不小于 36 字节，且不超过实际提供的字节数。
- 整张表必须完整落在同一个 `MemoryACPIReclaim` 或 `MemoryACPINVS` 区域；普通内存、区域外和跨区表均拒绝。
- `LockModeIntegrity` 下任何 `SourceInitRD` 候选表都直接拒绝；`LockModeNone` 仍逐表执行内容与地址检查。
- 任意两个候选表的四字节签名不得重复，包括 XSDT/RSDT；同一 XSDT/RSDT 也不得重复引用同一地址。
- 表范围、XSDT 指针的 36 字节表头范围和内存区域自身的 `Base+Length` 都做 64 位回绕检查。
- XSDT 的 64 位表项和 RSDT 的 32 位表项必须按各自宽度对齐；每个指针必须指向 ACPI 回收或 NVS 区域。

`Admit` 只能检查指针的地址归属，无法直接读取指针目标的物理内存；若目标表同时作为 `Candidate` 传入，则它的完整长度和校验和也会被检查。
