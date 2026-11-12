# CFI前向边类型匹配

解释器宿主的函数指针可能被攻击者覆盖，调用点需要在前向间接调用前确认目标。为 C++17 运行时写可嵌入的 CFI 目标注册表：函数入口、类型标识和取地址标记来自模块加载，调用点提交期望类型，目标必须是已取地址函数入口，且类型完全相同；相同代码折叠后的别名函数不能共享类型，指向函数体中间的地址拒绝。用类型不同、未取地址函数、入口偏移 4 字节、折叠别名和合法回调验证。

## 设计

注册表由模块加载期填充，调用点在前向间接调用前查询：

- `TargetRecord`：函数入口、`std::type_index` 类型标识、函数体字节数和取地址标记。
- `Registry::register_target / load_module`：登记模块元数据；重复登记同类型入口时合并取地址标记和函数体范围。
- `Registry::validate`：只有精确入口命中、已取地址、类型完全相同才返回 `Ok`。
- 函数体中间地址：精确入口未命中后，用登记的函数体范围识别并返回 `InteriorPointer`。
- ICF 折叠别名：同一入口登记不同类型时将槽位置为 `conflicted`，之后所有调用失败关闭，返回 `FoldedAliasConflict`，不允许别名共享类型。
- 未登记地址返回 `UnknownTarget`；`checked_call_with / checked_call` 在校验失败时抛出 `Violation`，成功才执行调用。

## 判定状态

- `Ok`：合法的已取地址函数入口，类型与调用点完全一致。
- `TypeMismatch`：入口存在，但期望类型与登记类型不同。
- `AddressNotTaken`：函数已登记，但模块元数据标记为未取地址。
- `InteriorPointer`：地址落在某个函数体内部，不是函数入口。
- `FoldedAliasConflict`：折叠到同一入口的别名具有不同类型。
- `UnknownTarget`：地址不属于任何已登记函数。

## 测试

`tests/cfi_registry_test.cpp` 覆盖：

- 合法回调通过并实际调用。
- 类型不同的函数指针被拒绝。
- 未取地址函数即使入口和类型正确也被拒绝。
- 入口偏移 4 字节的函数体内部地址被拒绝。
- 模拟 ICF 折叠别名（同入口、不同类型）在两种类型下均被拒绝。
- 未登记地址被拒绝；受控调用在类型不匹配时抛出异常。

## 构建

```sh
cmake -S . -B build
cmake --build build --parallel
ctest --test-dir build --output-on-failure
```

当前机器的 `/usr/bin/ar` 包装器无法定位开发者工具；CMake 构建可显式指定 Command Line Tools 中的归档器：

```sh
cmake -S . -B build \
  -DCMAKE_AR=/Library/Developer/CommandLineTools/usr/bin/ar \
  -DCMAKE_RANLIB=/Library/Developer/CommandLineTools/usr/bin/ranlib
```

也可以不经过静态库直接编译验证：

```sh
clang++ -std=c++17 -Wall -Wextra -Wpedantic -Werror \
  netsec.cpp tests/cfi_registry_test.cpp -o cfi_registry_test
```
