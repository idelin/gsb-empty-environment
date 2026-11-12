# SMM通信缓冲校验

SMI 处理程序收到的通信缓冲位于操作系统内存，攻击者可把指针指向 SMRAM、让长度回绕，或在校验后修改缓冲内容再让处理程序读取。为C++17 MM 服务写供 SMI 分发调用的缓冲校验库：缓冲与其内嵌指针指向的区域都必须整体位于 SMRAM 与受保护范围之外，base+length 用溢出安全运算，先整体复制进 SMRAM 再解析，消息长度不得超过缓冲减头部，GUID 不在处理程序白名单直接丢弃。用指向 SMRAM、回绕、校验后改写、嵌套指针和长度越界验证。

## 设计

核心代码位于 `smm_buffer_guard.hpp` / `smm_buffer_guard.cpp`，命名空间
`netsec::smm`，不抛异常、不使用 RTTI，适合 SMI 处理路径。

处理顺序即防御顺序，任何一步失败立即返回错误状态，不解析、不解引用：

1. **整体范围校验**：外部通信缓冲先过 `validate_external_range`——非空、非零长、
   不超过 `max_buffer_size`、`base+length` 溢出安全、与 SMRAM 及全部受保护范围
   无任何重叠（半开区间，相邻不算重叠）。
2. **先复制再解析**：校验通过后把**整个**缓冲一次性复制进私有 SMRAM 暂存内存，
   之后所有字段（GUID、长度、载荷）只从私有快照读取，杜绝校验后改写（TOCTOU）。
3. **头部/长度边界**：缓冲小于 `kCommHeaderSize` 返回 `TooSmall`；
   `message_length == 0` 拒绝；`message_length > 外部长度 - 头部` 返回
   `MessageTooLong`。
4. **GUID 白名单**：白名单外的 GUID 返回 `GuidNotAllowed` 直接丢弃；白名单为空时
   `BufferGuard::create` 直接构造失败（fail-closed）。
5. **嵌套指针**：`Snapshot::snapshot_nested` 对内嵌指针指向的区域重复同一套
   范围校验，再单独复制进新的私有暂存块，处理程序永远不直接解引用 OS 指针。

溢出安全加法 `checked_add_range` 优先使用 `__builtin_add_overflow`，并提供纯
C++17 标准库回退（`numeric_limits::max() - base` 比较）。

## 主要类型

- `Guid` / `MemoryRange` / `CommHeader`：与 `EFI_GUID`、地址区间、
  `EFI_MM_COMMUNICATE_HEADER` 对应的标准布局类型。
- `BufferGuard`：持有 SMRAM 范围、额外受保护范围、GUID 白名单和最大缓冲尺寸；
  `accept(base, length)` 返回 `Result<Snapshot>`。
- `Snapshot`：私有暂存缓冲的移动语义持有者，提供 `guid()`、`message()`、
  `buffer()` 视图与 `snapshot_nested()`。
- `IStagingAllocator`：SMRAM 暂存分配接口。固件实现应封装
  `SmmAllocatePool(..., EfiRuntimeServicesData, ...)`；宿主测试用
  `HeapStagingAllocator`（禁止用于真实 SMI 构建）。
- `Status` / `Result<T>`：无异常的错误传播。

## 处理程序用法

```cpp
using namespace netsec::smm;

// 初始化一次：SMRAM、其它受保护范围、本处理程序接受的 GUID。
auto guard = BufferGuard::create(
    MemoryRange{smram_base, smram_length},
    {MemoryRange{mmio_base, mmio_length}},
    {kMyHandlerGuid},
    /*max_buffer_size=*/4096);
if (!guard) { return; }  // 配置非法或白名单为空，fail-closed

// 每次 SMI：CommBuffer 物理/虚拟地址与长度来自 MM 核心。
auto staged = guard->accept(
    reinterpret_cast<uintptr_t>(comm_buffer), comm_buffer_size);
if (!staged) {
    return;  // 直接丢弃，status() 给出原因
}

Span message = staged->message();  // 仅指向 SMRAM 私有副本

// 载荷里的内嵌指针，先校验+复制再使用。
OwnedBuffer nested;
if (staged->snapshot_nested(payload->ptr, payload->len, nested) != Status::Ok) {
    return;
}
```

## 与 EDK2 / SMM 集成要点

- 受保护范围至少包含 SMRAM（可由 `SmmGetSmramRanges` / 启动期记录得到），
  建议再加入私有 MMIO、SEcrets 页等；`create` 会拒绝零长、回绕、互相重叠的配置。
- 将 `IStagingAllocator` 实现为 `SmmAllocatePool` / `SmmFreePool` 封装，
  确保 `allocate` 失败返回 `nullptr` 而不抛异常；暂存内存须位于 SMRAM。
- 真实固件中 `CommHeader` 的 `size_t` 与 EDK2 `UINTN` 同宽，头部布局与
  `EFI_MM_COMMUNICATE_HEADER` 一致；如需严格字节序/对齐，可在解析处保留现有
  `memcpy` 读法（已避免别名与对齐问题）。
- 本库做的是“黑名单之外 + 白名单 GUID”的指针/长度防线，与 EDK2 的
  `SmmIsBufferOutsideSmmValid` 语义一致并额外提供整体快照与嵌套指针处理。

## 构建与测试

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Debug
cmake --build build
ctest --test-dir build --output-on-failure
```

测试默认带 `-Wall -Wextra -Wpedantic` 与 ASan/UBSan（`NETSEC_ENABLE_SANITIZERS`
可关）。`tests/test_smm_buffer_guard.cpp` 覆盖全部要求的攻击向量：

| 攻击场景 | 测试 |
| --- | --- |
| 指针指向 SMRAM / 受保护范围（含跨越边界） | `test_smram_and_protected_ranges`、`test_nested_pointer` |
| `base+length` 回绕 | `test_checked_add_range`、`test_smram_and_protected_ranges` |
| 校验后改写（TOCTOU） | `test_post_validation_mutation`、`test_nested_pointer` |
| 嵌套指针 | `test_nested_pointer` |
| 消息/缓冲长度越界 | `test_length_bounds` |
| 非白名单 GUID | `test_guid_whitelist` |
| 暂存分配失败 | `test_allocator_failure` |
