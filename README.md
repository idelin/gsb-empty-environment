# Bundle长度失配检测

Android 的 Bundle 在系统服务与应用间往返时，若写入长度与读出长度不一致，攻击者能构造一次读取看似无害、再次序列化后变成特权 Intent 的数据（Parcel 失配）。为C++17库实现供系统服务反序列化前调用的失配检测：Bundle 为 4 字节总长 + 魔数 BNDL + 若干键值对，键为带 4 字节长度的字符串，值类型码 1 为 4 字节整数、2 为带 4 字节字节长度的字符串、3 为带 4 字节字节长度的嵌套 Bundle；按声明长度跳过与逐字段解析所得的结束位置必须相等，声明长度超过剩余字节、嵌套内部解析与声明不符、键重复都拒绝，并为总字节数与条目数设预算。用长度失配、重复键、预算边界和正常嵌套 Bundle 验证。

## 用法

系统服务在反序列化前调用 `netsec::validateBundle`：

```cpp
#include "netsec.hpp"

netsec::BundleValidationOptions options;
options.maxTotalBytes = 1 << 20;  // 总字节预算
options.maxEntries = 1024;        // 条目预算（跨嵌套累计）
options.maxDepth = 32;            // 嵌套层级预算

netsec::BundleValidationResult result =
    netsec::validateBundle(data, size, options);
if (!result.ok()) {
    // 拒绝该 Bundle；result.offset 为失败位置，
    // netsec::toString(result.error) 给出原因
}
```

格式与校验规则详见 `netsec.hpp` 头部注释。

## 构建与测试

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```
