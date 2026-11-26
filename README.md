# CCM — 机密计算 Enclave 边界校验 / 安全深拷贝 / 双重读取对抗库

一个可嵌入、零外部依赖（仅使用 C++17 标准库）的头文件库，用于在
SGX / SEV-SNP 等机密计算运行时中，安全处理不可信宿主机经由共享内存
（OCALL/ECALL 缓冲区）传入的指针、嵌套结构体与变长数据。

所有代码以
`-std=c++17 -Wall -Wextra -Wconversion -Wsign-conversion -Werror -pedantic`
编译通过。

## 威胁模型

宿主机是完全不可信的：它可以在 Enclave 两次读取共享页之间的任意时刻
并发覆写共享内存中的字节。两类典型攻击：

- **Double-Fetch TOCTOU**：第一次读取用于校验（长度/指针合法），第二次
  读取用于实际解引用；宿主在校验与使用的间隙篡改长度，诱导越界读写。
- **指针替换（Pointer Swap）**：校验时指针指向合法外部缓冲区，使用前被
  替换为 Enclave 私有地址，诱导越权读取私有内存。

库的安全目标：在任意并发篡改交错下，保证 Enclave 私有内存零越权访问，
且业务逻辑始终基于一份自洽、稳定的快照。

## 架构与模块

所有模块位于 `include/ccm/`，均为头文件、零第三方依赖。

| 头文件 | 职责 |
| --- | --- |
| `types.hpp` | 地址/长度/定宽整数类型别名，统一无符号指针运算 |
| `checked_math.hpp` | 防整数溢出的加法/乘法（`__builtin_*_overflow`，带可移植回退） |
| `status.hpp` | 不抛异常的统一 `Status` / `Result<T>` 错误上报 |
| `address_space.hpp` | 建模私有区间 `[encBase, encEnd)` 与一个或多个外部共享池 |
| `auditor.hpp` | 严格指针区间审计器（核心审计原语） |
| `shared_bus.hpp` | 带监控/篡改钩子的共享内存总线与 `ReadOnceBus` 读一次包装 |
| `schema.hpp` | 声明式 ABI 类型/字段/嵌套指针/变长尾部描述与静态校验 |
| `abi_decode.hpp` | 无对齐假设的小端标量编解码 |
| `marshaller.hpp` | 单次快照安全封送（深拷贝 + 指针重定向）引擎 |
| `monitor.hpp` | 安全监控器，记录越界/别名/双重读取/篡改违例 |
| `adversary.hpp` | 确定性 PRNG 与有界恶意宿主模型 |
| `simulator.hpp` | 对抗模拟器、易受攻击实现与安全实现的同场对比 |
| `demo_abi.hpp` | 覆盖全部结构形态的演示 ABI |

### （1）严格指针区间审计器

`PointerAuditor`（`auditor.hpp`）是唯一决定某段攻击者字节区间能否跨越
边界的组件，封送、输出写回与测试都经由它，策略无法被便捷封装绕过：

- 每个入参指针、嵌套指针及其字节跨度，先用 `checked_span_end` /
  `checked_mul` 做防溢出运算，拒绝任何地址回绕。
- 区间必须**完全**落在某个合法外部共享池内；与私有区间
  `[encBase, encEnd)` 有任意字节交集即判 `OverlapsEnclave`。
- 输出缓冲区遵循同等纪律，宿主无法让 Enclave 写出指向私有内存的别名。
- 变长结构内部偏移经 `audit_internal_offset` 校验，越界判
  `OutOfBoundsOffset`。
- 两个指针目标若别名同一外部字节区间，判 `OverlappingRegions`。
- ABI 模式静态校验字段偏移不越界、引用类型存在；运行时通过已访问地址
  集合与有界遍历深度检测指针图回环（`CycleDetected`）。链表式自引用
  类型在 ABI 层面合法，环是活动指针图的运行时属性而非静态类型属性。

### （2）单次快照安全封送引擎

`Marshaller`（`marshaller.hpp`）对嵌套输入执行 BFS 封送，对每个外部块：

