// 灰度量化库测试：精确累计、最终取整、边界丢弃计量、镜像↔反向扫描、分块不变。

#include <cmath>
#include <cstdint>
#include <iostream>
#include <random>
#include <string>
#include <vector>

#include "graydiff/quantize.hpp"

using namespace graydiff;

static int g_checks = 0;
static int g_failures = 0;

#define CHECK(cond)                                                        \
    do {                                                                   \
        ++g_checks;                                                        \
        if (!(cond)) {                                                     \
            ++g_failures;                                                  \
            std::cerr << "FAIL " << __FILE__ << ":" << __LINE__ << ": "    \
                      << #cond << "\n";                                    \
        }                                                                  \
    } while (0)

static void checkBigEq(const BigInt& a, const BigInt& b, const char* what, int line) {
    ++g_checks;
    if (a != b) {
        ++g_failures;
        std::cerr << "FAIL line " << line << ": " << what << " expected " << b.toString()
                  << " got " << a.toString() << "\n";
    }
}
#define CHECK_BIG_EQ(a, b) checkBigEq((a), (b), #a " == " #b, __LINE__)

static Image randomImage(int w, int h, int maxValue, uint32_t seed) {
    Image img(w, h);
    std::mt19937 rng(seed);
    std::uniform_int_distribution<int32_t> dist(0, maxValue);
    for (auto& p : img.pixels) p = dist(rng);
    return img;
}

static Image flipH(const Image& img) {
    Image out(img.width, img.height);
    for (int y = 0; y < img.height; ++y)
        for (int x = 0; x < img.width; ++x) out.at(x, y) = img.at(img.width - 1 - x, y);
    return out;
}

static std::vector<int> flipH(const std::vector<int>& v, int w, int h) {
    std::vector<int> out(v.size());
    for (int y = 0; y < h; ++y)
        for (int x = 0; x < w; ++x) out[size_t(y) * w + x] = v[size_t(y) * w + (w - 1 - x)];
    return out;
}

// ---------------------------------------------------------------- BigInt

static void testBigInt() {
    BigInt a = BigInt::powSmall(16, 10);  // 2^40
    CHECK(a.toString() == "1099511627776");
    BigInt b = a;
    b.divSmallExact(16);
    CHECK(b == BigInt::powSmall(16, 9));

    BigInt c = BigInt::powSmall(16, 10);
    c.mulSigned(-3);
    CHECK(c.sign() == -1);
    BigInt d = BigInt::powSmall(16, 10);
    d.mulSmall(4);
    CHECK(c + d == BigInt::powSmall(16, 10));  // -3x + 4x == x

    CHECK(BigInt(-5) < BigInt(3));
    CHECK(BigInt(0) == BigInt());
    CHECK(BigInt(-12345).toString() == "-12345");

    BigInt e = BigInt::powSmall(2, 200);
    e.mulSigned(-7);
    BigInt f = e;
    f.divSmallExact(7);
    CHECK(f == -BigInt::powSmall(2, 200));

    double r = BigInt::approxRatio(BigInt::powSmall(2, 200), BigInt::powSmall(2, 199));
    CHECK(std::fabs(r - 2.0) < 1e-12);

    // 加减互逆（大数）
    BigInt g = BigInt::powSmall(48, 30);
    BigInt h = BigInt::powSmall(7, 25);
    CHECK((g + h) - h == g);
    CHECK((g - h) + h == g);
}

// ------------------------------------------------------- 已知答案：单行

// W=4 H=1，核 rightHalf（右邻 1/2），levels=2，maxv=255，v=[100,100,100,100]
// 手算：A=[100,150,47.5,123.75] -> 级 [0,1,0,0]，误差 [100,-105,47.5,123.75]
// 仅最后一个像素的 1/2 份额出界：丢弃 61.875；D = 2^4*1 = 16 -> 990
static void testKnownAnswerSingleRow() {
    Image img(4, 1);
    img.pixels = {100, 100, 100, 100};
    Options opt;
    opt.kernel = Kernel::rightHalf();
    opt.trace = true;
    Result r = quantize(img, opt);

    CHECK(r.levelIndex == (std::vector<int>{0, 1, 0, 0}));
    CHECK_BIG_EQ(r.denominator, BigInt(16));
    CHECK_BIG_EQ(r.discardedNum, BigInt(990));

    // 累计值（单位 1/16）：100, 150, 47.5, 123.75
    CHECK_BIG_EQ(r.trace.accumUnits[0], BigInt(1600));
    CHECK_BIG_EQ(r.trace.accumUnits[1], BigInt(2400));
    CHECK_BIG_EQ(r.trace.accumUnits[2], BigInt(760));
    CHECK_BIG_EQ(r.trace.accumUnits[3], BigInt(1980));
    // 误差（单位 1/16）：100, -105, 47.5, 123.75
    CHECK_BIG_EQ(r.trace.errorUnits[0], BigInt(1600));
    CHECK_BIG_EQ(r.trace.errorUnits[1], BigInt(-1680));
    CHECK_BIG_EQ(r.trace.errorUnits[2], BigInt(760));
    CHECK_BIG_EQ(r.trace.errorUnits[3], BigInt(1980));
}

