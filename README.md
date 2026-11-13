# 空闲链表指针混淆与对齐校验

攻击者通过悬空指针改写已释放块的 next，再让分配器把任意地址当作下一块。为C++分配器写可导入的 tcache 式空闲链包：存放时 next 编码为 (所在地址 >> 12) XOR 真实指针，取出时还原并检查 16 字节对齐，不对齐则终止；释放时块内 key 等于本分配器随机值才遍历链检测重复释放；给定一串 alloc、free 与写 next 操作，输出每步结果与终止原因。用重复释放、改 next 为未对齐地址和正常交替验证。

## 组成

- `netsec.hpp` / `netsec.cpp`：可导入的 `netsec` 库
  - `protect_ptr` / `reveal_ptr`：safe-linking 编解码，`(pos >> 12) ^ ptr`
  - `TcacheAllocator`：固定块 LIFO 空闲链；`alloc` 弹出时还原 next 并检查 16 字节对齐，`free` 时块内 key 命中本分配器随机值才遍历链查重复释放；违规抛出 `SecurityViolation`（真实部署等价于 `abort()`）
  - `write_next(chunk, target)`：模拟攻击者经悬空指针改写已释放块的 next
- `driver.cpp` → `tcache_driver`：逐行执行 `alloc <名>` / `free <名>` / `write <名> <地址>`，输出每步结果与终止原因
- `tests.cpp` → `tcache_tests`：重复释放、未对齐 next、正常交替三组场景
- `examples/*.ops`：三个场景的示例操作序列

## 构建与验证

```sh
cmake -S . -B build && cmake --build build
cd build && ctest --output-on-failure
./tcache_driver ../examples/double_free.ops     # TERMINATED: double free detected in tcache
./tcache_driver ../examples/unaligned_next.ops  # TERMINATED: unaligned tcache chunk detected
./tcache_driver ../examples/normal.ops          # completed: 8 steps, no fault
```
