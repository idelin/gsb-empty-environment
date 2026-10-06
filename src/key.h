// key.h — 索引键:支持 Null / Int / Double / String 的全序比较与保序编码。
//
// 公开固定的类型顺序(比较器与编码共同遵守,不得更改):
//   Null (tag 0) < Int (tag 1) < Double (tag 2) < String (tag 3)
//
// 语义约定:
//   * 整数与浮点不跨类型等价:Int(1) 与 Double(1.0) 不相等,先按类型排序。
//   * 浮点 NaN 全等(任意 NaN 位模式互相等价),且排在 +inf 之后。
//   * +0.0 与 -0.0 等价(比较相等,编码相同)。
//   * 编码为 memcomparable:memcmp(encode(a), encode(b)) 的符号
//     与 compare(a, b) 的符号一致。
#pragma once

#include <cstdint>
#include <cstring>
#include <string>
#include <variant>

namespace idxkey {

enum class TypeTag : uint8_t {
    Null   = 0,
    Int    = 1,
    Double = 2,
    String = 3,
};

struct Key {
    TypeTag tag = TypeTag::Null;
    int64_t i = 0;          // tag == Int 时有效
    double  d = 0.0;        // tag == Double 时有效(原始值,比较/编码前规范化)
    std::string s;          // tag == String 时有效

    static Key null() { return Key{}; }
    static Key integer(int64_t v) { Key k; k.tag = TypeTag::Int; k.i = v; return k; }
    static Key floating(double v) { Key k; k.tag = TypeTag::Double; k.d = v; return k; }
    static Key str(std::string v) { Key k; k.tag = TypeTag::String; k.s = std::move(v); return k; }
};

// 浮点规范化:所有 NaN -> 规范 NaN(排在 +inf 之后的最小正 NaN);
// -0.0 -> +0.0。规范化后即可用位级全序比较。
inline double canonicalize(double v) {
    if (v != v) {  // NaN(任意位模式、任意符号)
        // 0x7FF0000000000001:正 quiet NaN,位序上恰在 +inf 之后一位。
        const uint64_t bits = 0x7FF0000000000001ULL;
        double out;
        std::memcpy(&out, &bits, sizeof(out));
        return out;
    }
    if (v == 0.0) return 0.0;  // -0.0 -> +0.0
    return v;
}

// 规范化浮点的位级全序:与数值序一致,NaN 在 +inf 之后。
inline int compare_double_bits(double a, double b) {
    uint64_t ua, ub;
    std::memcpy(&ua, &a, 8);
    std::memcpy(&ub, &b, 8);
    // 保序变换:负数翻全部位,非负数翻符号位。
    auto order_key = [](uint64_t u) -> uint64_t {
        return (u & 0x8000000000000000ULL) ? ~u : (u | 0x8000000000000000ULL);
    };
    uint64_t ka = order_key(ua), kb = order_key(ub);
    if (ka < kb) return -1;
    if (ka > kb) return 1;
    return 0;
}

// 全序比较器:先按类型标签,再按类型内规则。
inline int compare(const Key& a, const Key& b) {
    if (a.tag != b.tag)
        return static_cast<uint8_t>(a.tag) < static_cast<uint8_t>(b.tag) ? -1 : 1;
    switch (a.tag) {
        case TypeTag::Null:
            return 0;
        case TypeTag::Int:
            if (a.i < b.i) return -1;
            if (a.i > b.i) return 1;
            return 0;
        case TypeTag::Double:
            return compare_double_bits(canonicalize(a.d), canonicalize(b.d));
        case TypeTag::String:
            return a.s.compare(b.s) < 0 ? -1 : (a.s == b.s ? 0 : 1);
    }
    return 0;  // 不可达
}

inline bool operator==(const Key& a, const Key& b) { return compare(a, b) == 0; }
inline bool operator!=(const Key& a, const Key& b) { return compare(a, b) != 0; }
inline bool operator< (const Key& a, const Key& b) { return compare(a, b) <  0; }
inline bool operator<=(const Key& a, const Key& b) { return compare(a, b) <= 0; }
inline bool operator> (const Key& a, const Key& b) { return compare(a, b) >  0; }
inline bool operator>=(const Key& a, const Key& b) { return compare(a, b) >= 0; }

// ---------------- 保序编码 ----------------

inline void encode(const Key& k, std::string& out) {
    out.clear();
    out.push_back(static_cast<char>(k.tag));
    switch (k.tag) {
        case TypeTag::Null:
            break;
        case TypeTag::Int: {
            // 符号位翻转 + 大端:memcmp 序 == 有符号数值序。
            uint64_t u = static_cast<uint64_t>(k.i) ^ 0x8000000000000000ULL;
            for (int shift = 56; shift >= 0; shift -= 8)
                out.push_back(static_cast<char>((u >> shift) & 0xFF));
            break;
        }
        case TypeTag::Double: {
            double c = canonicalize(k.d);
            uint64_t u;
            std::memcpy(&u, &c, 8);
            uint64_t ok = (u & 0x8000000000000000ULL)
                              ? ~u
                              : (u | 0x8000000000000000ULL);
            for (int shift = 56; shift >= 0; shift -= 8)
                out.push_back(static_cast<char>((ok >> shift) & 0xFF));
            break;
        }
        case TypeTag::String: {
            // 转义:0x00 -> {0x00, 0x01};结尾 {0x00, 0x00}。
            // 保证前缀严格小于扩展,且整体 memcmp 序 == 字节序。
            for (unsigned char ch : k.s) {
                if (ch == 0x00) {
                    out.push_back('\x00');
                    out.push_back('\x01');
                } else {
                    out.push_back(static_cast<char>(ch));
                }
            }
            out.push_back('\x00');
            out.push_back('\x00');
            break;
        }
    }
}

inline std::string encode(const Key& k) {
    std::string out;
    encode(k, out);
    return out;
}

}  // namespace idxkey
