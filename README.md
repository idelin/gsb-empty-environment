# vhost-user客户机地址翻译越界

vhost-user 后端把客户机物理地址翻译成自己的内存映射，恶意客户机提交跨区域或回绕的描述符，能越界读写后端进程内存。为C++后端写可导入的地址翻译包，供虚拟队列处理循环调用：输入 SET_MEM_TABLE 给出的区域表（guest_phys_addr、memory_size、userspace_addr、mmap_offset），区域数不超过 8 且互不重叠；翻译 (gpa, len) 时整段须落在同一区域内，gpa+len 回绕或 len 为 0 拒绝；带 NEXT 的描述符链长度不得超过队列大小，出现环（同一索引重复）拒绝；间接描述符表长度须是 16 的倍数且不超过 queue_size×16，间接表内不得再含 INDIRECT；单条链累计长度超过 2^30 拒绝；区域表更新后旧翻译缓存立即失效。用跨区域、回绕、环、嵌套间接描述符和区域表更换后的旧缓存验证。

## 用法

```cpp
#include "netsec.hpp"

netsec::GuestMemoryMap mem;
mem.setRegions({{guest_phys_addr, memory_size, userspace_addr, mmap_offset}, ...});

netsec::Virtqueue vq(mem, queue_size);
netsec::ChainResult chain = vq.walkChain(desc_table, head_index);
if (chain.ok()) { /* chain.segments 为主机地址片段 */ }
```

- `GuestMemoryMap::setRegions` 校验区域数 ≤ 8、非零长度、无回绕、互不重叠；成功后旧翻译缓存立即失效（代际递增 + 缓存清空），失败时保留旧表。
- `GuestMemoryMap::translate(gpa, len)` 拒绝 `len == 0`、`gpa+len` 回绕、以及任何不落在单一区域内的范围。
- `Virtqueue::walkChain` 拒绝越界索引、超队列长度的链、环、非法间接表长度、嵌套 INDIRECT、单链累计长度超 2^30 及无法翻译的描述符地址。

## 构建与测试

```sh
cmake -S . -B build && cmake --build build
ctest --test-dir build --output-on-failure
```