1. **先审计后触碰**：声明（claim）区间，证明其可表示、完全外部、与私有
   区零交集、不与其它目标别名，然后才允许跨边界拷贝。
2. **每字节至多一次**：`fetch_once` 记录已抓取区间，任何重叠的二次抓取
   直接返回 `ByteReadTwice`；配合 `ReadOnceBus` 将“只读一次”从编码约定
   升级为运行时强制不变量。
3. **私有快照上解释**：长度、计数、嵌套指针的所有语义解读都发生在已拷贝
   的私有缓冲上，宿主此后再篡改共享页不影响 Enclave 观察到的任何字节。
4. **指针重定向（swizzle）**：封送完成后，外部指针被改写为私有副本地址，
   业务代码解引用的全部是私有内存，无需再接触不可信总线。
5. **输出单写**：`write_output` 先证明目标在外部区，再执行一次写回。

数组元素的固定头由父数组的整段声明一次性覆盖（`header_preclaimed`），
既不重复声明也不会重复读取。

### （3）有界对抗模拟器

`simulator.hpp` + `adversary.hpp` + `monitor.hpp` 在**单线程、确定性**
调度下复现真实竞态攻击（避免真实数据竞争的不可复现性），每次交错由
种子与读取事件索引唯一标识，可逐字节重放：

- `LengthDoubleFetch`：在易受攻击处理器“校验读取”与“使用读取”的间隙，
  将记录计数膨胀为远超真实数组的值。
- `PointerSwap`：在同一间隙把已校验的数组指针替换为 Enclave 私有地址。
- `RandomTamper`：基于 xorshift64* 的有界随机篡改，限定事件数与篡改数。

`run_vulnerable_handler` 刻意保留“校验一次、再取一次、不深拷贝、不复核”
的经典双重读取模式；`run_safe_handler` 则全程走封送引擎与
`ReadOnceBus`。两者由同一个 `SecurityMonitor` 对照裁决。

## 测试与验证结论

三个自包含测试套件（自带微型断言框架，无第三方依赖）：

- `tests/test_auditor.cpp`：溢出回绕、私有区交集、越界、输出限制、内部
  偏移、非法地址空间布局。
- `tests/test_marshal.cpp`：深拷贝与指针重定向、私有区指针拒绝、别名/环
  检测、读一次强制、语义校验、输出目标限制。
- `tests/test_adversary.cpp`：两类定向攻击在易受攻击路径上**稳定复现**
  违例；安全路径在定向攻击与 128 组随机交错下**始终**零越权、零二次
  读取，且 Enclave 私有秘密字节保持不变。

## 构建与运行

使用 Make（推荐，零配置）：

```sh
make test     # 编译并运行全部测试（严格警告即错误）
make all      # 额外构建演示程序 build/ccm_demo
./build/ccm_demo
```

或使用 CMake：

```sh
cmake -S . -B build/cmake
cmake --build build/cmake
ctest --test-dir build/cmake --output-on-failure
./build/cmake/ccm_demo
```

## 嵌入方式

库为纯头文件，集成时仅需：

```cpp
#include "ccm/marshaller.hpp"
#include "ccm/address_space.hpp"
```

1. 用 `AddressSpace::create(...)` 声明私有区间与外部共享池。
2. 用 `Schema::create(...)` 声明 ABI 类型图。
3. 构造 `Marshaller`，调用 `marshal(rootType, rootAddress)` 得到私有、
   已重定向指针的对象图；可选注入 `SemanticValidator` 做业务校验。
4. 用 `write_output(...)` 将结果安全写回外部缓冲区。

## 设计边界

- 标量按 64 位小端、无对齐访问解码；跨端宿主需在 ABI 层约定小端。
- 指针数组元素按连续固定大小块建模；变长尾部建模为紧跟块头，带非零
  尾部的类型不应作为指针数组元素。
- 对抗模拟采用确定性交错模型而非真实多线程数据竞争，以便攻击可稳定
  复现；其覆盖的是宿主在任意读取间隙篡改共享页这一本质行为。
