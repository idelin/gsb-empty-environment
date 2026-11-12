# NDEF记录边界与URI白名单

攻击者在NFC标签里让NDEF记录头的SR位、长度字段与消息边界不一致，或用URI记录诱导拨号、打开本地文件。给C++移动端写供标签读取回调调用的NDEF解析包：MB/ME位置、分片和ID长度须自洽，SR=1时载荷长度占1字节，总长度不得超出缓冲；URI记录按识别码前缀展开后只放行调用方的协议白名单；Smart Poster嵌套深度有上限。用ME缺失、长度越界和file://验证。

## 设计

纯 C++17、无异常、无第三方依赖，可直接放进标签读取回调在移动端调用。
所有读取都经过带边界检查的游标，绝不解引用缓冲之外的字节。

- `ndef.hpp` / `ndef.cpp`：解析包，命名空间 `netsec::ndef`
- `ndef_test.cpp`：22 组攻击/合规用例（26 项断言）

## 解析时强制的自洽性

- MB 必须出现在首记录且不得出现在后续记录；消息必须以 ME 收尾，缺失 ME、
  ME 后仍有多余字节分别报 `kMessageMissingMe`、`kTrailingDataAfterMe`。
- SR=1 时载荷长度只读 1 字节，否则读 4 字节；类型/ID/载荷长度用 64 位
  求和后统一做边界校验，4GB 级伪造长度以 `kLengthOverflow` 拒绝，越过
  剩余缓冲的长度以 `kTruncatedPayload/Type/Id` 拒绝。
- 分片（CF/TNF unchanged）要求 continuation 不带类型和 ID，分片记录一律
  使用 4 字节长度（置 SR 报 `kChunkedRecordHasSr`），孤儿 continuation、
  分片状态错乱都会被拒；分片载荷在末块拼完后才做策略检查。
- IL=1 才存在 ID 长度字段，ID 字节不足报 `kTruncatedId`；TNF 0 空记录
  不得携带任何内容，保留 TNF 7 报 `kReservedTnf`。

## URI 与 Smart Poster 策略

- URI RTD 先按识别码（0–36）展开前缀，再提取 RFC 3986 scheme，
  大小写不敏感地匹配调用方白名单 `ParseOptions::allowedSchemes`。
- `file` 在硬拒绝名单中，即使调用方误加白名单也会被拒；`tel:` 等拨号
  诱导仅当显式加入白名单才放行。空白/控制字符、无 scheme 的 URI 一律拒绝。
- 白名单默认为空，即默认拒绝一切 URI（fail-closed）。
- Smart Poster 载荷按嵌套 NDEF 消息递归解析，深度由
  `maxSmartPosterDepth`（默认 8）限制；内部 URI 同样受白名单约束，
  隐藏在 Smart Poster 内的 `file://` 仍会被拒。

## 用法

```cpp
#include "ndef.hpp"
using namespace netsec::ndef;

ParseOptions opts;
opts.allowedSchemes = {"https"};   // 只放行 https
// opts.maxSmartPosterDepth = 4;    // 可选：收紧嵌套深度

ParseResult r = parseMessage(tagBytes, tagLength, opts);
if (!r) {
    // r.error 为具体的 ParseError，丢弃整条消息
} else {
    for (const Record& rec : r.records) {
        std::string uri;
        if (recordUri(rec, uri)) { /* uri 已通过白名单，可安全分发 */ }
    }
}
```

## 构建与测试

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

解析失败是 fail-closed：返回错误时不输出任何记录，调用方无需再做二次校验。
