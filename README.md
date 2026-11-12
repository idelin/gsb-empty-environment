# 错误恢复时秘密清零

密码模块异常退出时必须清除已解码的秘密，但已交给合法调用者的所有权不能被提前清零。写可嵌入C++17宿主的秘密缓冲事务，支持移动、受限借用、提交转移及多阶段失败；内存分配器和清零器可注入观察，复制禁用。用每个构造点抛错和嵌套借用测试所有未提交字节被清零且只释放一次。

## 组件（均在 `secret_transaction.hpp`，命名空间 `netsec`）

- `IAllocator` / `IZeroizer`：可注入的分配与清零接口，附带 `DefaultAllocator`、`DefaultZeroizer`（用 `volatile` 写入避免清零被优化掉）。宿主可用 `std::shared_ptr` 注入带观察/统计的实现。
- `SecretBuffer`：仅移动、禁复制的事务缓冲。未提交时拥有字节，析构、构造异常回退或移动赋值都会“清零一次 + 释放一次”；`commit()` 转移所有权后缓冲变空，不再触碰这些字节。
- `SecretBuffer::Borrow`：仅移动、带边界检查的受限窗口。构造即登记租约、析构即归还；`sub_borrow()` 支持嵌套，异常回退时租约自动恢复。借用存活时 `commit()` 与移动赋值抛 `std::logic_error`。
- `SecretOwner`：仅移动的 RAII 句柄，代表已提交给合法调用者的秘密。缓冲不会提前清零；句柄析构时执行唯一一次“清零 + 释放”。
- `SecretTransaction`：多阶段解码容器。任一 `add_stage()` 在构造点抛错时，已建阶段仍由事务持有，并在事务析构时各自清零、各释放一次；`commit_stage()` 提交的阶段变空，由其 `SecretOwner` 负责清理。

## 关键不变量

- 每个成功的 `allocate()` 恰好对应一次 `deallocate()`，绝无双重释放。
- 未提交字节在释放前必定整体清零；已提交字节只在最终 `SecretOwner` 析构时清零，不会被提前抹掉。
- 所有持有秘密的类型（`SecretBuffer`、`SecretOwner`、`Borrow`、`SecretTransaction`）均删除复制，仅允许移动。
- 越界访问抛 `std::out_of_range`；空缓冲 `commit()`/`borrow()`、借用存活时提交或移动赋值抛 `std::logic_error`。

## 构建与测试

直接编译（已在 `-Wall -Wextra -Wpedantic -Wshadow` 下零警告，ASan/UBSan 与 `-O2` 均通过）：

```sh
clang++ -std=c++17 -Wall -Wextra -Wpedantic -fsanitize=address,undefined \
    test_secret_transaction.cpp netsec.cpp -o test_secret_txn
./test_secret_txn
```

或使用 CMake/CTest。若环境里 `/usr/bin/ar` 因缺少活动 Xcode 而失败，可显式指向命令行工具：

```sh
cmake -S . -B build -DCMAKE_AR="$(xcode-select -p)/usr/bin/ar" \
    -DCMAKE_RANLIB="$(xcode-select -p)/usr/bin/ranlib"
cmake --build build
ctest --test-dir build --output-on-failure
```

测试中的 `TrackingAllocator` 在每次 `deallocate()` 内断言：指针仍存活（无双重释放）、大小匹配、且整块内存已全零；`TrackingZeroizer` 记录每次清零的范围与字节数。多阶段测试在第 0、1、2 个构造点分别注入 `std::bad_alloc`，验证各阶段未提交字节均被清零且只释放一次。
