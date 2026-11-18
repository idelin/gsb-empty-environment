# FlatBuffers 缓冲校验

`netsec` 提供一个不依赖生成代码的 FlatBuffers 校验器。接收线程在读取任何字段前，
先用可信的 `BufferSchema` 检查不可信缓冲区；校验器无全局可变状态，可由多个接收
线程共享同一个布局对象。

## 接口

```cpp
#include "netsec.hpp"

netsec::FieldLayout fields[] = {
    // slot, type, required
    {0, netsec::FieldType::String, true},
};
netsec::TableLayout root{"Root", fields, 1};

netsec::BufferSchema schema;
schema.root_table = &root;
schema.file_identifier = {{'T', 'S', 'T', '1'}};
schema.has_file_identifier = true;

netsec::VerifyOptions options;
options.max_depth = 32;    // 根表深度为 0
options.max_tables = 256;

netsec::VerificationResult result =
    netsec::VerifyFlatBuffer(data, size, schema, options);
if (!result) {
    // result.error, result.offset, result.message 可用于日志或断开连接。
}
```

表、向量和联合通过布局对象描述：

- 表字段：`FieldLayout{slot, FieldType::Table, required, 0, 0, &child}`。
- 向量字段：设置 `FieldType::Vector` 并指向 `VectorLayout`；元素可再指定标量、
  字符串、表或嵌套向量。
- 联合：`UnionLayout` 给出判别字段 `type_slot`、值字段 `value_slot` 及
  `UnionVariant` 列表；判别值为 0（NONE）时必须没有值字段。
- 内联结构：`FieldType::Struct` 需显式给出 `size` 和 `alignment`。

数值字段的 `size`/`alignment` 可留为 0，校验器会按类型推导。

## 检查项

- 文件标识和根表布局；根偏移、所有 uoffset 均要求向前、四字节对齐且在缓冲内。
- vtable 偏移、尺寸、对象范围及其与对象的重叠；明确拒绝自指 vtable。
- 字段偏移与对象边界、字段对齐。
- 字符串长度和 NUL 终止符。
- 向量长度与元素大小的乘法溢出及实际缓冲边界。
- 联合类型/值成对，未知判别值拒绝。
- 最大嵌套深度、最大表数量和可选的向量/字符串长度上限。

`VerifyFlatBuffer` 只读取 `[data, data + size)`，不会解引用缓冲区外的地址。

## 测试

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

测试夹具包含合法缓冲、自指/重叠 vtable、超长向量、缺失字符串终止符、联合
配对错误、截断缓冲以及深度/表数量限制。