// ------------------------------------------------------- 已知答案：两行

// W=2 H=2，核 {(1,0,1),(-1,1,1),(0,1,1),(1,1,1)}/4，levels=2，maxv=255
// v = [[100,200],[50,150]]，serpentine：第 0 行 L->R，第 1 行 R->L
// 手算（见 README 附录）：
//   p(0,0): A=100   -> 0, e=100
//   p(1,0): A=225   -> 1, e=-30
//   p(1,1): A=167.5 -> 1, e=-87.5
//   p(0,1): A=45.625-> 0, e=45.625
// 丢弃 = 25 - 7.5 - 7.5 - 65.625 + 45.625 = -10；D = 4^4 = 256 -> -2560
static Kernel cross4() { return {{{1, 0, 1}, {-1, 1, 1}, {0, 1, 1}, {1, 1, 1}}, 4}; }

static void testKnownAnswerTwoRows() {
    Image img(2, 2);
    img.pixels = {100, 200, 50, 150};
    Options opt;
    opt.kernel = cross4();
    opt.trace = true;
    Result r = quantize(img, opt);

    CHECK(r.levelIndex == (std::vector<int>{0, 1, 0, 1}));
    CHECK_BIG_EQ(r.denominator, BigInt(256));
    CHECK_BIG_EQ(r.discardedNum, BigInt(-2560));

    CHECK_BIG_EQ(r.trace.accumUnits[2], BigInt(45.625 * 256));   // p(0,1)
    CHECK_BIG_EQ(r.trace.accumUnits[3], BigInt(167.5 * 256));    // p(1,1)
    CHECK_BIG_EQ(r.trace.errorUnits[3], BigInt(-87.5 * 256));
}

// --------------------------------------------------------- 最终取整规则

static void testRoundingRule() {
    // levels=2, maxv=255, D=16：阈值 127.5，恰好居中取高级（round-half-up）
    CHECK(decideLevel(BigInt(1275), BigInt(160), 2, 255) == 0);   // 127.5 - eps
    CHECK(decideLevel(BigInt(2040), BigInt(16), 2, 255) == 1);    // 恰好 127.5 -> 上
    CHECK(decideLevel(BigInt(2039), BigInt(16), 2, 255) == 0);    // 127.4375
    CHECK(decideLevel(BigInt(2041), BigInt(16), 2, 255) == 1);

    // levels=4, maxv=255, D=510：边界 42.5 / 127.5 / 212.5
    CHECK(decideLevel(BigInt(21675), BigInt(510), 4, 255) == 1);  // 恰好 42.5 -> 上
    CHECK(decideLevel(BigInt(21674), BigInt(510), 4, 255) == 0);
    CHECK(decideLevel(BigInt(65025), BigInt(510), 4, 255) == 2);  // 恰好 127.5 -> 上
    CHECK(decideLevel(BigInt(108375), BigInt(510), 4, 255) == 3); // 恰好 212.5 -> 上

    // 越界钳制
    CHECK(decideLevel(BigInt(-5000), BigInt(16), 2, 255) == 0);
    CHECK(decideLevel(BigInt(300 * 16), BigInt(16), 2, 255) == 1);
    CHECK(decideLevel(BigInt(99999 * 510), BigInt(510), 4, 255) == 3);
}

// ------------------------------------------------------- 扫描方向与往返

static void testSerpentineDirection() {
    Options opt;
    Engine e(2, 3, opt);
    CHECK(e.rowDirection(0) == ScanDirection::LeftToRight);
    CHECK(e.rowDirection(1) == ScanDirection::RightToLeft);
    CHECK(e.rowDirection(2) == ScanDirection::LeftToRight);

    opt.startDirection = ScanDirection::RightToLeft;
    Engine e2(2, 3, opt);
    CHECK(e2.rowDirection(0) == ScanDirection::RightToLeft);
    CHECK(e2.rowDirection(1) == ScanDirection::LeftToRight);
}

// ------------------------------------------- 精确累计：误差守恒恒等式

// 权重和为 1 时：sum(E_p) == sum(收到的贡献) + 边界丢弃
static void testConservation(const Kernel& kernel, int levels, int w, int h, uint32_t seed) {
    Image img = randomImage(w, h, 255, seed);
    Options opt;
    opt.kernel = kernel;
    opt.levels = levels;
    opt.trace = true;
    Result r = quantize(img, opt);

    BigInt sumE, sumR;
    for (size_t p = 0; p < r.trace.errorUnits.size(); ++p) {
        sumE += r.trace.errorUnits[p];
        BigInt vd = r.denominator;
        vd.mulSmall(uint32_t(img.pixels[p]));
        sumR += r.trace.accumUnits[p] - vd;
    }
    CHECK_BIG_EQ(sumE - sumR, r.discardedNum);
}

