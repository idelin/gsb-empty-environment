# Wasm数据段越界

主动数据段的偏移加上长度超出内存。初始化时越界则实例化失败，不能只写入落在界内的前缀。完全落在内存内的数据段写入成功。运行时调用可导入的C++解码器做这次裁决。把偏移设到最后一页的末尾。

## 实现

`netsec` 提供一个可链接/可导入的 C++17 Wasm 解码与实例化库：

- `netsec::decode()` 解析 Wasm 模块的导入、内存、全局量和数据段。
- `netsec::instantiate()` 分配内存、计算常量偏移，并应用主动数据段。
- `netsec::apply_active_data_segments()` 先检查所有主动数据段，全部通过后才写入；
  任一数据段越界时不会写入任何前缀。
- `netsec_c.h` 提供稳定的 C ABI，便于从其他运行时或 FFI 调用。

边界规则为 `offset <= memory_size && length <= memory_size - offset`，因此：

- 1 页内存、偏移 `65536`、长度 `1` 的主动数据段实例化失败；
- 同样偏移下长度为 `0` 的主动数据段合法；
- 完全落在内存内的数据段正常写入。

## 构建与测试

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

需要动态库供运行时导入时，可加 `-DBUILD_SHARED_LIBS=ON`。

测试覆盖最后一页末尾的越界、零长度边界、部分越界不写前缀、内存内成功写入，
以及 C ABI 运行时调用。
