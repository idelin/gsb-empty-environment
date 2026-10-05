# binrw

C++17 header-only 二进制读取/重编码库。

## 特性

- **固定小端头部**（16 字节）：`magic u32 | version u16 | schema_id u16 | payload_bits u64`，
  逐字节显式移位拼装，不依赖宿主字节序。
- **载荷字段按 schema 驱动**：每个字段指定位偏移、位宽（1..64）、字节序
  （`ByteOrder::Little/Big`）与符号性；字段可跨字节、可从任意位偏移开始。
- **无未对齐访问**：所有读写均为逐字节/逐位操作，没有任何
  `memcpy`/`reinterpret_cast` 到多字节整数的路径，因此宿主为大端或小端时
  得到完全相同的逻辑值。
- **未知字段保留**：解析时保留原始载荷缓冲，重编码只在副本上覆写已知字段，
  未覆盖的位（未知字段、填充、尾部数据）逐位保留；schema 未知（传 `nullptr`）
  时整体透传，往返字节完全一致。
- **长度乘法/加法溢出检查**：`checked_mul` / `checked_add` / `bits_to_bytes`
  守护所有长度计算，溢出抛 `binrw::Error` 而不是回绕。
- **符号扩展**：`sign_extend` 将 1..64 位原始值扩展为 `int64_t`；
  `set_signed` 写入时按位宽截断。

## 位编号约定

绝对位位置 `p` 指字节 `p/8` 的第 `p%8` 位（字节内 0 为最低位）。

- 小端字段：值的第 `i` 位（`i=0` 为 LSB）位于绝对位置 `bit_offset + i`。
- 大端字段：值的 MSB 位于 `bit_offset`，位权随绝对位置递增而递减。

## 使用

```cpp
#include "binrw/binrw.hpp"

binrw::Schema s;
s.schema_id = 7;
s.fields = {
    {"flags", 0, 4,  binrw::ByteOrder::Little, binrw::Signedness::Unsigned},
    {"temp",  4, 12, binrw::ByteOrder::Little, binrw::Signedness::Signed},
};

auto msg = binrw::Message::parse(bytes.data(), bytes.size(), &s);
int64_t t = msg.find("temp")->as_signed();   // 已做符号扩展
msg.set_signed("temp", -1);
std::vector<uint8_t> out = msg.encode();      // 未知位原样保留
```

## 构建与测试

```sh
cmake -S . -B build && cmake --build build
./build/binrw_tests        # 或 ctest --test-dir build
```

测试包含逐位夹具（期望值由独立脚本/手算得出并硬编码）、往返一致性、
未知位保留、符号扩展边界（1/8/12/64 位）、长度溢出与截断错误路径。
