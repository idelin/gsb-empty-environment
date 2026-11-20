#pragma once
#include <cstdint>
#include <optional>
#include <vector>
namespace measurebank {
struct Run { std::uint16_t first, count; std::uint32_t value; };
// Decode the persisted run list without expanding the slot space.
inline std::optional<std::vector<Run>> decode_v1(const std::vector<std::uint8_t>& bytes) {
    if (bytes.size()<3 || bytes[0]!=0x4d || bytes[1]!=1 || bytes.size()!=3u+8u*bytes[2]) return std::nullopt;
    std::uint32_t previous_end=0; std::vector<Run> result;
    for (std::size_t p=3;p<bytes.size();p+=8) {
        auto u16=[&](std::size_t i){return static_cast<std::uint16_t>((bytes[i]<<8)|bytes[i+1]);};
        std::uint32_t value=0; for(unsigned k=0;k<4;++k) value=(value<<8)|bytes[p+4+k];
        Run run{u16(p),u16(p+2),value};
        const auto end=static_cast<std::uint32_t>(run.first)+run.count;
        if (!run.count || run.first<previous_end || end>65536u) return std::nullopt;
        result.push_back(run); previous_end=end;
    }
    return result;
}
}