static void testConservationAll() {
    testConservation(Kernel::floydSteinberg(), 2, 24, 18, 42);
    testConservation(Kernel::floydSteinberg(), 5, 20, 14, 43);
    testConservation(Kernel::jarvisJudiceNinke(), 3, 17, 11, 44);
    testConservation(cross4(), 2, 30, 7, 45);
}

// ------------------------------------------- 镜像输入 <-> 反向扫描

static void testMirrorOne(const Image& img, const Kernel& kernel, int levels) {
    Options fwd;
    fwd.kernel = kernel;
    fwd.levels = levels;
    fwd.startDirection = ScanDirection::LeftToRight;

    Options rev = fwd;
    rev.startDirection = ScanDirection::RightToLeft;

    Result a = quantize(img, fwd);
    Result b = quantize(flipH(img), rev);

    CHECK(b.levelIndex == flipH(a.levelIndex, img.width, img.height));
    CHECK_BIG_EQ(b.discardedNum, a.discardedNum);
}

static void testMirror() {
    struct Size { int w, h; };
    const Size sizes[] = {{5, 4}, {6, 7}, {16, 9}, {1, 1}, {1, 8}, {8, 1}, {23, 2}};
    const Kernel kernels[] = {Kernel::floydSteinberg(), Kernel::jarvisJudiceNinke(), cross4(),
                              Kernel::rightHalf()};
    const int levelSet[] = {2, 3, 5};
    uint32_t seed = 100;
    for (const auto& s : sizes)
        for (const auto& k : kernels)
            for (int lv : levelSet) testMirrorOne(randomImage(s.w, s.h, 255, seed++), k, lv);
}

// ------------------------------------------------------- 分块不改变输出

static void testTilingOne(const Image& img, const Kernel& kernel, int levels) {
    Options opt;
    opt.kernel = kernel;
    opt.levels = levels;
    Result ref = quantize(img, opt);

    const int tiles[][2] = {{1, 1},  {1, img.height}, {img.width, 1}, {3, 4},
                            {5, 7},  {img.width, img.height},         {100, 100},
                            {2, 9},  {7, 2}};
    for (const auto& t : tiles) {
        Result r = quantizeTiled(img, opt, t[0], t[1]);
        CHECK(r.levelIndex == ref.levelIndex);
        CHECK_BIG_EQ(r.discardedNum, ref.discardedNum);
    }
}

static void testTiling() {
    testTilingOne(randomImage(37, 23, 255, 7), Kernel::floydSteinberg(), 3);
    testTilingOne(randomImage(29, 17, 255, 8), Kernel::jarvisJudiceNinke(), 2);
    testTilingOne(randomImage(16, 16, 255, 9), Kernel::stucki(), 5);
    testTilingOne(randomImage(4, 4, 255, 10), cross4(), 2);
}

// ------------------------------------------------------------- 其他

static void testAtkinsonSmoke() {
    Image img = randomImage(20, 12, 255, 11);
    Options opt;
    opt.kernel = Kernel::atkinson();  // sum(num)=6 < den=8，故意不完全扩散
    Result r = quantize(img, opt);
    CHECK(r.levelIndex.size() == 240u);
    for (int lv : r.levelIndex) CHECK(lv == 0 || lv == 1);
}

static void testGray8() {
    Result r;
    r.levels = 2;
    CHECK(r.gray8Of(0) == 0);
    CHECK(r.gray8Of(1) == 255);
    r.levels = 3;
    CHECK(r.gray8Of(0) == 0);
    CHECK(r.gray8Of(1) == 128);  // 127.5 round-half-up
    CHECK(r.gray8Of(2) == 255);
}

static void testValidation() {
    bool threw = false;
    try {
        Options opt;
        opt.levels = 1;
        quantize(randomImage(2, 2, 255, 1), opt);
    } catch (const std::invalid_argument&) { threw = true; }
    CHECK(threw);

    threw = false;
    try {
        Kernel bad = {{{0, 0, 1}}, 2};  // 不指向未处理像素
        Options opt;
        opt.kernel = bad;
        quantize(randomImage(2, 2, 255, 1), opt);
    } catch (const std::invalid_argument&) { threw = true; }
    CHECK(threw);

    threw = false;
    try {
        Image img(2, 2, 300);  // 超出 maxValue=255
        quantize(img, Options{});
    } catch (const std::out_of_range&) { threw = true; }
    CHECK(threw);
}

int main() {
    testBigInt();
    testKnownAnswerSingleRow();
    testKnownAnswerTwoRows();
    testRoundingRule();
    testSerpentineDirection();
    testConservationAll();
    testMirror();
    testTiling();
    testAtkinsonSmoke();
    testGray8();
    testValidation();

    std::cout << (g_checks - g_failures) << "/" << g_checks << " checks passed\n";
    if (g_failures) {
        std::cout << g_failures << " FAILURES\n";
        return 1;
    }
    std::cout << "ALL TESTS PASSED\n";
    return 0;
}
