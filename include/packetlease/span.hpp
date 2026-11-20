#pragma once
#include <array>
#include <cstdint>
#include <optional>
namespace packetlease {
struct Span { std::uint16_t pool,slot; std::uint32_t generation,offset,size; };
using SpanBytes=std::array<std::uint8_t,16>;
inline std::optional<Span> decode_span_v1(const SpanBytes& b) {
 auto u16=[&](unsigned i){return static_cast<std::uint16_t>((b[i]<<8)|b[i+1]);};
 auto u32=[&](unsigned i){std::uint32_t v=0;for(unsigned k=0;k<4;++k)v|=std::uint32_t(b[i+k])<<(8*k);return v;};
 Span s{u16(0),u16(2),u32(4),u32(8),u32(12)};
 if (!s.size || std::uint64_t(s.offset)+s.size>(std::uint64_t(1)<<32)) return std::nullopt;
 return s;
}
}
