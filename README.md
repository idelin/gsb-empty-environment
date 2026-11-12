# VTL0页权限W^X审核

虚拟化安全模式里，VTL1 的安全内核要审核 VTL0 内核提交的页权限变更，被攻陷的 VTL0 会申请把可写页改成可执行，或改写已验证的代码页。为C++17库实现供安全内核处理超调用时调用的页权限审核组件：登记已验证签名镜像的页范围与内容哈希；VTL0 请求可执行只允许已验证范围内且内容哈希未变的页；任何页不得同时可写可执行；已验证代码页永不可写，数据页请求可执行一律拒绝；范围起点与页数按页对齐，起点加页数回绕拒绝。用 W+X、改写已验证页、范围回绕和合法代码页重映射验证。

## 构建与测试

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

## 核心接口

```cpp
netsec::PagePermissionAuditor auditor(4096);

// 由 VTL1 在验证签名后登记。hashes 按页地址升序，每页一个摘要。
auditor.register_verified_pages(
    netsec::PageRange{0x10000000, 3}, netsec::PageType::Code,
    {code_hash_0, code_hash_1, code_hash_2});

// 审核 VTL0 超调用。observed_hashes 必须由 VTL1 自行测量，
// 不能直接信任超调用载荷。
netsec::PagePermissionRequest request;
request.range = {0x10000000, 3};
request.access = netsec::PageAccess::ReadExecute;
request.observed_hashes = {code_hash_0, code_hash_1, code_hash_2};

auto result = auditor.audit_hypercall(request);
if (!result.allowed) {
    // 拒绝原因见 result.reason，命中页见 result.offending_page。
}
```

`PageAccess` 表示请求完成后的完整权限集合，不是增量。审核器保证：

- 含 `Write | Execute` 的请求一律以 `WriteExecuteConflict` 拒绝。
- `Execute` 只允许已登记的 `PageType::Code` 页，且每一页的实测摘要必须与登记摘要一致；未登记页返回 `UnverifiedExecute`，数据页返回 `DataPageExecute`，摘要变化返回 `ContentHashMismatch`。
- 已验证代码页的任何写请求返回 `VerifiedCodeWrite`；数据页允许读写。
- 范围起点必须按页对齐，页数为零、页数乘法溢出、`base + page_count * page_size` 回绕均拒绝。
- 已验证范围不允许重叠登记，登记时哈希数量必须与页数一致。

无 `Execute` 的请求不要求摘要（摘要被忽略），因此数据页写入和只读重映射无需重新测量。

若超调用以字节长度描述范围，调用方必须先确认长度是 `page_size` 的整数倍，再换算为 `PageRange::page_count`；非页对齐长度不能进入审核器。

## 信任边界

组件只做策略判定，不计算摘要。调用方必须在 VTL1 内、在审核与权限应用之间对页内容进行测量并锁定页面，避免 TOCTOU。本类不提供内部同步，需由安全内核串行调用。

## 测试覆盖

`netsec_test.cpp` 覆盖：

- 已验证代码、已验证数据、未登记页上的 W+X 请求；
- 改写已验证代码页后的可执行请求；
- 页对齐、零页、乘法溢出和 `base + count` 回绕；
- 未改动的已验证代码页重映射为可执行；
- 数据页可执行、未登记页可执行、代码页可写、哈希数量错误和登记重叠。
