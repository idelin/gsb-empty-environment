# io_uring固定缓冲租约

存储服务把网络请求里的偏移和长度直接填进io_uring的READ_FIXED和WRITE_FIXED，攻击者可借整数回绕越出已注册缓冲区，或在请求在途时让缓冲被重新登记。写供C++存储服务调用的固定缓冲租约库：校验buf_index，以及地址加长度时不得溢出，范围须完整位于该缓冲内；租约绑定租户，在完成事件到达前阻止更新或注销该缓冲；完成后短读的剩余区域清零再回收。测试使用模拟提交队列，覆盖长度为零、地址加长度回绕、重复完成和跨租户租约。

## 组件

- `netsec.hpp` / `netsec.cpp`：`FixedBufferLeaseManager`，固定缓冲注册、租约发放与回收，全部接口带租户校验并由互斥锁保护。
- `tests/mock_submission_queue.hpp`：实现 `ISubmissionQueue` 的模拟提交队列，记录提交的 `IoDescriptor` 并按 FIFO 产出完成事件。
- `tests/lease_test.cpp`：13 个功能用例。
- `tests/concurrency_stress_test.cpp`：多线程租约/完成/更新竞态压测。

## 安全保证

- `buf_index` 必须已注册，否则返回 `InvalidBufIndex`。
- `offset + length` 使用 64 位溢出检查，回绕返回 `OffsetOverflow`；通过后再要求范围完整落在缓冲内（允许 `length == 0` 且 `offset <= size`），否则返回 `OutOfRange`。
- 租约与缓冲均绑定租户，跨租户获取、完成、释放、更新、注销统一返回 `AccessDenied`。
- 缓冲存在在途租约时，`update_buffer` / `unregister_buffer` 返回 `BufferBusy`。
- `READ_FIXED` 短读时把 `[offset + 已读, offset + length)` 清零再回收；满读不清零，写操作不清零。
- 完成事件先校验 `0 <= result <= length`；同一租约重复完成返回 `AlreadyCompleted`，在途计数不会二次递减。
- 提交队列拒绝时回滚租约与在途计数，返回 `QueueRejected`。

## 构建与测试

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

Sanitizer 验证（可选）：

```sh
cmake -S . -B build-asan -DCMAKE_CXX_FLAGS="-fsanitize=address,undefined" \
      -DCMAKE_EXE_LINKER_FLAGS="-fsanitize=address,undefined"
cmake -S . -B build-tsan -DCMAKE_CXX_FLAGS="-fsanitize=thread" \
      -DCMAKE_EXE_LINKER_FLAGS="-fsanitize=thread"
```
