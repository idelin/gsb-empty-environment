#include "layout.hpp"

#include <cstdint>
#include <iostream>
#include <string>

using namespace slayout;

static int failures = 0;

#define CHECK(cond)                                                            \
    do {                                                                       \
        if (!(cond)) {                                                         \
            std::cerr << "FAIL " << __FILE__ << ":" << __LINE__ << "  "        \
                      << #cond << "\n";                                        \
            ++failures;                                                        \
        }                                                                      \
    } while (0)

static Type scalar(const std::string& name, uint64_t size, uint64_t align) {
    Type t;
    t.kind = Kind::Scalar;
    t.name = name;
    t.size = size;
    t.align = align;
    return t;
}

static Type arrayOf(const std::string& name, uint64_t size, uint64_t align, uint64_t count) {
    Type t;
    t.kind = Kind::Array;
    t.name = name;
    t.size = size;
    t.align = align;
    t.count = count;
    return t;
}

static Type pad(uint64_t n) {
    Type t;
    t.kind = Kind::Padding;
    t.name = "<pad>";
    t.size = n;
    t.align = 1;
    return t;
}

static Type structOf(const std::string& name, std::vector<Type> members, bool pod = true) {
    Type t;
    t.kind = Kind::Struct;
    t.name = name;
    t.pod = pod;
    t.members = std::move(members);
    return t;
}

static Type unionOf(const std::string& name, std::vector<Type> members, bool pod = true) {
    Type t;
    t.kind = Kind::Union;
    t.name = name;
    t.pod = pod;
    t.members = std::move(members);
    return t;
}

static void testBasicStruct() {
    Layout l = compute(structOf("S", {scalar("a", 4, 4), scalar("b", 1, 1)}));
    CHECK(l.ok);
    CHECK(l.fields[0].offset == 0);
    CHECK(l.fields[1].offset == 4);
    CHECK(l.dsize == 5);
    CHECK(l.size == 8);   // tail padding to align 4
    CHECK(l.align == 4);
}

static void testExplicitPadding() {
    Layout l = compute(structOf("S", {scalar("a", 1, 1), pad(3), scalar("b", 4, 4)}));
    CHECK(l.ok);
    CHECK(l.fields[2].offset == 4);
    CHECK(l.size == 8);
    // padding members do not raise struct alignment
    Layout l2 = compute(structOf("S", {scalar("a", 1, 1), pad(7)}));
    CHECK(l2.ok && l2.align == 1 && l2.size == 8);
}

static void testArrays() {
    Layout l = compute(structOf("S", {scalar("a", 1, 1), arrayOf("arr", 2, 2, 4)}));
    CHECK(l.ok);
    CHECK(l.fields[1].offset == 2);
    CHECK(l.fields[1].size == 8);
    CHECK(l.size == 10);
    CHECK(l.align == 2);
    // zero-length array occupies no space but still contributes alignment
    Layout z = compute(structOf("S", {arrayOf("z", 4, 4, 0), scalar("b", 1, 1)}));
    CHECK(z.ok && z.fields[1].offset == 0 && z.align == 4 && z.size == 4);
}

static void testNestedStruct() {
    Type inner = structOf("Inner", {scalar("c", 1, 1), scalar("d", 4, 4)});
    Layout li = compute(inner);
    CHECK(li.ok && li.size == 8 && li.dsize == 8 && li.align == 4);
    Layout lo = compute(structOf("Outer", {scalar("x", 1, 1), inner, scalar("y", 1, 1)}));
    CHECK(lo.ok);
    CHECK(lo.fields[1].offset == 4);
    CHECK(lo.fields[2].offset == 12);  // POD member occupies full sizeof
    CHECK(lo.size == 16);
}

static void testUnion() {
    Layout l = compute(unionOf("U", {scalar("a", 4, 4), arrayOf("b", 1, 1, 6)}));
    CHECK(l.ok);
    CHECK(l.fields[0].offset == 0 && l.fields[1].offset == 0);
    CHECK(l.size == 8);
    CHECK(l.dsize == 6);
    CHECK(l.align == 4);
}

static void testTailPaddingReuse() {
    // Inner: { u32 i; u8 c; } -> size 8, dsize 5, align 4
    Type innerPod = structOf("Inner", {scalar("i", 4, 4), scalar("c", 1, 1)}, true);
    Type innerNonPod = structOf("Inner", {scalar("i", 4, 4), scalar("c", 1, 1)}, false);

    Layout podOuter = compute(structOf("Outer", {innerPod, scalar("b", 1, 1)}));
    CHECK(podOuter.ok);
    CHECK(podOuter.fields[1].offset == 8);   // no reuse: b after full sizeof(Inner)
    CHECK(podOuter.size == 12);

    Layout nonPodOuter = compute(structOf("Outer", {innerNonPod, scalar("b", 1, 1)}));
    CHECK(nonPodOuter.ok);
    CHECK(nonPodOuter.fields[1].offset == 5);  // b reuses Inner's tail padding
    CHECK(nonPodOuter.dsize == 6);
    CHECK(nonPodOuter.size == 8);

    // diff between the two embeddings reports the offset change first
    Diff d = firstDiff(podOuter, nonPodOuter, "Outer");
    CHECK(d.kind == Diff::Kind::Offset);
    CHECK(d.path == "Outer.b");
    CHECK(d.oldValue == 8 && d.newValue == 5);
}

