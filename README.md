# binrw

Schema 驱动的二进制读取/重编码库（C++17，header-only）。

## 格式

```
[ 12 字节固定头部（小端） ][ 载荷 ]
```

头部布局（全部小端，逐字节移位组装，无未对齐访问）：

| 偏移 | 类型 | 含义         |
|------|------|--------------|
| 0    | u32  | magic "BWR1" |
| 4    | u16  | version      |
| 6    | u16  | schema_id    |
| 8    | u32  | 载荷字节数   |

载荷字段由 `Schema` 描述：每个字段有绝对比特偏移、位宽（1..64）、
字节序（`ByteOrder::Little` / `Big`）与符号性。字段可从任意比特开始、
跨任意字节边界。

- **Little**：结果的第 i 位 = 流比特 (offset+i)，字节内 LSB 先编号。
- **Big**：字节内 MSB 先消费，第一个读到的比特是结果的最高位。

## 保证

- **无未对齐访问**：所有多字节值都由单字节移位组装，与宿主大小端无关，
  任何架构上产生相同逻辑值（ASan/UBSan 验证通过）。
- **溢出检查**：`payload_len * 8`、`bit_offset + bit_width` 均先做 u64
  溢出检查；头部声明长度超出实际缓冲区、字段超出载荷比特数都会抛
  `binrw::Error`。
- **符号扩展**：`i64()` 按字段声明位宽做二进制补码符号扩展；
  `set()`/`set_signed()` 将值截断到字段位宽。
- **未知字段保留**：`parse()` 保留原始载荷字节，`encode()` 只重写
  schema 拥有的比特区间，间隙与尾部未知字节逐位原样保留。
- **物理重叠检测**：LE/BE 字段的比特编号方向不同，流区间不相交的
  两个字段仍可能物理别名；`Schema::add` 按物理比特（字节, 位）检测重叠。

## 构建与测试

```sh
cmake -B build && cmake --build build && ctest --test-dir build
```

`tests/test_binrw.cpp` 含手工逐位计算的夹具：跨字节 LE/BE 字段、
负数字段符号扩展、修改单字段后未知比特不变、64 位全宽字段、
溢出/截断/重叠等错误路径。

## 最小示例

```cpp
binrw::Schema s;
s.add({"seq", 0, 12, binrw::ByteOrder::Little, false});
s.add({"temp", 16, 9, binrw::ByteOrder::Big, true});   // 跨字节、有符号

auto msg = binrw::Message::parse(bytes, len, s);
int64_t t = msg.i64("temp");
msg.set("seq", 42);
auto out = msg.encode();   // 未知比特原样保留
```
