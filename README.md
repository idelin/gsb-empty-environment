# FlatBuffers 缓冲校验

`netsec::ValidateFlatBuffer` 是面向接收线程的只读校验入口。调用方提供预期根表布局和可选的四字节文件标识；校验器不依赖 FlatBuffers 运行时，也不持有全局状态，可并发调用。

```cpp
const netsec::TypeLayout u32{netsec::FieldType::UInt32};
const netsec::TypeLayout string{netsec::FieldType::String};

const netsec::FieldLayout fields[] = {
    {0, u32, true},
    {1, string, true},
};
const netsec::TableLayout root_table{fields, 2, "Root"};
const netsec::RootLayout expected{&root_table, "TEST"};

const auto result = netsec::ValidateFlatBuffer(
    data, size, expected, netsec::ValidationLimits{});
if (!result) {
    // result.error / result.offset / result.table_count / result.max_depth_seen
}
```

FlatBuffers 不含运行时类型标签，因此 `RootLayout::root_table` 就是调用方声明的预期根类型；`file_identifier` 非空时还会逐字节核对偏移 4 处的标识。

**校验内容**

- 根偏移、表对象、vtable、vtable 槽、字段、字符串、向量和结构体均在缓冲范围内，并按布局要求的对齐方式检查。
- vtable 必须位于表对象之前且不与表对象重叠；`soffset == 0` 明确作为自指 vtable 拒绝。
- 字符串长度前缀后必须存在 NUL，且 NUL 位于缓冲内。
- 向量长度与元素大小的乘法先做溢出检查，再检查数据范围；表、字符串、向量、结构体元素会递归校验。
- 联合字段同时检查 uint8 判别子和值偏移：`NONE` 不得带值，非 `NONE` 必须有值，未知判别子拒绝。
- `ValidationLimits` 限制嵌套深度、表数量、vtable 大小、单向量和总元素数、字符串长度；所有偏移运算均使用范围检查后的无符号算术。
- 表引用循环通过活动访问集拒绝，重复引用同一表只校验一次并只计一次。

`netsec::ValidateBuffer` 是同一入口的简短别名。可用 `ValidationErrorName(result.error)` 获取稳定错误文本。

**构建与测试**

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

测试用手工构造的原始 FlatBuffers 字节流覆盖合法缓冲、自指 vtable、向量长度乘法溢出、缺失 NUL、联合类型/值不配对、文件标识错误及深度/表数量上限。
