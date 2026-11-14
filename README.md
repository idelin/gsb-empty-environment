# netsec：软件标签传播运行时

硬件内存标签通常只在最终的 load/store 上比较标签。攻击者若对指针做算术、
类型转换或整数往返，就可能让一个带旧标签的地址指到另一个对象，从而绕过
只检查访存指令的硬件。`netsec` 在软件层把“对象标签 + 基址 + 大小 +
代际”作为指针的不可分割属性传播，并在每次访存和系统调用入口重新校验。

## 设计

- **对象注册**：`netsec_register_object` 为一段连续内存生成随机 64 位标签，
  记录基址、大小和单调代际。重叠注册会使旧标签立即失效。
- **指针描述符**：`netsec_ptr` 保存 `address/tag/object_base/object_size/
  generation`。指针算术只移动 `address`，其余字段随指针一起传播。
- **访问校验**：校验时根据标签查注册表，再按地址查当前区间。地址落入
  另一个对象、标签不符、标签已失效、范围越界或来源字段被伪造都会触发
  陷阱。特别地，`A + n` 指到对象 `B` 时会保留 `A` 的标签，因此首次解引用
  即报 `cross-object tag mismatch`。
- **转换**：C++ `tagged_ptr<T>` 的隐式/显式类型转换保留标签、边界和代际；
  C ABI 的 `netsec_ptr_convert` 供编译器插桩使用。
- **标签剥离**：`strip_tag()` / `netsec_ptr_strip` 产生无标签指针，任何
  非零长度访问都会以 `untagged pointer` 失败关闭。
- **系统调用边界**：`netsec_syscall_revalidate` 和 `netsec_copy_from_user` /
  `netsec_copy_to_user` 不信任用户态缓存结果，在边界重新查询存活对象和
  完整范围，防止任务切换期间的对象失效或标签替换。
- **陷阱策略**：默认抛出可捕获的 `netsec::tag_violation`（含原因、地址、
  期望/实际标签），便于运行时和测试处理；生产环境可安装 handler 直接终止，
  或使用 `-DNETSEC_TRAP_ABORT=ON` 让陷阱在 handler 返回后调用 `abort()`。

## C++20 用法

```cpp
#include "netsec.hpp"

int values[8] = {};
auto p = netsec::tag_object(values);      // 注册对象并得到带标签指针

*p = 1;                                   // 合法访问
auto q = p + 3;                           // 标签、对象边界随算术传播
*q = 2;                                   // 仍在 bounds 内，合法

netsec::tagged_ptr<unsigned char> bytes = q;   // 隐式转换不剥标签
bytes.require(1);                               // 访问前校验

auto stripped = p.strip_tag();
*stripped;                                // 陷阱：untagged pointer

auto one_past = p + 8;
one_past.check(0);                        // true：可形成尾后指针
one_past.check(1);                        // false：不允许越界访问
*one_past;                                // 陷阱：object bounds violation
```

跨对象示例：

```cpp
alignas(64) std::byte storage[64];
auto a = netsec::tag_bytes(storage, 32);
auto b = netsec::tag_bytes(storage + 32, 32);   // 相邻、独立标签

auto moved = a + 32;      // 地址进入 b，但仍携带 a 的标签
*moved;                   // 陷阱：cross-object tag mismatch
```

### 主要 API

- `netsec::tag_object(array)` / `tag_object(ptr, count)` / `tag_bytes(ptr, n)`
- `netsec::tagged_ptr<T>::add/sub`、`operator+/-`、`operator[]`
- `netsec::implicit_cast<U>(p)`、`tagged_ptr<U>(p)`、`netsec_ptr_convert`
- `tagged_ptr<T>::check(bytes)`（不抛异常）与 `require(bytes, access)`（陷阱）
- `tagged_ptr<T>::strip_tag()`、`netsec_ptr_strip`
- `tagged_ptr<T>::from_address(raw)`、`netsec_ptr_from_address`
- `netsec::syscall_revalidate`、`copy_from_user`、`copy_to_user`
- RAII 注册：`netsec::object_scope scope(memory, bytes);`

注意：`add` / `sub` 与 C ABI 接受**字节**偏移，`operator+` / `operator-`
接受**元素个数**；`checked_add` / `checked_sub` 在算术后立即要求地址仍在
对象范围内（尾后零长度仍允许）。

### 边界语义

- `[base, base + size)` 内可访问；`base + size` 可形成尾后指针，但零长度
  校验之外不允许解引用或跨越。
- 校验先与注册表中实际区间比较，不信任指针自带的 `object_base` /
  `object_size`；这些字段被篡改会得到 `forged pointer provenance`。
- 对象注销或同址重新注册后，旧标签会得到 `stale object tag`。

## C ABI

`netsec.h` 是稳定的 C 接口，所有符号均以 `netsec_` 开头，可被 C++20 运行时
或其他语言导入：

```c
netsec_object object = netsec_register_object(buffer, size);
netsec_ptr p = netsec_ptr_from_object(object);
p = netsec_ptr_add(p, byte_offset);
netsec_ptr_require(p, access_bytes, NETSEC_ACCESS_READ);
```

核心导出：

- `netsec_register_object` / `netsec_unregister_object` / `netsec_object_alive`
- `netsec_ptr_add` / `_sub` / `_convert` / `_strip` / `_reassociate`
- `netsec_ptr_check` / `netsec_ptr_require` / `netsec_bounds_require`
- `netsec_syscall_revalidate` / `netsec_syscall_map` / `netsec_copy_from_user`
- `netsec_set_trap_handler` / `netsec_raise_trap`

## 编译器插桩约定

插桩只需在会改变指针来源或发生访存的位置调用运行时，不要求改写每一个
普通加载：

| IR / C++ 位置 | 运行时调用 |
| --- | --- |
| `getelementptr`、指针加减 | `netsec_ptr_add` / `netsec_ptr_sub` |
| 指针类型转换、隐式转换 | C++ 转换构造或 `netsec_ptr_convert` |
| `ptrtoint` / 显式剥离 | `netsec_ptr_strip`（返回值保持无标签） |
| `inttoptr` / 恢复已注册地址 | `netsec_ptr_from_address` 或 `netsec_ptr_reassociate` |
| `load` / `store` | `netsec_ptr_require(..., READ/WRITE)` |
| 用户态/内核态边界 | `netsec_syscall_revalidate` 后再复制 |
| 对象进入/离开作用域 | `netsec_register_object` / `netsec_unregister_object` |

隐式转换的关键约束：C++ 层应在 `tagged_ptr<T>` 之间使用转换构造函数，
不要先转成裸 `T*` 再转回；工具链对原始指针的隐式转换必须调用
`netsec_ptr_from_address`（若可证明来自已注册对象），否则调用
`netsec_ptr_strip` 并让后续访问失败关闭。

## 构建与测试

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build
ctest --test-dir build --output-on-failure
```

测试覆盖：合法读写、指针算术与指针差、隐式/显式转换、标签剥离、跨对象
访问、尾后与越界检查、陈旧标签/同址重注册、系统调用 copy-in/copy-out
重校验，以及陷阱 handler 观测。

安装后可通过 `find_package` 风格导入 `netsec::netsec` 目标（安装导出文件为
`netsecTargets.cmake`）。
