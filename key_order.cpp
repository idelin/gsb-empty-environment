// 索引键：支持 Null / Int64 / Double / String，全序比较 + 保序字节编码。
#include <cstdint>
#include <cstring>
#include <cmath>
#include <string>
#include <vector>
#include <variant>
#include <random>
#include <algorithm>
#include <iostream>
#include <cassert>

// 公开固定的类型顺序：Null < Int < Float < String
enum class KeyType : uint8_t { Null = 0, Int = 1, Float = 2, String = 3 };

struct Key {
    KeyType type = KeyType::Null;
    int64_t i = 0;
    double d = 0.0;
    std::string s;

    static Key null() { return Key{}; }
    static Key ofInt(int64_t v) { Key k; k.type = KeyType::Int; k.i = v; return k; }
    static Key ofFloat(double v) { Key k; k.type = KeyType::Float; k.d = v; return k; }
    static Key ofString(std::string v) { Key k; k.type = KeyType::String; k.s = std::move(v); return k; }
};

// 浮点规范化：NaN 全等（统一为规范 NaN），-0.0 与 +0.0 等价（统一为 +0.0）。
static double normalizeDouble(double v) {
    if (std::isnan(v)) return std::numeric_limits<double>::quiet_NaN(); // 规范 NaN
    if (v == 0.0) return 0.0; // -0.0 -> +0.0
    return v;
}

// 全序比较：先按类型标签，再按类型内规则。整数与浮点不跨类型等价。
static int compare(const Key& a, const Key& b) {
    if (a.type != b.type) return a.type < b.type ? -1 : 1;
    switch (a.type) {
        case KeyType::Null: return 0;
        case KeyType::Int:
            return a.i < b.i ? -1 : (a.i > b.i ? 1 : 0);
        case KeyType::Float: {
            double x = normalizeDouble(a.d), y = normalizeDouble(b.d);
            // 规范化后：NaN==NaN；IEEE 序中 NaN 已在 +Inf 之后（< 对 NaN 恒假，需特判）
            bool xn = std::isnan(x), yn = std::isnan(y);
            if (xn || yn) return xn == yn ? 0 : (xn ? 1 : -1);
            return x < y ? -1 : (x > y ? 1 : 0);
        }
        case KeyType::String:
            return a.s.compare(b.s);
    }
    return 0;
}
static bool keyLess(const Key& a, const Key& b) { return compare(a, b) < 0; }
static bool keyEqual(const Key& a, const Key& b) { return compare(a, b) == 0; }

// 保序编码：字节序（memcmp 序）与比较器序一致。
static void encode(const Key& k, std::string& out) {
    out.push_back(static_cast<char>(k.type)); // 类型标签，顺序即类型顺序
    switch (k.type) {
        case KeyType::Null: break;
        case KeyType::Int: {
            uint64_t u = static_cast<uint64_t>(k.i) ^ (1ULL << 63); // 翻符号位
            for (int sh = 56; sh >= 0; sh -= 8) out.push_back(char(u >> sh));
            break;
        }
        case KeyType::Float: {
            double v = normalizeDouble(k.d);
            uint64_t bits;
            std::memcpy(&bits, &v, 8);
            // 正数(含规范NaN,+0)：置符号位；负数：全部取反 → 字典序 == 数值序
            uint64_t m = (bits & (1ULL << 63)) ? ~bits : bits | (1ULL << 63);
            for (int sh = 56; sh >= 0; sh -= 8) out.push_back(char(m >> sh));
            break;
        }
        case KeyType::String:
            for (unsigned char c : k.s) {
                if (c == 0x00) out.push_back('\x00'), out.push_back('\xFF'); // 转义
                else out.push_back(char(c));
            }
            out.push_back('\x00'); out.push_back('\x00'); // 终止符，小于任何续字节
            break;
    }
}
static std::string encode(const Key& k) { std::string o; encode(k, o); return o; }

