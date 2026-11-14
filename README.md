# 扩展头链让过滤器跳过了传输层

IPv6过滤设备只解析固定头与部分扩展头，攻击者插入逐跳或目的地选项让传输层偏移计算错误，规则未命中。请实现供C++20网络过滤设备调用的可导入扩展头遍历组件。完整遍历逐跳、路由、分片、目的地、认证与封装安全载荷等扩展头，按长度字段推进并校验对齐；未知扩展头按策略拒绝而不是跳过，最大链长与嵌套受限。上层协议与端口只从正确偏移读取，分片与ESP按各自规则单独处理。用未知扩展、超长链、错位长度、嵌套封装、合法链和规则命中验证解析位置一致。

## 接口

- `netsec::parse_ipv6_extension_chain(std::span<const uint8_t>, ParsePolicy)`：一次遍历并返回 `ParseResult`。
- `netsec::Ipv6ExtensionWalker`：可复用的解析器，构造时固定策略。
- `netsec::rule_matches(result, TransportRule)`：仅在解析成功且端口确实可用时匹配端口规则。
- `ParseResult` 同时给出扩展头列表、实际传输层偏移、端口、分片信息、ESP 偏移和终止原因。

```cpp
#include "netsec.hpp"

std::span<const std::uint8_t> packet = /* 从网卡取得 IPv6 报文 */;
netsec::ParsePolicy policy;
policy.max_extension_headers = 16;
policy.max_chain_bytes = 2048;
policy.max_nesting_depth = 2;

const auto parsed = netsec::parse_ipv6_extension_chain(packet, policy);
if (parsed.ok() && parsed.has_ports()) {
    const netsec::TransportRule rule{
        netsec::kNextHeaderUdp, std::nullopt, 8080};
    if (netsec::rule_matches(parsed, rule)) {
        /* 规则命中，端口来自 parsed.transport_offset 指向的 UDP 头。 */
    }
}
```

## 解析规则

- 固定头为 40 字节，版本必须为 6；声明的 payload length 不能越过输入缓冲区。
- 逐跳、路由和目的地选项头使用 `(HdrExtLen + 1) * 8`；分片头固定 8 字节；认证头使用 `(PayloadLen + 2) * 4`。
- 每个扩展头按自身对齐单位校验，越界或长度字段不合法直接返回错误，不猜测下一个头的位置。
- 逐跳和目的地选项头内的 TLV 也校验边界，损坏的选项长度不会改变后续偏移。
- 未知 next header 默认返回 `UnknownExtensionHeader`，绝不按猜测长度跳过；策略可显式改为不透明上层协议，此时仍不读取端口。
- `max_extension_headers`、`max_chain_bytes` 和 `max_nesting_depth` 限制整条链与 IPv6-in-IPv6 封装深度。
- 首片（offset 为 0，包括原子分片）继续遍历；非首片在分片头后终止，`transport_offset` 和端口均不可用。
- ESP 只确认 8 字节清晰头后终止，载荷视为加密内容，不读取内部 next header 或端口。
- TCP、UDP、DCCP、SCTP 和 UDP-Lite 的端口只从最终 `transport_offset` 读取；ICMPv6 不产生端口。

分片信息中的 `fragment_offset` 以字节为单位。非首片和 ESP 的 `payload_kind` 分别为 `NonInitialFragment` 与 `EncryptedEsp`，因此端口规则不会误命中。

## 构建与验证

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Debug
cmake --build build
ctest --test-dir build --output-on-failure
```

测试覆盖合法完整链与规则命中、未知扩展、超长链、错位/越界长度、坏选项、嵌套封装上限、首片/非首片以及 ESP。
