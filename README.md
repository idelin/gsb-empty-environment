# MT 字段编码组件

供 C++20 跨境报文网关导入的字段校验与编码库。组件在写入 MT 报文前完成校验，字段值中的回车、换行、冒号及协议外字符不会进入序列化结果。

## 使用

```cpp
#include "netsec.hpp"

netsec::FieldCodec codec({
    {"20", netsec::FieldKind::Reference, 16},
    {"32A", netsec::FieldKind::Amount, 18},
    {"70", netsec::FieldKind::SwiftText, 140},
});

std::vector<netsec::Field> fields = {
    {"20", "REF-20261109-01"},
    {"32A", "1250.00"},
    {"70", "Invoice 42 paid in full"},
};

std::string wire = codec.encode(fields);
std::vector<netsec::Field> decoded = codec.decode(wire);
```

CMake 目标为 `netsec`，头文件位于仓库根目录。`encode` 和 `decode` 校验失败时抛出 `netsec::CodecError`；`try_encode` 和 `try_decode` 返回 `std::optional` 并通过 `ValidationResult` 返回结构化错误及字段位置。

## 字段规则

- `SwiftText`：仅允许 SWIFT X 字符集，另拒绝字段分隔符 `:`。不允许回车、换行和其他控制字符。
- `Reference`：固定为 `[A-Za-z0-9][A-Za-z0-9/.-]{0,15}`，最长 16 字符。
- `Amount`：固定为 `[0-9]{1,15}\.[0-9]{2}`，不使用千分位、正负号或逗号小数。
- `Utf8Text`：可选的 UTF-8 扩展文本模式。拒绝控制字符、Unicode 行分隔符、格式控制符、非字符及常见冒号混淆字符。
- 所有字段长度按 Unicode 码点计算，而不是 UTF-8 字节数。
- 字段标签固定为 `[0-9]{2}[A-Z]?`。

## 序列化格式

字段按调用方给定顺序编码为：

```text
:20:REF-20261109-01
:32A:1250.00
:70:Invoice 42 paid in full
```

每个字段为 `:`、标签、`:`、值、换行。值中的冒号会与字段边界冲突，因此在编码前被拒绝。解码器严格检查起始冒号、标签、行终止符和字段值，并保序返回全部字段。

## 验证

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

测试覆盖合法报文、换行/回车/冒号边界注入、字符集绕过、非法 UTF-8、Unicode 行长限制、参考号与金额格式、长度溢出及序列化往返。