// ---------------- 随机验证 ----------------
static Key randomKey(std::mt19937_64& rng) {
    std::uniform_int_distribution<int> t(0, 3);
    switch (t(rng)) {
        case 0: return Key::null();
        case 1: {
            // 偏向边界值
            std::uniform_int_distribution<int> c(0, 3);
            switch (c(rng)) {
                case 0: return Key::ofInt(0);
                case 1: return Key::ofInt(INT64_MAX);
                case 2: return Key::ofInt(INT64_MIN);
                default: return Key::ofInt(int64_t(rng()));
            }
        }
        case 2: {
            std::uniform_int_distribution<int> c(0, 6);
            switch (c(rng)) {
                case 0: return Key::ofFloat(std::numeric_limits<double>::quiet_NaN());
                case 1: return Key::ofFloat(std::numeric_limits<double>::infinity());
                case 2: return Key::ofFloat(-std::numeric_limits<double>::infinity());
                case 3: return Key::ofFloat(0.0);
                case 4: return Key::ofFloat(-0.0);
                case 5: return Key::ofFloat(1.5);
                default: {
                    uint64_t b = rng(); double v; std::memcpy(&v, &b, 8);
                    return Key::ofFloat(v); // 含随机 NaN 载荷
                }
            }
        }
        default: {
            std::string s;
            std::uniform_int_distribution<int> len(0, 8), ch(0, 3);
            int n = len(rng);
            for (int j = 0; j < n; ++j) {
                int c = ch(rng);
                s.push_back(c == 0 ? '\x00' : char(1 + rng() % 255)); // 掺入 NUL 字节
            }
            return Key::ofString(std::move(s));
        }
    }
}

int main() {
    // 定点语义断言
    assert(keyEqual(Key::ofFloat(std::numeric_limits<double>::quiet_NaN()),
                    Key::ofFloat(std::numeric_limits<double>::quiet_NaN())));   // NaN 全等
    assert(keyLess(Key::ofFloat(std::numeric_limits<double>::infinity()),
                   Key::ofFloat(std::numeric_limits<double>::quiet_NaN())));    // NaN 在 +Inf 后
    assert(keyEqual(Key::ofFloat(0.0), Key::ofFloat(-0.0)));                    // 正负零等价
    assert(!keyEqual(Key::ofInt(1), Key::ofFloat(1.0)));                        // 不跨类型等价
    assert(keyLess(Key::ofInt(INT64_MAX), Key::ofFloat(-std::numeric_limits<double>::infinity())));
    assert(keyLess(Key::null(), Key::ofInt(INT64_MIN)));
    assert(keyLess(Key::ofFloat(std::numeric_limits<double>::quiet_NaN()), Key::ofString("")));
    assert(encode(Key::ofFloat(0.0)) == encode(Key::ofFloat(-0.0)));
    assert(encode(Key::ofFloat(std::nan("1"))) == encode(Key::ofFloat(std::nan("2"))));

    std::mt19937_64 rng(20261007);
    const int N = 2000;
    std::vector<Key> keys(N);
    std::vector<std::string> enc(N);
    for (int i = 0; i < N; ++i) { keys[i] = randomKey(rng); enc[i] = encode(keys[i]); }

    // 1) 编码序与比较器序全量一致（O(N^2)）
    for (int i = 0; i < N; ++i)
        for (int j = 0; j < N; ++j) {
            int c = compare(keys[i], keys[j]);
            int e = enc[i].compare(enc[j]);
            if ((c < 0) != (e < 0) || (c == 0) != (e == 0)) {
                std::cerr << "编码序不一致: i=" << i << " j=" << j << "\n";
                return 1;
            }
        }

    // 2) 随机三元组传递性：a<=b && b<=c => a<=c；a<b && b<c => a<c
    std::uniform_int_distribution<int> pick(0, N - 1);
    for (long t = 0; t < 2000000; ++t) {
        const Key &a = keys[pick(rng)], &b = keys[pick(rng)], &c = keys[pick(rng)];
        int ab = compare(a, b), bc = compare(b, c), ac = compare(a, c);
        if (ab <= 0 && bc <= 0 && ac > 0) { std::cerr << "违反传递性(<=)\n"; return 1; }
        if (ab < 0 && bc < 0 && ac >= 0)  { std::cerr << "违反传递性(<)\n"; return 1; }
        // 反对称与自反
        if (compare(a, a) != 0) { std::cerr << "违反自反\n"; return 1; }
        if ((ab < 0) != (compare(b, a) > 0)) { std::cerr << "违反反对称\n"; return 1; }
    }

    // 3) 排序交叉验证：按键排序 == 按编码排序
    std::vector<int> byKey(N), byEnc(N);
    for (int i = 0; i < N; ++i) byKey[i] = byEnc[i] = i;
    std::stable_sort(byKey.begin(), byKey.end(), [&](int x, int y){ return keyLess(keys[x], keys[y]); });
    std::stable_sort(byEnc.begin(), byEnc.end(), [&](int x, int y){ return enc[x] < enc[y]; });
    if (byKey != byEnc) { std::cerr << "排序结果不一致\n"; return 1; }

    std::cout << "全部通过: 定点断言 + " << N << " 键全量编码序一致 + 200万三元组传递性\n";
    return 0;
}
