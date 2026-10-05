// Bit-exact fixture tests for binrw.
//
// Main fixture (hand-computed):
//   header:  42 57 52 31 | 01 00 | 07 00 | 07 00 00 00
//            magic "BWR1"  ver=1   id=7    payload_len=7
//   payload: BC 1A F8 88 80 01 A5
//
//   a: LE u12 @ bit 0  -> 0xABC            (byte0, byte1 low nibble)
//   bits 12..15: unknown gap               (byte1 high nibble = 0x5)
//   b: BE u9  @ bit 16 -> 0x155            (byte2, byte3 bit7)
//   c: LE s6  @ bit 25 -> raw 0x3D -> -3   (byte3 bits 1..6)
//   bit 31: unknown gap                    (byte3 bit0)
//   d: BE s16 @ bit 32 -> raw 0x8001 -> -32767
//   byte 6 (0xA5): entirely unknown, must be preserved verbatim

#include "binrw/binrw.hpp"

#include <cstdio>
#include <vector>

static int g_failures = 0;

#define CHECK(cond)                                                     \
  do {                                                                  \
    if (!(cond)) {                                                      \
      std::printf("FAIL %s:%d: %s\n", __FILE__, __LINE__, #cond);       \
      ++g_failures;                                                     \
    }                                                                   \
  } while (0)

#define CHECK_THROWS(expr)                              \
  do {                                                  \
    bool threw = false;                                 \
    try {                                               \
      (void)(expr);                                     \
    } catch (const binrw::Error&) {                     \
      threw = true;                                     \
    }                                                   \
    if (!threw) {                                       \
      std::printf("FAIL %s:%d: expected throw: %s\n",   \
                  __FILE__, __LINE__, #expr);           \
      ++g_failures;                                     \
    }                                                   \
  } while (0)

using binrw::ByteOrder;
using binrw::FieldDesc;
using binrw::Header;
using binrw::Message;
using binrw::Schema;

static const uint8_t kFixture[] = {
    0x42, 0x57, 0x52, 0x31,              // magic
    0x01, 0x00,                          // version
    0x07, 0x00,                          // schema_id
    0x07, 0x00, 0x00, 0x00,              // payload_len = 7
    0xBC, 0x5A, 0xAA, 0xFA, 0x80, 0x01, 0xA5};

static Schema make_schema() {
  Schema s;
  s.add(FieldDesc{"a", 0, 12, ByteOrder::Little, false});
  s.add(FieldDesc{"b", 16, 9, ByteOrder::Big, false});
  s.add(FieldDesc{"c", 25, 6, ByteOrder::Little, true});
  s.add(FieldDesc{"d", 32, 16, ByteOrder::Big, true});
  return s;
}

static void test_parse_fixture() {
  const Schema s = make_schema();
  const Message m = Message::parse(kFixture, sizeof(kFixture), s);

  CHECK(m.header().version == 1);
  CHECK(m.header().schema_id == 7);
  CHECK(m.header().payload_len == 7);

  CHECK(m.u64("a") == 0xABC);
  CHECK(m.u64("b") == 0x155);
  CHECK(m.u64("c") == 0x3D);
  CHECK(m.i64("c") == -3);
  CHECK(m.u64("d") == 0x8001);
  CHECK(m.i64("d") == -32767);
}

static void test_roundtrip_preserves_unknown_bits() {
  const Schema s = make_schema();
  const Message m = Message::parse(kFixture, sizeof(kFixture), s);
  const std::vector<uint8_t> out = m.encode();
  CHECK(out.size() == sizeof(kFixture));
  CHECK(out == std::vector<uint8_t>(kFixture, kFixture + sizeof(kFixture)));
}

static void test_modify_field_keeps_other_bits() {
  const Schema s = make_schema();

  // Rewrite a = 0x123: byte0 -> 0x23, byte1 low nibble -> 0x1 (was 0xA),
  // byte1 high nibble (unknown gap) and everything else untouched.
  Message m = Message::parse(kFixture, sizeof(kFixture), s);
  m.set("a", 0x1FF123);  // extra high bits must be masked off
  std::vector<uint8_t> out = m.encode();
  const uint8_t expect[] = {0x42, 0x57, 0x52, 0x31, 0x01, 0x00,
                            0x07, 0x00, 0x07, 0x00, 0x00, 0x00,
                            0x23, 0x51, 0xAA, 0xFA, 0x80, 0x01, 0xA5};
  CHECK(out == std::vector<uint8_t>(expect, expect + sizeof(expect)));

  // Rewrite c = -2 (6-bit two's complement 0x3E): byte3 bits 1..6 become
  // 0b111110, bit7 (owned by b) and bit0 (unknown) untouched: 0xFA -> 0xFC.
  Message m2 = Message::parse(kFixture, sizeof(kFixture), s);
  m2.set_signed("c", -2);
  out = m2.encode();
  const uint8_t expect2[] = {0x42, 0x57, 0x52, 0x31, 0x01, 0x00,
                             0x07, 0x00, 0x07, 0x00, 0x00, 0x00,
                             0xBC, 0x5A, 0xAA, 0xFC, 0x80, 0x01, 0xA5};
  CHECK(out == std::vector<uint8_t>(expect2, expect2 + sizeof(expect2)));

  // Re-parse the modified message and confirm logical values.
  const Message m3 = Message::parse(out.data(), out.size(), s);
  CHECK(m3.i64("c") == -2);
  CHECK(m3.u64("a") == 0xABC);
  CHECK(m3.u64("b") == 0x155);
  CHECK(m3.i64("d") == -32767);
}

static void test_full_width_fields() {
  // Two 64-bit fields, LE then BE, over distinct 8-byte words.
  const uint8_t payload[] = {0x01, 0x23, 0x45, 0x67, 0x89, 0xAB, 0xCD, 0xEF,
                             0x01, 0x23, 0x45, 0x67, 0x89, 0xAB, 0xCD, 0xEF};
  uint8_t buf[Header::kSize + sizeof(payload)];
  Header h;
  h.payload_len = sizeof(payload);
  h.encode(buf);
  std::copy(std::begin(payload), std::end(payload), buf + Header::kSize);

  Schema s;
  s.add(FieldDesc{"lo", 0, 64, ByteOrder::Little, false});
  s.add(FieldDesc{"hi", 64, 64, ByteOrder::Big, false});
  const Message m = Message::parse(buf, sizeof(buf), s);
  CHECK(m.u64("lo") == 0xEFCDAB8967452301ull);
  CHECK(m.u64("hi") == 0x0123456789ABCDEFull);
  CHECK(m.encode() == std::vector<uint8_t>(buf, buf + sizeof(buf)));
}

static void test_cross_byte_alignment() {
  const uint8_t payload[] = {0xFF, 0xF0};
  uint8_t buf[Header::kSize + sizeof(payload)];
  Header h;
  h.payload_len = sizeof(payload);
  h.encode(buf);
  std::copy(std::begin(payload), std::end(payload), buf + Header::kSize);

  // Overlapping declarations are rejected, so use one schema per field.
  Schema s_be, s_le;
  s_be.add(FieldDesc{"be", 4, 8, ByteOrder::Big, false});
  s_le.add(FieldDesc{"le", 4, 8, ByteOrder::Little, false});
  CHECK(Message::parse(buf, sizeof(buf), s_be).u64("be") == 0xFF);
  CHECK(Message::parse(buf, sizeof(buf), s_le).u64("le") == 0x0F);

  // Single-bit fields at byte edges.
  Schema bits;
  bits.add(FieldDesc{"b0", 0, 1, ByteOrder::Little, false});
  bits.add(FieldDesc{"b7", 7, 1, ByteOrder::Little, false});
  bits.add(FieldDesc{"b8", 8, 1, ByteOrder::Big, false});
  const Message m = Message::parse(buf, sizeof(buf), bits);
  CHECK(m.u64("b0") == 1);
  CHECK(m.u64("b7") == 1);
  CHECK(m.u64("b8") == 1);
}

static void test_sign_extend() {
  CHECK(binrw::sign_extend(0x1, 1) == -1);
  CHECK(binrw::sign_extend(0x0, 1) == 0);
  CHECK(binrw::sign_extend(0x7F, 7) == -1);
  CHECK(binrw::sign_extend(0x3F, 7) == 63);
  CHECK(binrw::sign_extend(0xFF, 8) == -1);
  CHECK(binrw::sign_extend(0x8001, 16) == -32767);
  CHECK(binrw::sign_extend(0x7FFFFFFF, 32) == 0x7FFFFFFF);
  CHECK(binrw::sign_extend(0x80000000ull, 32) == INT32_MIN);
  CHECK(binrw::sign_extend(0x8000000000000000ull, 64) == INT64_MIN);
  CHECK(binrw::sign_extend(0xFFFFFFFFFFFFFFFFull, 64) == -1);
}

static void test_schema_validation() {
  Schema s;
  CHECK_THROWS(s.add(FieldDesc{"w0", 0, 0, ByteOrder::Little, false}));
  CHECK_THROWS(s.add(FieldDesc{"w65", 0, 65, ByteOrder::Little, false}));
  CHECK_THROWS(s.add(FieldDesc{"ovf", UINT64_MAX - 3, 8, ByteOrder::Little, false}));
  s.add(FieldDesc{"x", 0, 8, ByteOrder::Little, false});
  CHECK_THROWS(s.add(FieldDesc{"x", 8, 8, ByteOrder::Little, false}));   // dup
  CHECK_THROWS(s.add(FieldDesc{"y", 4, 8, ByteOrder::Little, false}));   // overlap
  s.add(FieldDesc{"z", 8, 8, ByteOrder::Little, false});                 // ok
}

static void test_mixed_endian_physical_overlap() {
  // LE field at stream bits 0..11 owns byte1 bits 0..3; a BE field at stream
  // bit 12 owns byte1 bits 3..0 -- disjoint stream ranges, physically aliased.
  Schema s;
  s.add(FieldDesc{"le", 0, 12, ByteOrder::Little, false});
  CHECK_THROWS(s.add(FieldDesc{"be", 12, 9, ByteOrder::Big, false}));
  // Same ranges with matching order do not overlap.
  Schema ok;
  ok.add(FieldDesc{"le", 0, 12, ByteOrder::Little, false});
  ok.add(FieldDesc{"le2", 12, 9, ByteOrder::Little, false});
}

static void test_parse_errors() {
  Schema s;
  s.add(FieldDesc{"a", 0, 8, ByteOrder::Little, false});

  // Truncated header.
  CHECK_THROWS(Message::parse(kFixture, 5, s));
  // Bad magic.
  uint8_t bad_magic[Header::kSize] = {0};
  CHECK_THROWS(Message::parse(bad_magic, sizeof(bad_magic), s));
  // Header declares more payload than available.
  uint8_t trunc[Header::kSize + 2];
  Header h;
  h.payload_len = 100;
  h.encode(trunc);
  CHECK_THROWS(Message::parse(trunc, sizeof(trunc), s));
  // Field range beyond payload bits.
  Schema wide;
  wide.add(FieldDesc{"a", 0, 8, ByteOrder::Little, false});
  wide.add(FieldDesc{"b", 100, 8, ByteOrder::Little, false});
  uint8_t one[Header::kSize + 1];
  h.payload_len = 1;
  h.encode(one);
  CHECK_THROWS(Message::parse(one, sizeof(one), wide));
  // Field range overflowing u64 is rejected at schema build time; also make
  // sure a near-limit offset that fits u64 but exceeds payload is caught.
  Schema huge;
  huge.add(FieldDesc{"a", (uint64_t{1} << 40), 8, ByteOrder::Little, false});
  CHECK_THROWS(Message::parse(one, sizeof(one), huge));
}

static void test_checked_arithmetic() {
  uint64_t out;
  CHECK(!binrw::checked_add_u64(UINT64_MAX, 1, out));
  CHECK(binrw::checked_add_u64(5, 7, out) && out == 12);
  CHECK(!binrw::checked_mul_u64(UINT64_MAX / 2 + 1, 2, out));
  CHECK(!binrw::checked_mul_u64(UINT64_MAX, 8, out));
  CHECK(binrw::checked_mul_u64(0xFFFFFFFFu, 8, out) && out == 0x7FFFFFFF8ull);
}

int main() {
  test_parse_fixture();
  test_roundtrip_preserves_unknown_bits();
  test_modify_field_keeps_other_bits();
  test_full_width_fields();
  test_cross_byte_alignment();
  test_sign_extend();
  test_schema_validation();
  test_mixed_endian_physical_overlap();
  test_parse_errors();
  test_checked_arithmetic();

  if (g_failures == 0) {
    std::printf("all tests passed\n");
    return 0;
  }
  std::printf("%d check(s) failed\n", g_failures);
  return 1;
}
