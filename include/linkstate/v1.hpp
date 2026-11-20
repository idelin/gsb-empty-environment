#pragma once
#include <array>
#include <cstdint>
#include <optional>
namespace linkstate {
struct Checkpoint { std::uint8_t controller; std::uint32_t boot, peer, next; };
using Wire = std::array<std::uint8_t,16>;
inline Wire encode_v1(Checkpoint value) {
    Wire bytes{'L','K',1,value.controller};
    const std::array<std::uint32_t,3> words{value.boot,value.peer,value.next};
    for (unsigned i=0;i<words.size();++i)
        for (unsigned k=0;k<4;++k)
            bytes[4+4*i+k]=static_cast<std::uint8_t>(words[i]>>(8*k));
    return bytes;
}
inline std::optional<Checkpoint> decode_v1(const Wire& bytes) {
    if (bytes[0]!='L' || bytes[1]!='K' || bytes[2]!=1 || !bytes[3]) return std::nullopt;
    std::array<std::uint32_t,3> words{};
    for (unsigned i=0;i<words.size();++i)
        for (unsigned k=0;k<4;++k)
            words[i]|=static_cast<std::uint32_t>(bytes[4+4*i+k])<<(8*k);
    if (!words[0]) return std::nullopt;
    return Checkpoint{bytes[3],words[0],words[1],words[2]};
}
}
