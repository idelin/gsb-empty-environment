// tests.cpp — layout_checker 的单元测试。

#include "layout_checker.hpp"

#include <cstdio>
#include <optional>
#include <string>

using namespace layoutcheck;

static int failures = 0;

static void fail(const char* file, int line, const std::string& what) {
    ++failures;
    std::printf("FAIL %s:%d: %s\n", file, line, what.c_str());
}

#define CHECK(cond)                                                        \
    do {                                                                   \
        if (!(cond)) fail(__FILE__, __LINE__, "CHECK(" #cond ")");         \
    } while (0)

static void expectDiff(const std::optional<Diff>& d, Diff::Kind kind,
                       const char* path, const char* file, int line) {
    if (!d) {
        fail(file, line, std::string("expected diff '") + toString(kind) +
                             "' at '" + path + "', got no diff");
        return;
    }
    if (d->kind != kind || d->path != path) {
        fail(file, line, std::string("expected ") + toString(kind) + " at '" +
                             path + "', got " + toString(d->kind) + " at '" +
                             d->path + "' (" + d->message + ")");
    }
}

#define EXPECT_DIFF(d, kind, path) expectDiff(d, kind, path, __FILE__, __LINE__)

// 常用基础类型
static TypePtr u8() { return primitive(1, 1); }
static TypePtr u16() { return primitive(2, 2); }
static TypePtr u32() { return primitive(4, 4); }
static TypePtr u64() { return primitive(8, 8); }

static void testIdentical() {
    auto s = structOf({field("a", u32()), field("b", u8())});
    CHECK(!compareLayouts(*s, *s).has_value());
    CHECK(!compareLayouts(*u32(), *u32()).has_value());
}

static void testPrimitiveSizeAndAlign() {
    EXPECT_DIFF(compareLayouts(*u32(), *u64()), Diff::Kind::Size, "<root>");
    // 大小相同、对齐不同（如 int -> 过对齐 int）
    EXPECT_DIFF(compareLayouts(*primitive(4, 4), *primitive(4, 8)),
                Diff::Kind::Align, "<root>");
}

static void testOffsetChange() {
    // 旧: {char a; int b}  新: {char a; int x; int b}
    // b 的偏移 4 -> 8，这是首个差异。
    auto oldS = structOf({field("a", u8()), field("b", u32())});
    auto newS = structOf(
        {field("a", u8()), field("x", u32()), field("b", u32())});
    auto d = compareLayouts(*oldS, *newS);
    EXPECT_DIFF(d, Diff::Kind::Offset, "<root>.b");
    CHECK(d && d->oldValue == 4 && d->newValue == 8);
}

static void testTailPaddingReuse() {
    // 内层结构在自身尾部填充内增长：外层布局完全不变。
    // inner:  {char c}            size 1 align 1
    // inner': {char c; char d[2]} size 3 align 1
    // outer:  {inner x; int i}    i 的偏移都是 4，总大小都是 8。
    auto innerOld = structOf({field("c", u8())});
    auto innerNew = structOf({field("c", u8()), field("d", arrayOf(u8(), 2))});
    auto outerOld = structOf({field("x", innerOld), field("i", u32())});
    auto outerNew = structOf({field("x", innerNew), field("i", u32())});

    auto lo = computeLayout(*outerOld);
    auto ln = computeLayout(*outerNew);
    CHECK(lo && ln);
    CHECK(lo->memberOffsets == ln->memberOffsets); // i 都在偏移 4
    CHECK(lo->size == ln->size && lo->size == 8);  // 尾部填充吸收了增长

    // 检查器仍报告内层新增字段（首个差异），但外层偏移/大小不变。
    EXPECT_DIFF(compareLayouts(*outerOld, *outerNew), Diff::Kind::FieldAdded,
                "<root>.x.d");
}

static void testExplicitPaddingReuse() {
    // 显式填充被真实字段复用：布局逐字节一致。
    // 旧: {char c; _pad[3]; int i}  新: {char c; char x[3]; int i}
    auto oldS = structOf(
        {field("c", u8()), padField("_pad1", 3), field("i", u32())});
    auto newS = structOf(
        {field("c", u8()), field("x", arrayOf(u8(), 3)), field("i", u32())});

    auto lo = computeLayout(*oldS);
    auto ln = computeLayout(*newS);
    CHECK(lo && ln);
    CHECK(lo->memberOffsets == ln->memberOffsets); // 0, 1, 4
    CHECK(lo->size == ln->size && lo->size == 8);
    CHECK(lo->align == ln->align && lo->align == 4);

    // 首个差异：显式填充成员在新版本中消失。
    EXPECT_DIFF(compareLayouts(*oldS, *newS), Diff::Kind::FieldRemoved,
                "<root>._pad1");
}

static void testNestedStruct() {
    // 嵌套结构内部字段大小变化，路径逐层展开。
    auto innerOld = structOf({field("n", u16())});
    auto innerNew = structOf({field("n", u64())});
    auto outerOld = structOf({field("x", innerOld), field("i", u32())});
    auto outerNew = structOf({field("x", innerNew), field("i", u32())});
    auto d = compareLayouts(*outerOld, *outerNew);
    EXPECT_DIFF(d, Diff::Kind::Size, "<root>.x.n");
    CHECK(d && d->oldValue == 2 && d->newValue == 8);
}

static void testUnion() {
    // 联合体：所有成员偏移为 0，大小取最大成员并对齐。
    auto u = unionOf({field("i", u32()), field("c", arrayOf(u8(), 8))});
    auto l = computeLayout(*u);
    CHECK(l && l->size == 8 && l->align == 4);
    CHECK(l->memberOffsets.size() == 2 && l->memberOffsets[0] == 0 &&
          l->memberOffsets[1] == 0);

    auto uOld = unionOf({field("i", u32()), field("c", arrayOf(u8(), 8))});
    auto uNew = unionOf({field("i", u32()), field("c", arrayOf(u8(), 4))});
    auto d = compareLayouts(*uOld, *uNew);
    EXPECT_DIFF(d, Diff::Kind::Count, "<root>.c");
    CHECK(d && d->oldValue == 8 && d->newValue == 4);

    auto ln = computeLayout(*uNew);
    CHECK(ln && ln->size == 4); // 联合体总大小随之缩小
}

static void testArrayCountAndElement() {
    // 数组长度变化
    auto aOld = structOf({field("arr", arrayOf(u32(), 4))});
    auto aNew = structOf({field("arr", arrayOf(u32(), 8))});
    EXPECT_DIFF(compareLayouts(*aOld, *aNew), Diff::Kind::Count, "<root>.arr");

    // 元素类型变化，路径带 []
    auto bOld = structOf({field("arr", arrayOf(u32(), 4))});
    auto bNew = structOf({field("arr", arrayOf(u64(), 4))});
    EXPECT_DIFF(compareLayouts(*bOld, *bNew), Diff::Kind::Size, "<root>.arr[]");
}

static void testKindMismatch() {
    auto s = structOf({field("a", u32())});
    EXPECT_DIFF(compareLayouts(*u32(), *s), Diff::Kind::KindMismatch, "<root>");
}

static void testOverflow() {
    const uint64_t max = std::numeric_limits<uint64_t>::max();

    // 数组乘法溢出：2^32 * 2^32
    auto bigArray = arrayOf(primitive(uint64_t(1) << 32, 1), uint64_t(1) << 32);
    CHECK(!computeLayout(*bigArray).has_value());

    // 比较时报告 Overflow
    auto sOld = structOf({field("a", bigArray)});
    auto sNew = structOf({field("a", bigArray)});
    EXPECT_DIFF(compareLayouts(*sOld, *sNew), Diff::Kind::Overflow, "<root>");

    // 偏移对齐上取整溢出：成员 a 结束后 offset 接近 UINT64_MAX，
    // 下一个成员对齐到 8 时 value + 7 溢出。
    auto s = structOf({field("a", primitive(max - 3, 1)), field("b", u64())});
    CHECK(!computeLayout(*s).has_value());

    // 偏移加法溢出：offset + size 越界。
    auto s2 = structOf(
        {field("a", primitive(max - 3, 1)), field("b", primitive(8, 1))});
    CHECK(!computeLayout(*s2).has_value());
}

static void testInvalidInput() {
    CHECK(!computeLayout(*primitive(0, 1)).has_value()); // 大小为 0
    CHECK(!computeLayout(*primitive(4, 3)).has_value()); // 对齐非 2 的幂
    CHECK(!computeLayout(*primitive(4, 0)).has_value()); // 对齐为 0
}

static void testFieldRemovedAdded() {
    auto oldS = structOf({field("a", u32()), field("b", u32())});
    auto newS = structOf({field("a", u32())});
    EXPECT_DIFF(compareLayouts(*oldS, *newS), Diff::Kind::FieldRemoved,
                "<root>.b");
    EXPECT_DIFF(compareLayouts(*newS, *oldS), Diff::Kind::FieldAdded,
                "<root>.b");
}

int main() {
    testIdentical();
    testPrimitiveSizeAndAlign();
    testOffsetChange();
    testTailPaddingReuse();
    testExplicitPaddingReuse();
    testNestedStruct();
    testUnion();
    testArrayCountAndElement();
    testKindMismatch();
    testOverflow();
    testInvalidInput();
    testFieldRemovedAdded();

    if (failures == 0) {
        std::printf("all tests passed\n");
        return 0;
    }
    std::printf("%d test(s) failed\n", failures);
    return 1;
}
