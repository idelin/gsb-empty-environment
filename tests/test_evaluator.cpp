// Tests for the debug location expression evaluator.
#include <cstdio>
#include <cstring>

#include "debugloc/evaluator.hpp"

using namespace debugloc;

static int failures = 0;
static int checks = 0;

#define CHECK(cond)                                                        \
  do {                                                                     \
    ++checks;                                                              \
    if (!(cond)) {                                                         \
      ++failures;                                                          \
      std::printf("FAIL %s:%d: %s\n", __FILE__, __LINE__, #cond);          \
    }                                                                      \
  } while (0)

#define CHECK_ERR(result, err)                                             \
  do {                                                                     \
    ++checks;                                                              \
    if ((result).error != (err)) {                                         \
      ++failures;                                                          \
      std::printf("FAIL %s:%d: expected error %s, got %s (%s)\n",          \
                  __FILE__, __LINE__, errorName(err),                      \
                  errorName((result).error), (result).message.c_str());    \
    }                                                                      \
  } while (0)

static Evaluator makeEval(const Memory* mem, const RegisterFile* regs,
                          bool littleEndian = true, uint64_t budget = 1000) {
  EvalOptions opts;
  opts.littleEndian = littleEndian;
  opts.budget = budget;
  return Evaluator(mem, regs, opts);
}

// --- basics ---------------------------------------------------------------

static void testLiteralAndArithmetic() {
  SparseMemory mem;
  MapRegisters regs(64);
  auto ev = makeEval(&mem, &regs);
  // (6 * 7) + 1 = 43
  auto r = ev.eval({Op::lit(6, 8), Op::lit(7, 8), Op::simple(OpCode::Mul),
                    Op::lit(1, 8), Op::simple(OpCode::Add)});
  CHECK(r.ok());
  CHECK(r.value == BitValue::constant(43, 8));
}

static void testRegisterRead() {
  SparseMemory mem;
  MapRegisters regs(32);
  regs.set(4, 0xDEADBEEF);
  auto ev = makeEval(&mem, &regs);
  auto r = ev.eval({Op::reg(4)});
  CHECK(r.ok());
  CHECK(r.value == BitValue::constant(0xDEADBEEF, 32));

  // Unlisted register number -> error, not zero.
  auto bad = ev.eval({Op::reg(99)});
  CHECK_ERR(bad, Error::InvalidRegister);

  // Register present but value unavailable -> fully unknown, not zero.
  regs.setUnknown(7);
  auto unk = ev.eval({Op::reg(7)});
  CHECK(unk.ok());
  CHECK(unk.value.isFullyUnknown());
  CHECK(unk.value.width == 32);
}

// --- memory & endianness --------------------------------------------------

static void testMemoryReadEndianness() {
  SparseMemory mem;
  mem.write(0x1000, {0x01, 0x02, 0x03, 0x04});
  MapRegisters regs(64);

  auto le = makeEval(&mem, &regs, true);
  auto r1 = le.eval({Op::derefAt(0x1000, 4)});
  CHECK(r1.ok());
  CHECK(r1.value == BitValue::constant(0x04030201, 32));

  auto be = makeEval(&mem, &regs, false);
  auto r2 = be.eval({Op::derefAt(0x1000, 4)});
  CHECK(r2.ok());
  CHECK(r2.value == BitValue::constant(0x01020304, 32));
}

static void testMissingMemoryIsUnknownNotZero() {
  SparseMemory mem;
  mem.writeByte(0x2001, 0xAB);  // only the middle byte of three exists
  MapRegisters regs(64);
  auto ev = makeEval(&mem, &regs);

  auto r = ev.eval({Op::derefAt(0x2000, 3)});
  CHECK(r.ok());
  CHECK(r.value.width == 24);
  // Little-endian: low byte unknown, middle byte 0xAB, high byte unknown.
  CHECK(r.value.toString() == "xxxxxxxx10101011xxxxxxxx");
  CHECK(!r.value.isFullyKnown());
  CHECK(r.value.bits != 0 || r.value.known != 0);

  // Fully missing region -> fully unknown, never zero.
  auto r2 = ev.eval({Op::derefAt(0x9000, 2)});
  CHECK(r2.ok());
  CHECK(r2.value.isFullyUnknown());
  CHECK(r2.value.width == 16);
}

static void testUnknownPropagatesThroughOps() {
  SparseMemory mem;
  mem.writeByte(0x10, 0x0F);  // byte at 0x11 missing -> high half unknown
  MapRegisters regs(64);
  auto ev = makeEval(&mem, &regs);

  // value = "xxxxxxxx00001111" (16 bits)
  // unknown | 0x00F0: the known 1-bits of 0x00F0 stay known.
  auto r = ev.eval({Op::derefAt(0x10, 2), Op::lit(0x00F0, 16), Op::simple(OpCode::Or)});
  CHECK(r.ok());
  CHECK(r.value.toString() == "xxxxxxxx11111111");

  // unknown & 0x0FF0: known zeros stay zero.
  auto r2 = ev.eval({Op::derefAt(0x10, 2), Op::lit(0x0FF0, 16), Op::simple(OpCode::And)});
  CHECK(r2.ok());
  CHECK(r2.value.toString() == "0000xxxx00000000");

  // unknown + 1: bits below the lowest unknown bit stay exact.
  auto r3 = ev.eval({Op::derefAt(0x10, 2), Op::lit(1, 16), Op::simple(OpCode::Add)});
  CHECK(r3.ok());
  CHECK(r3.value.toString() == "xxxxxxxx00010000");

  // Fully known low bits survive: (x in low nibble only) + 1 keeps bits 4..7.
  SparseMemory mem2;
  mem2.writeByte(0x20, 0x00);
  MapRegisters regs2(64);
  EvalOptions o;
  o.littleEndian = true;
  Evaluator ev2(&mem2, &regs2, o);
  // 0x00 (8-bit, fully known) shifted: sanity check Shl known-mask handling.
  auto r4 = ev2.eval({Op::lit(0x01, 8), Op::shl(4)});
  CHECK(r4.ok());
  CHECK(r4.value == BitValue::constant(0x10, 8));
}

// --- pieces ---------------------------------------------------------------

static void testUnalignedPiece() {
  SparseMemory mem;
  MapRegisters regs(64);
  auto ev = makeEval(&mem, &regs);
  // 16-bit value 0b1011011011001010; take 5 bits at offset 3 (from LSB).
  auto r = ev.eval({Op::lit(0b1011011011001010, 16), Op::piece(5, 3)});
  CHECK(r.ok());
  // bits [3..7] = 0b11001
  CHECK(r.value == BitValue::constant(0b11001, 5));
}

static void testPieceOffsetCountsFromMsbOnBigEndian() {
  SparseMemory mem;
  MapRegisters regs(64);
  // Same value, same piece operands: offset base differs by endianness.
  const uint16_t v = 0b1011011011001010;

  auto le = makeEval(&mem, &regs, true);
  auto rle = le.eval({Op::lit(v, 16), Op::piece(4, 0)});
  CHECK(rle.ok());
  CHECK(rle.value == BitValue::constant(0b1010, 4));  // low nibble

  auto be = makeEval(&mem, &regs, false);
  auto rbe = be.eval({Op::lit(v, 16), Op::piece(4, 0)});
  CHECK(rbe.ok());
  CHECK(rbe.value == BitValue::constant(0b1011, 4));  // high nibble
}

static void testConcatPiecesAcrossMemory() {
  // Rebuild a 12-bit value from two unaligned pieces of a 3-byte little-endian
  // object, one of which contains an unavailable byte.
  SparseMemory mem;
  mem.write(0x3000, {0b11001010, 0b10110110});  // byte 2 missing
  MapRegisters regs(64);
  auto ev = makeEval(&mem, &regs);

  // hi = bits [0..8) of u8  @0x3002  (fully unknown: byte missing)
  // lo = bits [4..8) of u16 @0x3000  (4 bits, unaligned)
  // result = hi:lo (12 bits); Concat pops the low part from the stack top.
  auto r = ev.eval({
      Op::derefAt(0x3002, 1), Op::piece(8, 0),   // hi slice (unknown)
      Op::derefAt(0x3000, 2), Op::piece(4, 4),   // lo slice
      Op::simple(OpCode::Concat),
  });
  CHECK(r.ok());
  CHECK(r.value.width == 12);
  // u16 @0x3000 = 0b1011011011001010; bits [4..8) = 0b1100.
  CHECK(r.value.toString() == "xxxxxxxx1100");
}

static void testPieceOutOfRange() {
  SparseMemory mem;
  MapRegisters regs(64);
  auto ev = makeEval(&mem, &regs);
  auto r = ev.eval({Op::lit(0xFF, 8), Op::piece(6, 4)});  // 6+4 > 8
  CHECK_ERR(r, Error::InvalidWidth);
}

// --- stack discipline -----------------------------------------------------

static void testStackUnderflow() {
  SparseMemory mem;
  MapRegisters regs(64);
  auto ev = makeEval(&mem, &regs);

  CHECK_ERR(ev.eval({Op::simple(OpCode::Drop)}), Error::StackUnderflow);
  CHECK_ERR(ev.eval({Op::lit(1, 8), Op::simple(OpCode::Add)}),
            Error::StackUnderflow);
  CHECK_ERR(ev.eval({Op::simple(OpCode::Concat)}), Error::StackUnderflow);
  // Program that consumes everything: ends with an empty stack.
  CHECK_ERR(ev.eval({Op::lit(1, 8), Op::simple(OpCode::Drop)}),
            Error::StackUnderflow);
}

static void testStackOverflowLimit() {
  SparseMemory mem;
  MapRegisters regs(64);
  EvalOptions opts;
  opts.maxStackDepth = 3;
  Evaluator ev(&mem, &regs, opts);
  auto r = ev.eval({Op::lit(1, 8), Op::lit(2, 8), Op::lit(3, 8), Op::lit(4, 8)});
  CHECK_ERR(r, Error::StackOverflow);
}

// --- budget ---------------------------------------------------------------

static void testBudget() {
  SparseMemory mem;
  mem.write(0x40, {1, 2, 3, 4});
  MapRegisters regs(64);

  // 3 ops with a budget of 2 -> exhausted.
  auto tight = makeEval(&mem, &regs, true, 2);
  auto r = tight.eval({Op::lit(1, 8), Op::lit(2, 8), Op::simple(OpCode::Add)});
  CHECK_ERR(r, Error::BudgetExhausted);

  // Memory bytes are charged too: 1 op + 4 bytes = 5 > budget 4.
  auto tight2 = makeEval(&mem, &regs, true, 4);
  auto r2 = tight2.eval({Op::derefAt(0x40, 4)});
  CHECK_ERR(r2, Error::BudgetExhausted);

  // Same program fits in budget 5.
  auto ok = makeEval(&mem, &regs, true, 5);
  auto r3 = ok.eval({Op::derefAt(0x40, 4)});
  CHECK(r3.ok());
  CHECK(r3.value == BitValue::constant(0x04030201, 32));
}

// --- overflow checks ------------------------------------------------------

static void testAddressOverflow() {
  SparseMemory mem;
  MapRegisters regs(64);
  auto ev = makeEval(&mem, &regs);

  // Immediate address + size wraps the 64-bit address space.
  auto r = ev.eval({Op::derefAt(UINT64_MAX - 1, 4)});
  CHECK_ERR(r, Error::AddressOverflow);

  // Address computed on the stack, then dereferenced.
  regs.set(1, UINT64_MAX - 2);
  auto r2 = ev.eval({Op::reg(1), Op::deref(8)});
  CHECK_ERR(r2, Error::AddressOverflow);

  // Boundary case that exactly fits is fine.
  auto r3 = ev.eval({Op::derefAt(UINT64_MAX, 1)});
  CHECK(r3.ok());
  CHECK(r3.value.isFullyUnknown());  // nothing mapped there
}

static void testArithmeticOverflow() {
  SparseMemory mem;
  MapRegisters regs(64);
  auto ev = makeEval(&mem, &regs);

  auto r = ev.eval({Op::lit(UINT64_MAX, 64), Op::lit(1, 64), Op::simple(OpCode::Add)});
  CHECK_ERR(r, Error::ArithmeticOverflow);

  auto r2 = ev.eval({Op::lit(0, 64), Op::lit(1, 64), Op::simple(OpCode::Sub)});
  CHECK_ERR(r2, Error::ArithmeticOverflow);

  // Narrow width: 0xFF + 1 wraps an 8-bit value.
  auto r3 = ev.eval({Op::lit(0xFF, 8), Op::lit(1, 8), Op::simple(OpCode::Add)});
  CHECK_ERR(r3, Error::ArithmeticOverflow);

  // No wrap -> fine.
  auto r4 = ev.eval({Op::lit(0xFE, 8), Op::lit(1, 8), Op::simple(OpCode::Add)});
  CHECK(r4.ok());
  CHECK(r4.value == BitValue::constant(0xFF, 8));
}

// --- composite: a realistic location program ------------------------------

static void testCompositeProgram() {
  // struct { uint16_t a; uint8_t flags:3, :5; uint32_t *p; } at R0.
  // Evaluate: *(u32*)(R0 + 8) concatenated with the 3-bit flags field.
  SparseMemory mem;
  MapRegisters regs(64);
  regs.set(0, 0x5000);
  mem.write(0x5000, {0x34, 0x12});        // a
  mem.writeByte(0x5002, 0b00000101);     // flags = 0b101 in low 3 bits
  // bytes 0x5003..0x5007 padding, unmapped
  mem.write(0x5008, {0x00, 0x60, 0x00, 0x00});  // p = 0x6000
  mem.write(0x6000, {0xEF, 0xBE, 0xAD, 0xDE});  // *p

  auto ev = makeEval(&mem, &regs);
  auto r = ev.eval({
      Op::reg(0), Op::lit(2, 64), Op::simple(OpCode::Add), Op::deref(1),
      Op::piece(3, 0),             // flags, becomes the high part
      Op::reg(0), Op::lit(8, 64), Op::simple(OpCode::Add),
      Op::deref(4),                // load p
      Op::deref(4),                // load *p
      Op::simple(OpCode::Concat),  // flags(3) : pointee(32) -> 35 bits
  });
  CHECK(r.ok());
  CHECK(r.value.width == 35);
  CHECK(r.value.isFullyKnown());
  CHECK(r.value.bits == ((uint64_t{0b101} << 32) | 0xDEADBEEF));
}

int main() {
  testLiteralAndArithmetic();
  testRegisterRead();
  testMemoryReadEndianness();
  testMissingMemoryIsUnknownNotZero();
  testUnknownPropagatesThroughOps();
  testUnalignedPiece();
  testPieceOffsetCountsFromMsbOnBigEndian();
  testConcatPiecesAcrossMemory();
  testPieceOutOfRange();
  testStackUnderflow();
  testStackOverflowLimit();
  testBudget();
  testAddressOverflow();
  testArithmeticOverflow();
  testCompositeProgram();

  std::printf("%d checks, %d failures\n", checks, failures);
  return failures == 0 ? 0 : 1;
}
