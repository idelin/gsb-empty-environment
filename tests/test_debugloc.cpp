#include "debugloc/debugloc.hpp"

#include <cstdio>
#include <map>

using namespace debugloc;

static int failures = 0;
static int checks = 0;

#define CHECK(cond)                                                        \
    do {                                                                   \
        ++checks;                                                          \
        if (!(cond)) {                                                     \
            ++failures;                                                    \
            std::printf("FAIL %s:%d: %s\n", __FILE__, __LINE__, #cond);    \
        }                                                                  \
    } while (0)

class MapMemory : public Memory {
public:
    std::map<uint64_t, uint8_t> bytes;
    std::vector<std::optional<uint8_t>> read(uint64_t addr,
                                             size_t size) const override {
        std::vector<std::optional<uint8_t>> out(size);
        for (size_t i = 0; i < size; ++i) {
            auto it = bytes.find(addr + i);
            if (it != bytes.end())
                out[i] = it->second;
        }
        return out;
    }
};

class MapRegs : public Registers {
public:
    std::map<uint32_t, std::vector<uint8_t>> regs;
    std::optional<std::vector<uint8_t>> read(uint32_t id) const override {
        auto it = regs.find(id);
        if (it == regs.end())
            return std::nullopt;
        return it->second;
    }
};

static MapMemory g_mem;
static MapRegs g_regs;

static Result<Value> run(const std::vector<Instr>& prog,
                         EvalOptions opts = {}) {
    Evaluator ev(g_mem, g_regs, opts);
    return ev.run(prog);
}

static void testArithmetic() {
    auto r = run({Instr::pushConst(3), Instr::pushConst(4), Instr::add()});
    CHECK(r && r.value().toUint64() == 7);

    r = run({Instr::pushConst(10), Instr::pushConst(4), Instr::sub()});
    CHECK(r && r.value().toUint64() == 6);

    r = run({Instr::pushConst(6), Instr::pushConst(7), Instr::mul()});
    CHECK(r && r.value().toUint64() == 42);
}

static void testOverflowChecks() {
    // UINT64_MAX + 1 wraps.
    auto r = run({Instr::pushConst(UINT64_MAX), Instr::pushConst(1),
                  Instr::add()});
    CHECK(!r && r.error() == Error::AddressOverflow);

    // 5 - 10 underflows.
    r = run({Instr::pushConst(5), Instr::pushConst(10), Instr::sub()});
    CHECK(!r && r.error() == Error::AddressOverflow);

    // 2^32 * 2^32 overflows.
    r = run({Instr::pushConst(uint64_t{1} << 32),
             Instr::pushConst(uint64_t{1} << 32), Instr::mul()});
    CHECK(!r && r.error() == Error::AddressOverflow);

    // Boundary: UINT64_MAX + 0 is fine.
    r = run({Instr::pushConst(UINT64_MAX), Instr::pushConst(0), Instr::add()});
    CHECK(r && r.value().toUint64() == UINT64_MAX);

    // Deref whose address range wraps the address space.
    r = run({Instr::pushConst(UINT64_MAX - 1), Instr::deref(4)});
    CHECK(!r && r.error() == Error::AddressOverflow);

    // Boundary: [MAX-1, MAX] exactly fits.
    r = run({Instr::pushConst(UINT64_MAX - 1), Instr::deref(2)});
    CHECK(r);
}

static void testStackUnderflow() {
    auto r = run({Instr::add()});
    CHECK(!r && r.error() == Error::StackUnderflow);

    r = run({Instr::pushConst(1), Instr::add()});
    CHECK(!r && r.error() == Error::StackUnderflow);

    r = run({Instr::pushConst(1), Instr::concat()});
    CHECK(!r && r.error() == Error::StackUnderflow);

    r = run({Instr::deref(4)});
    CHECK(!r && r.error() == Error::StackUnderflow);

    // Empty program leaves nothing to return.
    r = run({});
    CHECK(!r && r.error() == Error::StackUnderflow);
}

static void testBudget() {
    EvalOptions opts;
    opts.maxOps = 3;
    auto r = run({Instr::pushConst(1), Instr::pushConst(2), Instr::add()},
                 opts);
    CHECK(r && r.value().toUint64() == 3);

    opts.maxOps = 2;
    r = run({Instr::pushConst(1), Instr::pushConst(2), Instr::add()}, opts);
    CHECK(!r && r.error() == Error::BudgetExhausted);
}

static void testEndianness() {
    g_mem.bytes.clear();
    g_mem.bytes[0x10] = 0x01;
    g_mem.bytes[0x11] = 0x02;

    // Little-endian (default): byte 0 is LSB.
    auto r = run({Instr::pushConst(0x10), Instr::deref(2)});
    CHECK(r && r.value().toUint64() == 0x0201);

    // Switch to big-endian: byte 0 is MSB.
    r = run({Instr::setEndian(Endian::Big), Instr::pushConst(0x10),
             Instr::deref(2)});
    CHECK(r && r.value().toUint64() == 0x0102);

    // Switch mid-expression: little deref minus big deref = 0x0201 - 0x0102.
    r = run({Instr::pushConst(0x10), Instr::deref(2),
             Instr::setEndian(Endian::Big), Instr::pushConst(0x10),
             Instr::deref(2), Instr::sub()});
    CHECK(r && r.value().toUint64() == 0x00FF);

    // Big-endian via initial options.
    EvalOptions opts;
    opts.endian = Endian::Big;
    r = run({Instr::pushConst(0x10), Instr::deref(2)}, opts);
    CHECK(r && r.value().toUint64() == 0x0102);
}

static void testMissingMemoryIsUnknownNotZero() {
    g_mem.bytes.clear();

    // Fully missing: 4-byte deref is fully unknown, not zero.
    auto r = run({Instr::pushConst(0x500), Instr::deref(4)});
    CHECK(r);
    CHECK(!r.value().fullyKnown());
    CHECK(!r.value().toUint64().has_value());
    CHECK(r.value().bitSize() == 32);

    // Partially missing: bytes 0 and 2 known, byte 1 unknown.
    g_mem.bytes[0x200] = 0xAA;
    g_mem.bytes[0x202] = 0xCC;
    r = run({Instr::pushConst(0x200), Instr::deref(3)});
    CHECK(r);
    const Value& v = r.value();
    CHECK(!v.toUint64().has_value());
    for (size_t i = 0; i < 8; ++i)
        CHECK(v.bitKnown(i));
    for (size_t i = 8; i < 16; ++i)
        CHECK(!v.bitKnown(i));
    for (size_t i = 16; i < 24; ++i)
        CHECK(v.bitKnown(i));
    CHECK(v.slice(0, 8).toUint64() == 0xAA);
    CHECK(v.slice(16, 8).toUint64() == 0xCC);

    // Unknown address cannot be dereferenced.
    r = run({Instr::pushConst(0x900), Instr::deref(8), Instr::deref(1)});
    CHECK(!r && r.error() == Error::UnknownAddress);

    // Unknown arithmetic operand propagates unknown.
    r = run({Instr::pushConst(0x900), Instr::deref(8), Instr::pushConst(1),
             Instr::add()});
    CHECK(r && !r.value().fullyKnown());
}

static void testUnalignedBitField() {
    // Within a byte: bits [1,5) of 0b00101101 = 0b0110.
    auto r = run({Instr::pushConst(0x2D), Instr::bitField(1, 4)});
    CHECK(r && r.value().toUint64() == 0x6);
    CHECK(r.value().bitSize() == 4);

    // Across a byte boundary: bits [4,12) of 0xBEEF = 0xEE.
    r = run({Instr::pushConst(0xBEEF), Instr::bitField(4, 8)});
    CHECK(r && r.value().toUint64() == 0xEE);

    // Offset 3, size 13 across three bytes of 0x012345.
    r = run({Instr::pushConst(0x012345), Instr::bitField(3, 13)});
    CHECK(r && r.value().toUint64() == ((0x012345 >> 3) & 0x1FFF));

    // Out of range.
    r = run({Instr::pushConst(0xFF), Instr::bitField(60, 8)});
    CHECK(!r && r.error() == Error::InvalidBitRange);

    r = run({Instr::pushConst(0xFF), Instr::bitField(0, 4),
             Instr::bitField(2, 3)});
    CHECK(!r && r.error() == Error::InvalidBitRange);
}

static void testConcat() {
    // 3-bit 0b101 below 2-bit 0b10 -> 0b10101.
    auto r = run({Instr::pushConst(0b101), Instr::bitField(0, 3),
                  Instr::pushConst(0b10), Instr::bitField(0, 2),
                  Instr::concat()});
    CHECK(r && r.value().bitSize() == 5);
    CHECK(r.value().toUint64() == 0b10101);

    // Known low bits + unknown high bits (from missing memory).
    g_mem.bytes.clear();
    r = run({Instr::pushConst(0x7), Instr::bitField(0, 3),
             Instr::pushConst(0x500), Instr::deref(1), Instr::bitField(0, 2),
             Instr::concat()});
    CHECK(r);
    const Value& v = r.value();
    CHECK(v.bitSize() == 5);
    CHECK(!v.fullyKnown());
    for (size_t i = 0; i < 3; ++i) {
        CHECK(v.bitKnown(i));
        CHECK(v.bit(i));
    }
    CHECK(!v.bitKnown(3));
    CHECK(!v.bitKnown(4));

    // Unaligned concat then slice: reconstruct a value split at bit 5.
    g_mem.bytes[0x30] = 0xAB;
    r = run({Instr::pushConst(0x30), Instr::deref(1), Instr::bitField(0, 5),
             Instr::pushConst(0x30), Instr::deref(1), Instr::bitField(5, 3),
             Instr::concat()});
    CHECK(r && r.value().toUint64() == 0xAB);
}

static void testRegisters() {
    g_regs.regs.clear();
    g_regs.regs[3] = {0xAA, 0xBB}; // little-endian: 0xBBAA

    auto r = run({Instr::loadReg(3)});
    CHECK(r && r.value().toUint64() == 0xBBAA);
    CHECK(r.value().bitSize() == 16);

    r = run({Instr::loadReg(9)});
    CHECK(!r && r.error() == Error::RegisterUnavailable);

    // Register as base address for a deref.
    g_mem.bytes.clear();
    g_mem.bytes[0xBBAA] = 0x42;
    r = run({Instr::loadReg(3), Instr::deref(1)});
    CHECK(r && r.value().toUint64() == 0x42);
}

int main() {
    testArithmetic();
    testOverflowChecks();
    testStackUnderflow();
    testBudget();
    testEndianness();
    testMissingMemoryIsUnknownNotZero();
    testUnalignedBitField();
    testConcat();
    testRegisters();

    std::printf("%d checks, %d failures\n", checks, failures);
    return failures == 0 ? 0 : 1;
}
