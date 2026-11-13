# Wasm 元素段函数索引

`netsec` 是一个可导入的 C++ WebAssembly 二进制解码与实例化库，重点处理
元素段（element segment）的函数索引裁决。

规则：函数索引空间包含导入函数和模块内定义函数。元素段中的每个非空函数
索引必须严格小于函数总数；索引等于函数总数即为越界，模块验证和实例化都会
失败。索引合法的活动元素段会按其偏移写入表，成功初始化表项。

## 构建与测试

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

构建产物包括可动态导入的 `libnetsec`（`.so`/`.dylib`，取决于平台）、命令行
工具 `netsec` 以及测试程序。

## 运行时导入

C ABI 的主要入口：

- `netsec_decode_module(data, size, err, err_size)`：解析并验证模块。
- `netsec_parse_module(...)`：仅做结构解析。
- `netsec_instantiate_module(...)`：解析、验证并实例化；失败返回 `NULL`。
- `netsec_decode_element_segment(data, size, function_count, info, err,
  err_size)`：对单个元素段执行同样的严格索引裁决。
- `netsec_element_index_is_valid(index, function_count)`：判断
  `index < function_count`。

`NETSEC_ERR_ELEMENT_FUNCTION_INDEX_OUT_OF_BOUNDS`（值为 `9`）表示元素段引用
了不存在的函数。实例化失败时错误缓冲区包含函数索引与实际函数总数。

```cpp
#include "netsec.hpp"

netsec::Instance instance;
netsec::Error error;
if (netsec::decode_and_instantiate(bytes, instance, error) !=
    netsec::ErrorCode::ok) {
  // error.code == element_function_index_out_of_bounds for an OOB index.
}
```

## 命令行

```sh
# 解析、验证并实例化；打印表的初始化结果
./build/netsec module.wasm

# 只验证模块
./build/netsec --validate module.wasm

# 裁决一个原始元素段；索引等于函数数量时失败
./build/netsec --element --function-count 1 segment.bin
```

测试覆盖了合法索引初始化表、`index == function_count` 边界失败、导入函数计入
函数索引空间、表偏移越界，以及通过 `dlopen`/C ABI 的运行时导入。
