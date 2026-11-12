# netsec：TLS 1.3 内层明文填充与解封

针对被动记录长度分析与全零内层探测，提供可嵌入 C++17 记录层的零依赖填充库
（实现见 `netsec.cpp`，接口见 `netsec.hpp`）。

## 安全目标与协议语义

- 内层明文布局遵循 RFC 8446 §5.2：`content || ContentType || zeros`。
- 发送端在内容后追加 1 字节类型，再补零使总长成为 256 的整数倍；同一
  256 字节桶内的所有记录长度相同，被动观察者无法按长度区分内容大小。
- 总长遵守 RFC 8446 §5.4 上限 2^14+1：实际可产生的最大填充长度是
  16384（最大 256 倍数），对应内容上限 16383；内容长 16384 时
  `content+type=16385` 无法再对齐到 256，按超长拒绝。
- 接收端从整条记录中定位最后一个非零字节作为 `ContentType`，其前为内容、
  其后的零为填充。全零内层没有类型，按 `unexpected_message`（alert 10）拒绝，
  对应 `UnpadError::kAllZeroInner`，可用 `recommended_alert()` 取告警号。
- 解封扫描为单趟、分支无关实现：无论类型在头部还是尾部、填充多少，都执行
  完全相同的逐字节运算，耗时只取决于记录总长，不依赖数据内容。
- 类型字节为 0 在发送端直接拒绝，避免与填充 / 全零探测混淆。

## 接口概览

- `padded_length(content_length)`：返回填充后总长；超长或溢出返回 0。
- `pad(...)` / `pad_in_place(...)`：缓冲式填充，错误用 `PadError` 表达；
  另有返回 `std::optional<std::vector<...>>` 的便捷重载。
- `unpad(record, length, &view, strict=false)`：零拷贝解封，`UnpadView`
  直接指向输入缓冲；`strict=true` 时仅接受 20–24 的合法 ContentType。
- `is_known_content_type()`、`recommended_alert()`、`to_string()` 辅助函数。

### 最小示例

```cpp
#include "netsec.hpp"

using namespace netsec;

std::uint8_t buffer[16384];
std::size_t out_len = 0;
PadError perr = pad(payload, payload_len, /*application_data=*/23,
                    buffer, sizeof(buffer), &out_len);

UnpadView view;
UnpadError uerr = unpad(buffer, out_len, &view);
if (uerr == UnpadError::kAllZeroInner) {
    // 发送 unexpected_message(10) 并终止连接
}
```

## 构建与测试

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

若系统 `/usr/bin/ar` 是不可用的 xcrun 垫片，可显式指定工具链：

```sh
cmake -S . -B build-cli \
  -DCMAKE_AR=/Library/Developer/CommandLineTools/usr/bin/ar \
  -DCMAKE_RANLIB=/Library/Developer/CommandLineTools/usr/bin/ranlib
```

测试覆盖（`test_netsec.cpp`，约 9 万次断言）：

- 各 256 桶边界（0、254/255/256、510/511/512、16382/16383/16384）。
- 同桶长度不可区分、填充字节全零、容量恰好 / 不足、原地填充。
- 多类型多长度往返、内容内部含零的保留语义。
- 全零内层（含 16384 字节）拒绝与告警映射、空 / 空指针输入。
- 超长内容、零类型、空输出等错误路径与严格 ContentType 模式。
- 可选恒定耗时冒烟测试：`NETSEC_TIMING_TEST=1 ./test_netsec`。

嵌入式场景也可直接编译 `netsec.cpp` 并包含 `netsec.hpp`，库本身仅依赖
`<cstring>` 与标准整数类型，不抛出异常、不做动态分配（vector 重载除外）。
