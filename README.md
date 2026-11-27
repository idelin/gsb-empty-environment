# Intel CET 影子栈 / IBT 指令流符号验证库

一个可嵌入、零外部依赖（仅 C++17 标准库）的 Intel CET
（Control-flow Enforcement Technology）符号验证库，面向运行时安全分析宿主。
它在指令流层面建模影子栈（Shadow Stack）与间接分支追踪（IBT），并以
`#CP`（Control Protection Fault）模型异常报告所有控制流完整性违规。

库与测试均以
`-std=c++17 -Wall -Wextra -Wconversion -Wpedantic -Werror` 编译通过。

## 目录结构

- `include/cet/cet.h`：公共 API、故障码、指令工厂与 `CetMachine`。
- `src/cet.cpp`：内存保护、双栈同步、令牌原子协议、IBT 状态机实现。
- `tests/cet_tests.cpp`：零依赖测试套件（41 项断言，13 个场景）。
- `Makefile` / `CMakeLists.txt`：静态库与测试构建。

## 内存与栈模型

- 通过 `map_region(base, size, shadow)` 划分常规页与影子页；常规页可普通
  读写，影子页只读受保护。
- 影子页只允许 CET 影子操作访问：`CALL` / `RET` / `WRSS` / `INCSSP` /
  `SAVEPREVSSP` / `RSTORSSP`。普通 `STORE` / `LOAD` 触碰影子页立即 `#CP`
  （`ShadowStoreDenied` / `ShadowLoadDenied`）。
- 影子栈向下增长，`SSP` 始终 8 字节对齐；每个影子槽记录值、帧类型
  （`ReturnAddress` / `RestoreToken` / `PrevSspToken`）与所属保护域。

## 控制流规则

### 1. 双栈同步与 ROP 检测

- `CALL` 把同一符号返回地址同时压入常规栈与影子栈。
- `RET` 弹出两栈返回地址并严格比对，任何不一致立即
  `ReturnAddressMismatch`，从而检出只篡改常规栈的 ROP 攻击。

### 2. 恢复令牌原子切换协议

恢复令牌布局（与 Intel SDM 一致）：

| 位 | 含义 |
|----|------|
| 0  | `busy`，影子栈是否活跃 |
| 1  | 64 位模式标志（64 位模式下必须为 1） |
| 63:2 | 目标 `SSP` |

`RSTORSSP` 依次校验：

1. 令牌地址与目标 `SSP` 均 8 字节对齐，否则 `MisalignedToken`；
2. 地址位于影子页内，否则 `UnmappedShadowAddress`；
3. 令牌存在于恢复令牌表，否则 `TokenForged`（拦截伪造令牌）；
4. 位 1（64 位模式）为 1，否则 `TokenModeBitClear`；
5. 位 0（`busy`）为 0，否则 `TokenBusy`（拦截令牌重放）；
6. 令牌字与令牌表登记值一致，否则 `TokenForged`；
7. 通过原子 compare-and-set 将 `busy` 置 1（模拟硬件锁语义）；
8. 目标影子栈未在其它协程中活跃，否则 `TokenReplay`
   （拦截双协程共用同一活跃影子栈）。

`SAVEPREVSSP` 在成功的 `RSTORSSP` 之后，把切换前的 `SSP` 编码为
`busy=1, mode=1` 的前序影子栈令牌压入当前影子栈；未先执行 `RSTORSSP`
则报 `NoPreviousShadowStack`。

### 3. IBT 等待状态机

- 所有间接 `JMP` / `CALL` 进入 `WAIT_FOR_ENDBRANCH`。
- 下一条执行指令若不是 `ENDBR64`，立即 `MissingEndBranch`，从而在落点
  缺失 ENDBR 时阻断 JOP/COP。
- `ENDBR64` 满足等待后回到 `IDLE`；在 `IDLE` 下为架构 NOP。

### 4. INCSSP 栈展开越权

`INCSSP n` 仅允许跳过**当前保护域拥有的 `ReturnAddress` 帧**。遇到其它域
的返回帧、`RestoreToken` 或 `PrevSspToken` 边界令牌即报
`IncsspOverreach`，防止借栈展开越过受保护帧。

## 快速开始

```cpp
#include "cet/cet.h"

cet::CetMachine machine;
machine.map_region(0x1000, 0x4000, /*shadow=*/false);
machine.map_region(0x8000, 0x2000, /*shadow=*/true);
machine.set_rsp(0x4f00);
machine.set_ssp(0x9000);

machine.step(cet::make_call_direct());
machine.step(cet::make_ret());                 // 正常返回

// 非抛出式接口，适合批量分析
cet::VerificationReport report = machine.verify(program);
if (!report.ok) { /* report.fault / report.message */ }
```

## 构建与测试

使用 Make（工具链提供 `ar` 时同时产出静态库）：

```sh
make test
```

使用 CMake：

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

若环境缺少归档器，可直接编译源文件嵌入宿主：

```sh
c++ -std=c++17 -Wall -Wextra -Wconversion -Wpedantic -Werror \
    -Iinclude src/cet.cpp tests/cet_tests.cpp -o cet_tests
```

## 测试覆盖

- 正常 `CALL`/`RET` 双栈平衡；
- ROP 篡改常规返回地址触发 `#CP`；
- 普通 `STORE`/`LOAD` 触碰影子页被拒，`WRSS` 合法；
- 间接 `JMP`/`CALL` 缺失 `ENDBR64` 被拒，合法落点放行；
- 伪造令牌、未对齐令牌、模式位清零、`busy` 重放；
- 双协程对同一活跃影子栈的 `TokenReplay`；
- 跨域与边界令牌的 `INCSSP` 越权，同域合法展开；
- 完整协程 `RSTORSSP`/`SAVEPREVSSP` 往返与 `setjmp`/`longjmp` 建模。

## 建模边界

这是指令流级符号模型而非 CPU 仿真：返回地址采用符号值（`RIP+8`），
`WRSS` 是受信运行时写入影子页的唯一数据通道，令牌表代表受信运行时维护的
令牌来源。模型聚焦 CET 控制流完整性属性，不建模分页权限位、缓存或并发
调度，但 `RSTORSSP` 的 `busy` 获取使用真实原子操作表达。
