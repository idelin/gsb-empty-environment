# ffi_safety — FFI 跨边界生命周期与别名安全验证库

面向 C++17 运行时安全宿主的可嵌入、零外部依赖（仅标准库）头文件库，用于在
C++ 宿主与外部 FFI 插件之间动态审计借用切片、回调函数与不透明句柄的所有权
转移（`Owned` vs `Borrowed`）与可变别名互斥（Aliasing XOR Mutability）。

编译要求：`-std=c++17 -Wall -Wextra -Wconversion -Werror`
（另在 `-Wsign-conversion -Wshadow -pedantic` 下保持零警告）。

## 建模对象

- **宿主分配区（Region）**：每个 `acquire_owned` 建立一个宿主拥有的对象区，
  带单调递增的**代次（Generation）**计数器；`release` 使区域失效并再次推进代次。
- **借用句柄表（Handle Table）**：`borrow_shared` / `borrow_mut` 从槽位表分配
  不透明 `Handle{slot, generation}`，归还后槽位可复用，但旧代次句柄永久失效。
- **FFI 调用事件流**：`AcquireOwned` / `BorrowShared` / `BorrowMut` / `Release`
  / `Deref` / `ReturnBorrow` / `InvokeCallback` / `ReturnCallback`
  / `BeginIteration` / `EndIteration`，全部进入可回放的 `trace()`。
- **回调重入栈**：`InvokeCallback` / `ReturnCallback` 维护嵌套深度，支持多跳回调。

## 安全不变量（违反即判定 UB 并阻断）

| 违规 `Violation` | 触发条件 |
| --- | --- |
| `RegionAlreadyLive` | 对存活区域重复 `AcquireOwned` |
| `UnknownRegion` | 对从未获取或已释放的区域发起借用/迭代 |
| `AliasingViolation` | 活跃 `BorrowMut` 期间再借 `Shared`/`Mut`，或有共享借时借 `Mut` |
| `ActiveBorrowsOnRelease` | 仍有未归还借用时 `Release` |
| `UseAfterRelease` | 对已死区域再次 `Release` |
| `DanglingHandle` | 解引用/归还无效、已归还或**旧代次**句柄（宿主释放后访问） |
| `ReentrantMutation` | 回调栈非空时，对正在迭代的区域发起 `BorrowMut`/`Release` |
| `CallbackStackImbalance` | `ReturnCallback` 无匹配的 `InvokeCallback`，或会话结束仍有回调 |
| `IterationImbalance` | 迭代守卫重复 `Begin` 或无匹配 `End` |
| `LeakedBorrows` | `check_closed()` 时仍有活跃借用 |
| `TaintedSession` | 首次 UB 之后的任何后续事件（模拟边界立即中止） |

一旦返回 `Verdict::UB`，会话进入 tainted 状态，后续所有调用立即被拒绝，确保
“释放后访问 / 数据竞争”在边界处被阻断而不是延迟到内存破坏。

## 快速使用

```cpp
#include "ffi_safety/ffi_safety.hpp"
using namespace ffi_safety;

Monitor monitor;
monitor.acquire_owned(/*region=*/1);
Handle slice = monitor.borrow_mut(1);

Monitor plugin = monitor.clone();              // 模拟插件侧视角
plugin.borrow_shared(1);                       // UB: 与可变借用别名冲突
plugin.last_violation().code;                  // Violation::AliasingViolation

monitor.return_borrow(slice);
monitor.release(1);
monitor.deref(slice);                          // UB: 旧代次悬垂句柄
monitor.last_violation().code;                 // Violation::DanglingHandle
```

### 多跳回调重入

```cpp
Monitor monitor;
monitor.acquire_owned(1);
monitor.begin_iteration(1);
monitor.invoke_callback(1);                   // 插件回调第 1 跳
monitor.invoke_callback(2);                   // 第 2 跳
monitor.borrow_mut(1);                        // UB: ReentrantMutation
```

## 有界对抗序列搜索器

`AdversarialSearcher` 在固定动作字母表上做**广度优先**搜索（默认深度上限 6），
并通过 `Monitor::state_key()` 对逻辑状态做有序去重。因此对每个目标违规，返回的
第一个命中序列就是**最短序列**，且结果完全确定、可重复。

```cpp
AdversarialSearcher searcher;

// 双可变/共享-可变别名：最短 3 步
SearchResult alias = searcher.find_first(
    Violation::AliasingViolation,
    AdversarialSearcher::single_region_alphabet(1));

// 跨 FFI 悬垂引用：最短 5 步
// Acquire -> BorrowShared -> Return -> Release -> Deref(旧句柄)
SearchResult dangling = searcher.find_first(
    Violation::DanglingHandle,
    AdversarialSearcher::single_region_alphabet(1));

// 回调重入修改正在迭代的容器：最短 4 步
SearchResult reentrant = searcher.search_reentrancy(1);

// 命中结果可直接在全新 Monitor 上回放复现 UB
Monitor replay;
for (const Event& event : reentrant.events) replay.feed(event);
```

搜索器将“无已发出句柄时的归还/解引用”视为空操作，并要求真正的
`DanglingHandle` 命中必须是对**已释放区域**的解引用，从而排除空句柄、二次归还
等平凡伪阳性，专注于跨代次悬垂与别名冲突。

## 构建与测试

直接使用 Make：

```sh
make test
```

或使用 CMake：

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

测试套件（`tests/ffi_safety_tests.cpp`，20 个用例、无外部测试框架）覆盖：

- 所有权/代次推进、重复获取与重复释放；
- 共享借用共存、可变借用独占、归还后再借；
- 活跃借用下释放阻断、释放后旧句柄悬垂、未知区域借用；
- 单跳与多跳回调重入、无回调时允许修改、回调/迭代栈失衡；
- 会话泄漏审计、区域隔离与状态键、事件流回放一致性；
- 搜索器确定性、最短别名/重入/悬垂序列、深度边界与多违规类一次搜索。

## 目录结构

```
include/ffi_safety/ffi_safety.hpp   # 全部实现，单头文件，零依赖
tests/ffi_safety_tests.cpp          # 确定性边界与重入对抗测试
tests/header_check.cpp              # 单 TU 独立包含编译检查
Makefile / CMakeLists.txt
```
