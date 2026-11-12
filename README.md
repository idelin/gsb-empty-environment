# 图形提交缓冲校验

用户态图形驱动在提交缓冲前拦截恶意包的可嵌入 C++17 校验组件（`netsec` 命名空间）。

## 线协议

所有数值小端编码，缓冲按 4 字节 DWORD 组织：

- 包头 1 个 DWORD：低 16 位为负载长度（单位 DWORD），高 16 位为操作码。
- 操作码 `0x01` 写寄存器：负载为若干 `(寄存器号, 值)` DWORD 对，长度必须为偶数。
- 操作码 `0x02` 间接跳转：负载固定 2 个 DWORD，组成 64 位绝对跳转地址（低 32 位在前）。

## 校验策略（两阶段）

1. 线性结构扫描：空缓冲/基址未对齐直接拒绝；逐包检查零长度、负载越界、截断包头、未知操作码、负载长度、寄存器白名单，并统计包数预算。
2. 控制流模拟：对每个跳转包沿链模拟，依次检查
   - 目标落在进程已登记区间（`addAddressRange`）且满足对齐要求；
   - 目标若在本缓冲内，必须精确落在包起始位置，禁止跳进包体；
   - 跳转深度受 `max_jump_depth` 预算约束；
   - 已访问的本地包再次落地即判定为环形链接（含自环）并拒绝；
   - 目标落在其他已登记区间时，本地分析安全结束。

所有索引均做带溢出防护的边界检查，校验器配置完成后 `validate()` 为 `const`，可并发调用。

## 集成

```cpp
#include "netsec.hpp"
netsec::SubmissionValidator validator;
validator.addAllowedRegister(0x10);
validator.addAddressRange(buffer_gpu_base, buffer_size);
netsec::CommandBufferView cb{ptr, gpu_base, byte_size};
netsec::ValidationResult result = validator.validate(cb);
if (!result.ok()) { /* result.error / offset / address 定位拒绝原因 */ }
```

## 构建与测试

```sh
cmake -S . -B build -G "Unix Makefiles"
cmake --build build
ctest --test-dir build --output-on-failure
```

- `tests/netsec_test.cpp`：20 项断言，覆盖零长度、长度越界、截断、特权寄存器、环链/自环、包数与跳转深度预算、越界/未对齐/包内跳转及合法缓冲。
- `tests/netsec_fuzz.cpp`：2 万组随机缓冲，配合 ASan/UBSan 验证任意输入不越界读取。
