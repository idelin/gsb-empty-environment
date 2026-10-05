// Bit-exact fixture tests for binrw.
// All expected values below were computed independently (by hand and with an
// independent script) from the documented bit-numbering convention.

#include <cstdint>
#include <cstdio>
#include <vector>

#include "binrw/binrw.hpp"

using namespace binrw;

static int g_failures = 0;
static int g_checks = 0;

#define CHECK(cond)                                                  \
    do {                                                             \
        ++g_checks;                                                  \
        if (!(cond)) {                                               \
            ++g_failures;                                            \
            std::printf("FAIL %s:%d: %s\n", __FILE__, __LINE__, #cond); \
        }                                                            \
    } while (0)

// Payload fixture: 9 bytes / 72 bits.
static const uint8_t kPayload[] = {0xAB, 0xCD, 0xEF, 0x01, 0x23,
                                   0x45, 0x67, 0x89, 0x55};

static std::vector<uint8_t> make_message(uint16_t schema_id,
                                         uint64_t payload_bits,
                                         const uint8_t* payload,
                                         size_t payload_len) {
    Header h;
    h.version = 1;
    h.schema_id = schema_id;
    h.payload_bits = payload_bits;
    std::vector<uint8_t> out(Header::kSize + payload_len);
    h.serialize(out.data());
    std::copy(payload, payload + payload_len, out.begin() + Header::kSize);
    return out;
}

static Schema test_schema() {
    Schema s;
    s.schema_id = 7;
    s.fields = {
        {"f1", 0, 4, ByteOrder::Little, Signedness::Unsigned},
        {"f2", 4, 12, ByteOrder::Little, Signedness::Signed},
        {"f3", 16, 8, ByteOrder::Big, Signedness::Unsigned},
        {"f4", 4, 12, ByteOrder::Big, Signedness::Unsigned},
        {"f5", 3, 64, ByteOrder::Little, Signedness::Unsigned},
        {"f7", 8, 64, ByteOrder::Big, Signedness::Unsigned},
        {"f6", 71, 1, ByteOrder::Little, Signedness::Unsigned},
    };
    return s;
}

static void test_header_fixture() {
    // Expected wire bytes for magic=BWR1, version=1, schema=7, bits=72.
    static const uint8_t expected[] = {0x42, 0x52, 0x57, 0x31, 0x01, 0x00,
                                       0x07, 0x00, 0x48, 0x00, 0x00, 0x00,
                                       0x00, 0x00, 0x00, 0x00};
    Header h;
    h.version = 1;
    h.schema_id = 7;
    h.payload_bits = 72;
    uint8_t buf[Header::kSize];
    h.serialize(buf);
    CHECK(std::equal(std::begin(expected), std::end(expected), buf));

    Header p = Header::parse(buf, sizeof(buf));
    CHECK(p.magic == Header::kMagic);
    CHECK(p.version == 1);
    CHECK(p.schema_id == 7);
    CHECK(p.payload_bits == 72);
}

static void test_field_fixtures() {
    Schema s = test_schema();
    std::vector<uint8_t> raw =
        make_message(7, 72, kPayload, sizeof(kPayload));
    Message m = Message::parse(raw.data(), raw.size(), &s);

    CHECK(m.find("f1")->as_unsigned() == 0xB);
    CHECK(m.find("f2")->as_unsigned() == 0xCDA);
    CHECK(m.find("f2")->as_signed() == -806);  // 0xCDA sign-extended from 12 bits
    CHECK(m.find("f3")->as_unsigned() == 0xF7);
    CHECK(m.find("f4")->as_unsigned() == 0x5B3);
    CHECK(m.find("f5")->as_unsigned() == 0xB12CE8A4603DF9B5ULL);
    CHECK(m.find("f7")->as_unsigned() == 0xB3F780C4A2E691AAULL);
    CHECK(m.find("f6")->as_unsigned() == 0);
}

static void test_roundtrip_identical() {
    Schema s = test_schema();
    std::vector<uint8_t> raw =
        make_message(7, 72, kPayload, sizeof(kPayload));
    Message m = Message::parse(raw.data(), raw.size(), &s);
    std::vector<uint8_t> out = m.encode();
    CHECK(out == raw);
}

static void test_unknown_bits_preserved() {
    // Schema covers only the low nibble of byte 0; everything else is
    // unknown and must survive a parse/encode cycle verbatim.
    Schema s;
    s.schema_id = 1;
    s.fields = {{"nib", 0, 4, ByteOrder::Little, Signedness::Unsigned}};
    std::vector<uint8_t> raw =
        make_message(1, 72, kPayload, sizeof(kPayload));
    Message m = Message::parse(raw.data(), raw.size(), &s);
    CHECK(m.encode() == raw);

    // Modify the known field; unknown bits must be untouched.
    CHECK(m.set("nib", 0x5));
    std::vector<uint8_t> out = m.encode();
    CHECK(out.size() == raw.size());
    CHECK(out[Header::kSize + 0] == 0xA5);  // high nibble preserved
    for (size_t i = 1; i < sizeof(kPayload); ++i) {
        CHECK(out[Header::kSize + i] == kPayload[i]);
    }
}

static void test_unknown_schema_passthrough() {
    std::vector<uint8_t> raw =
        make_message(0xBEEF, 72, kPayload, sizeof(kPayload));
    Message m = Message::parse(raw.data(), raw.size(), nullptr);
    CHECK(m.encode() == raw);
}

static void test_modify_and_reparse() {
    // Non-overlapping fields so writes cannot clobber each other.
    Schema s;
    s.schema_id = 9;
    s.fields = {
        {"a", 0, 4, ByteOrder::Little, Signedness::Unsigned},
        {"b", 4, 12, ByteOrder::Little, Signedness::Signed},
        {"c", 16, 8, ByteOrder::Big, Signedness::Unsigned},
        {"d", 24, 40, ByteOrder::Little, Signedness::Unsigned},
    };
    std::vector<uint8_t> raw =
        make_message(7, 72, kPayload, sizeof(kPayload));
    Message m = Message::parse(raw.data(), raw.size(), &s);
    CHECK(m.set_signed("b", -1));        // 12-bit signed -> 0xFFF
    CHECK(m.set("c", 0x42));
    std::vector<uint8_t> out = m.encode();
    Message m2 = Message::parse(out.data(), out.size(), &s);
    CHECK(m2.find("b")->as_signed() == -1);
    CHECK(m2.find("c")->as_unsigned() == 0x42);
    // Untouched fields keep their original values.
    CHECK(m2.find("a")->as_unsigned() == 0xB);
    CHECK(m2.find("d")->as_unsigned() == 0x8967452301ULL);
}

static void test_sign_extend() {
    CHECK(sign_extend(0xCDA, 12) == -806);
    CHECK(sign_extend(0x2DA, 12) == 730);
    CHECK(sign_extend(1, 1) == -1);
    CHECK(sign_extend(0, 1) == 0);
    CHECK(sign_extend(0xFFFFFFFFFFFFFFFFULL, 64) == -1);
    CHECK(sign_extend(0x8000000000000000ULL, 64) == INT64_MIN);
    CHECK(sign_extend(0x7FFFFFFFFFFFFFFFULL, 64) == INT64_MAX);
    CHECK(sign_extend(0x80, 8) == -128);
    CHECK(sign_extend(0x7F, 8) == 127);
}

static void test_overflow_checks() {
    bool threw = false;
    try {
        (void)bits_to_bytes(UINT64_MAX);
    } catch (const Error&) {
        threw = true;
    }
    CHECK(threw);

    threw = false;
    try {
        (void)checked_mul(UINT64_MAX, 2, "test");
    } catch (const Error&) {
        threw = true;
    }
    CHECK(threw);

    threw = false;
    try {
        (void)checked_add(UINT64_MAX, 1, "test");
    } catch (const Error&) {
        threw = true;
    }
    CHECK(threw);

    // Header declaring an astronomical payload: length arithmetic must
    // overflow (or report truncation), never wrap.
    threw = false;
    try {
        std::vector<uint8_t> raw = make_message(7, UINT64_MAX, kPayload, 1);
        (void)Message::parse(raw.data(), raw.size(), nullptr);
    } catch (const Error&) {
        threw = true;
    }
    CHECK(threw);

    // Large but representable length with a short buffer -> truncation error.
    threw = false;
    try {
        std::vector<uint8_t> raw =
            make_message(7, 1ULL << 40, kPayload, sizeof(kPayload));
        (void)Message::parse(raw.data(), raw.size(), nullptr);
    } catch (const Error&) {
        threw = true;
    }
    CHECK(threw);

    // Schema whose field range overflows u64.
    threw = false;
    try {
        Schema bad;
        bad.fields = {{"boom", UINT64_MAX - 3, 8, ByteOrder::Little,
                       Signedness::Unsigned}};
        std::vector<uint8_t> raw =
            make_message(7, 72, kPayload, sizeof(kPayload));
        (void)Message::parse(raw.data(), raw.size(), &bad);
    } catch (const Error&) {
        threw = true;
    }
    CHECK(threw);

    // Field past the end of the payload.
    threw = false;
    try {
        Schema bad;
        bad.fields = {{"past", 70, 8, ByteOrder::Little,
                       Signedness::Unsigned}};
        std::vector<uint8_t> raw =
            make_message(7, 72, kPayload, sizeof(kPayload));
        (void)Message::parse(raw.data(), raw.size(), &bad);
    } catch (const Error&) {
        threw = true;
    }
    CHECK(threw);
}

static void test_truncation() {
    std::vector<uint8_t> raw =
        make_message(7, 72, kPayload, sizeof(kPayload));
    bool threw = false;
    try {
        (void)Message::parse(raw.data(), raw.size() - 1, nullptr);
    } catch (const Error&) {
        threw = true;
    }
    CHECK(threw);

    threw = false;
    try {
        (void)Message::parse(raw.data(), Header::kSize - 1, nullptr);
    } catch (const Error&) {
        threw = true;
    }
    CHECK(threw);
}

static void test_create_and_encode() {
    Schema s = test_schema();
    Header h;
    h.version = 1;
    h.schema_id = 7;
    h.payload_bits = 72;
    Message m = Message::create(h, &s);
    CHECK(m.set("f1", 0xB));
    CHECK(m.set_signed("f2", -806));
    CHECK(m.set("f3", 0xF7));
    CHECK(m.set("f4", 0x5B3));
    CHECK(m.set("f5", 0xB12CE8A4603DF9B5ULL));
    CHECK(m.set("f7", 0xB3F780C4A2E691AAULL));
    CHECK(m.set("f6", 0));
    std::vector<uint8_t> out = m.encode();
    // Re-encoding the same logical values must reproduce the fixture bytes
    // exactly (fields f1..f7 together cover every payload bit).
    std::vector<uint8_t> expected =
        make_message(7, 72, kPayload, sizeof(kPayload));
    CHECK(out == expected);
}

// The library only assembles integers from individual bytes with explicit
// shifts; this test pins the logical values so any host-endianness leak
// (e.g. a stray memcpy into a uint64_t) would fail here.
static void test_host_endianness_independence() {
    const uint8_t bytes[] = {0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08};
    CHECK(read_bits(bytes, 0, 64, ByteOrder::Little) ==
          0x0807060504030201ULL);
    CHECK(read_bits(bytes, 0, 64, ByteOrder::Big) == 0x8040C020A060E010ULL);
    // Each byte's bits are read LSB-first and become the value's next most
    // significant byte: 0x01 -> 0x80, 0x02 -> 0x40, 0x03 -> 0xC0, ...
}

int main() {
    test_header_fixture();
    test_field_fixtures();
    test_roundtrip_identical();
    test_unknown_bits_preserved();
    test_unknown_schema_passthrough();
    test_modify_and_reparse();
    test_sign_extend();
    test_overflow_checks();
    test_truncation();
    test_create_and_encode();
    test_host_endianness_independence();

    std::printf("%d checks, %d failures\n", g_checks, g_failures);
    return g_failures == 0 ? 0 : 1;
}
