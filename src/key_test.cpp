// key_test.cpp — 随机三元组验证:
//   1) 比较器全序性质(自反/反对称/传递);
//   2) 编码与比较器序一致(memcmp 符号 == compare 符号);
//   3) 等价键编码相同(NaN 各形态、±0)。
#include "key.h"

#include <cassert>
#include <cinttypes>
#include <cstdio>
#include <cstring>
#include <limits>
#include <random>
#include <string>
#include <vector>

using idxkey::Key;
using idxkey::compare;
using idxkey::encode;

static int g_failures = 0;

#define CHECK(cond, ...)                                   \
    do {                                                   \
        if (!(cond)) {                                     \
            ++g_failures;                                  \
            std::printf("FAIL %s:%d: ", __FILE__, __LINE__); \
            std::printf(__VA_ARGS__);                      \
            std::printf("\n");                             \
        }                                                  \
    } while (0)

static int sign(int v) { return (v > 0) - (v < 0); }

static int memcmp_sign(const std::string& a, const std::string& b) {
    size_t n = a.size() < b.size() ? a.size() : b.size();
    int r = std::memcmp(a.data(), b.data(), n);
    if (r != 0) return sign(r);
    if (a.size() < b.size()) return -1;
    if (a.size() > b.size()) return 1;
    return 0;
}

// 顺序敏感的双 NaN 构造(正/负、不同 payload)。
static double make_nan(uint64_t bits) {
    double d;
    std::memcpy(&d, &bits, 8);
    return d;
}

static Key random_key(std::mt19937_64& rng) {
    std::uniform_int_distribution<int> kind(0, 9);
    switch (kind(rng)) {
        case 0:
            return Key::null();
        case 1:
        case 2:
        case 3: {
            // 混合边界值与随机整数。
            static const int64_t edge[] = {
                std::numeric_limits<int64_t>::min(),
                std::numeric_limits<int64_t>::min() + 1,
                -2, -1, 0, 1, 2,
                std::numeric_limits<int64_t>::max() - 1,
                std::numeric_limits<int64_t>::max(),
            };
            if (rng() % 2)
                return Key::integer(edge[rng() % (sizeof(edge) / sizeof(edge[0]))]);
            return Key::integer(static_cast<int64_t>(rng()));
        }
        case 4:
        case 5:
        case 6: {
            // 浮点:NaN 多形态、±0、±inf、次正规、边界与随机位模式。
            static const double edge[] = {
                -std::numeric_limits<double>::infinity(),
                -std::numeric_limits<double>::max(),
                -1.5, -1.0, -std::numeric_limits<double>::min(),
                -std::numeric_limits<double>::denorm_min(),
                -0.0, 0.0,
                std::numeric_limits<double>::denorm_min(),
                std::numeric_limits<double>::min(),
                1.0, 1.5,
                std::numeric_limits<double>::max(),
                std::numeric_limits<double>::infinity(),
                std::numeric_limits<double>::quiet_NaN(),
                -std::numeric_limits<double>::quiet_NaN(),
                make_nan(0x7FF0000000000001ULL),   // 最小正 NaN payload
                make_nan(0x7FFFFFFFFFFFFFFFULL),   // 最大正 NaN payload
                make_nan(0xFFF8000000000000ULL),   // 负 quiet NaN
                make_nan(0xFFFFFFFFFFFFFFFFULL),   // 负 NaN 最大 payload
            };
            if (rng() % 2)
                return Key::floating(edge[rng() % (sizeof(edge) / sizeof(edge[0]))]);
            uint64_t bits = rng();
            double d;
            std::memcpy(&d, &bits, 8);
            return Key::floating(d);
        }
        default: {
            // 字符串:含内嵌 NUL、空前缀、长串。
            size_t len = rng() % 24;
            std::string s;
            s.reserve(len);
            for (size_t j = 0; j < len; ++j) {
                uint64_t r = rng();
                if (r % 4 == 0)
                    s.push_back('\0');  // 高比例内嵌 NUL
                else
                    s.push_back(static_cast<char>(r & 0xFF));
            }
            return Key::str(std::move(s));
        }
    }
}