static void testOverflow() {
    // array size overflow
    Layout a = compute(arrayOf("big", 2, 2, UINT64_MAX));
    CHECK(!a.ok);
    CHECK(a.error.find("overflow") != std::string::npos);
    // member placement overflow: cursor near UINT64_MAX, next align-up wraps
    Layout b = compute(structOf("S", {scalar("a", UINT64_MAX, 1), scalar("b", 8, 8)}));
    CHECK(!b.ok);
    // non-power-of-two alignment rejected
    Layout c = compute(scalar("x", 4, 3));
    CHECK(!c.ok);
    // error propagates through nesting
    Layout d = compute(structOf("S", {arrayOf("big", 2, 2, UINT64_MAX)}));
    CHECK(!d.ok);
}

static void testFirstDiff() {
    Type oldT = structOf("Pkt", {scalar("len", 4, 4), scalar("tag", 1, 1)});
    // identical layouts
    CHECK(firstDiff(compute(oldT), compute(oldT), "Pkt").kind == Diff::Kind::None);

    // offset change: insert explicit padding before tag
    Type newT = structOf("Pkt", {scalar("len", 4, 4), pad(3), scalar("tag", 1, 1)});
    Diff d = firstDiff(compute(oldT), compute(newT), "Pkt");
    CHECK(d.kind == Diff::Kind::MemberCount);  // member count differs first

    Type newT2 = structOf("Pkt", {scalar("len", 4, 4), scalar("tag", 4, 4)});
    d = firstDiff(compute(oldT), compute(newT2), "Pkt");
    CHECK(d.kind == Diff::Kind::Size && d.path == "Pkt.tag");
    CHECK(d.oldValue == 1 && d.newValue == 4);

    // alignment change
    Type newT3 = structOf("Pkt", {scalar("len", 4, 4), scalar("tag", 1, 8)});
    d = firstDiff(compute(oldT), compute(newT3), "Pkt");
    CHECK(d.kind == Diff::Kind::Offset);  // tag moves 4 -> 8 due to align 8
    CHECK(d.path == "Pkt.tag" && d.oldValue == 4 && d.newValue == 8);

    // nested path reported
    Type innerOld = structOf("Inner", {scalar("a", 2, 2)});
    Type innerNew = structOf("Inner", {scalar("a", 4, 4)});
    Type outerOld = structOf("Outer", {scalar("h", 1, 1), innerOld});
    Type outerNew = structOf("Outer", {scalar("h", 1, 1), innerNew});
    d = firstDiff(compute(outerOld), compute(outerNew), "Outer");
    CHECK(d.kind == Diff::Kind::Offset);  // inner moves 2 -> 4
    CHECK(d.path == "Outer.Inner");

    // same offsets, inner field size differs -> nested Size diff
    Type innerOld2 = structOf("Inner", {scalar("a", 2, 2), scalar("b", 2, 2)});
    Type innerNew2 = structOf("Inner", {scalar("a", 2, 2), scalar("b", 4, 4)});
    Type outerOld2 = structOf("Outer", {innerOld2});
    Type outerNew2 = structOf("Outer", {innerNew2});
    d = firstDiff(compute(outerOld2), compute(outerNew2), "Outer");
    CHECK(d.kind == Diff::Kind::Offset);  // Inner.b moves 2 -> 4
    CHECK(d.path == "Outer.Inner.b");

    // union member change
    Type uOld = unionOf("U", {scalar("a", 4, 4), scalar("b", 2, 2)});
    Type uNew = unionOf("U", {scalar("a", 4, 4), scalar("b", 8, 8)});
    d = firstDiff(compute(uOld), compute(uNew), "U");
    CHECK(d.kind == Diff::Kind::Size && d.path == "U.b");
    CHECK(d.oldValue == 2 && d.newValue == 8);

    // total size change only
    Type sOld = structOf("S", {scalar("a", 4, 4)});
    Type sNew = structOf("S", {scalar("a", 4, 8)});
    d = firstDiff(compute(sOld), compute(sNew), "S");
    CHECK(d.kind == Diff::Kind::Align && d.path == "S.a");
}

static void testArrayOfStructs() {
    Type elem = structOf("E", {scalar("x", 4, 4), scalar("y", 1, 1)});
    Type arr;
    arr.kind = Kind::Array;
    arr.name = "items";
    arr.count = 3;
    arr.members.push_back(elem);
    Layout l = compute(structOf("S", {scalar("h", 1, 1), arr}));
    CHECK(l.ok);
    CHECK(l.fields[1].offset == 4);
    CHECK(l.fields[1].size == 24);  // 3 * sizeof(E)=8
    CHECK(l.size == 28);
}

int main() {
    testBasicStruct();
    testExplicitPadding();
    testArrays();
    testNestedStruct();
    testUnion();
    testTailPaddingReuse();
    testOverflow();
    testFirstDiff();
    testArrayOfStructs();
    if (failures == 0) {
        std::cout << "all tests passed\n";
        return 0;
    }
    std::cerr << failures << " check(s) failed\n";
    return 1;
}