static void check_edge_cases() {
    const double inf = std::numeric_limits<double>::infinity();
    const double qnan = std::numeric_limits<double>::quiet_NaN();
    const double neg_qnan = -std::numeric_limits<double>::quiet_NaN();
    const double nan_min_payload = make_nan(0x7FF0000000000001ULL);
    const double nan_max_payload = make_nan(0x7FFFFFFFFFFFFFFFULL);
    const double neg_nan_max = make_nan(0xFFFFFFFFFFFFFFFFULL);

    // NaN 全等:任意位模式互相等价,编码相同。
    const double nans[] = {qnan, neg_qnan, nan_min_payload, nan_max_payload, neg_nan_max};
    for (double x : nans)
        for (double y : nans) {
            CHECK(compare(Key::floating(x), Key::floating(y)) == 0,
                  "NaN 形态应互相等价");
            CHECK(encode(Key::floating(x)) == encode(Key::floating(y)),
                  "NaN 形态编码应相同");
        }

    // NaN 排在 +inf 之后。
    CHECK(compare(Key::floating(inf), Key::floating(qnan)) < 0, "inf < NaN");
    CHECK(compare(Key::floating(qnan), Key::floating(inf)) > 0, "NaN > inf");
    CHECK(memcmp_sign(encode(Key::floating(inf)), encode(Key::floating(qnan))) < 0,
          "encode(inf) < encode(NaN)");

    // ±0 等价,编码相同。
    CHECK(compare(Key::floating(0.0), Key::floating(-0.0)) == 0, "+0 == -0");
    CHECK(encode(Key::floating(0.0)) == encode(Key::floating(-0.0)),
          "encode(+0) == encode(-0)");

    // 整数与浮点不跨类型等价:Int(1) != Double(1.0),且 Int < Double。
    CHECK(compare(Key::integer(1), Key::floating(1.0)) != 0, "Int(1) != Double(1.0)");
    CHECK(compare(Key::integer(1), Key::floating(1.0)) < 0, "Int 类型序在 Double 前");

    // 类型顺序:Null < Int < Double < String。
    CHECK(compare(Key::null(), Key::integer(std::numeric_limits<int64_t>::min())) < 0,
          "Null < Int");
    CHECK(compare(Key::floating(qnan), Key::str("")) < 0, "Double < String");
    CHECK(memcmp_sign(encode(Key::null()), encode(Key::integer(0))) < 0,
          "encode(Null) < encode(Int)");

    // 字符串:前缀、内嵌 NUL。
    CHECK(compare(Key::str("a"), Key::str(std::string("a\0", 2))) < 0, "\"a\" < \"a\\0\"");
    CHECK(compare(Key::str(std::string("\0", 1)), Key::str(std::string("\0\0", 2))) < 0,
          "\"\\0\" < \"\\0\\0\"");
    CHECK(compare(Key::str(std::string("\0", 1)), Key::str(std::string("\x01", 1))) < 0,
          "\"\\0\" < \"\\x01\"");
    CHECK(memcmp_sign(encode(Key::str("a")), encode(Key::str(std::string("a\0", 2)))) < 0,
          "encode(\"a\") < encode(\"a\\0\")");
    CHECK(memcmp_sign(encode(Key::str(std::string("\0", 1))),
                      encode(Key::str(std::string("\x01", 1)))) < 0,
          "encode(\"\\0\") < encode(\"\\x01\")");

    // 整数边界。
    CHECK(compare(Key::integer(std::numeric_limits<int64_t>::min()),
                  Key::integer(std::numeric_limits<int64_t>::max())) < 0,
          "INT64_MIN < INT64_MAX");
    CHECK(memcmp_sign(encode(Key::integer(-1)), encode(Key::integer(0))) < 0,
          "encode(-1) < encode(0)");
    CHECK(memcmp_sign(encode(Key::integer(std::numeric_limits<int64_t>::min())),
                      encode(Key::integer(std::numeric_limits<int64_t>::max()))) < 0,
          "encode(INT64_MIN) < encode(INT64_MAX)");
}

int main() {
    check_edge_cases();

    std::mt19937_64 rng(0x5EED2026ULL);
    const int kTriples = 200000;

    for (int t = 0; t < kTriples; ++t) {
        Key a = random_key(rng);
        Key b = random_key(rng);
        Key c = random_key(rng);

        int cab = compare(a, b);
        int cbc = compare(b, c);
        int cac = compare(a, c);

        // 自反性。
        CHECK(compare(a, a) == 0, "自反性: a == a");
        // 反对称性。
        CHECK(sign(cab) == -sign(compare(b, a)), "反对称性: cmp(a,b) == -cmp(b,a)");
        // 传递性(随机三元组核心验证)。
        if (cab <= 0 && cbc <= 0)
            CHECK(cac <= 0, "传递性: a<=b && b<=c => a<=c");
        if (cab == 0 && cbc == 0)
            CHECK(cac == 0, "等价传递: a==b && b==c => a==c");

        // 编码与比较器序一致(对三元组的三对组合全部验证)。
        std::string ea = encode(a), eb = encode(b), ec = encode(c);
        CHECK(memcmp_sign(ea, eb) == sign(cab), "编码序一致: enc(a) vs enc(b)");
        CHECK(memcmp_sign(eb, ec) == sign(cbc), "编码序一致: enc(b) vs enc(c)");
        CHECK(memcmp_sign(ea, ec) == sign(cac), "编码序一致: enc(a) vs enc(c)");
        // 等价键编码必须相同。
        if (cab == 0)
            CHECK(ea == eb, "等价键编码相同: enc(a) == enc(b)");
        if (cbc == 0)
            CHECK(eb == ec, "等价键编码相同: enc(b) == enc(c)");
    }

    if (g_failures == 0) {
        std::printf("OK: %d 个随机三元组 + 边界用例全部通过\n", kTriples);
        return 0;
    }
    std::printf("FAILED: %d 处断言失败\n", g_failures);
    return 1;
}
