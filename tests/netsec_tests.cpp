#include "netsec.hpp"

#include <cstdint>
#include <cstring>
#include <iostream>
#include <string>
#include <vector>

namespace {

using namespace netsec;

int failures = 0;

void Check(bool condition, const std::string& description) {
    if (!condition) {
        std::cerr << "FAIL: " << description << '\n';
        ++failures;
    }
}

void WriteU16(std::vector<std::uint8_t>* buffer, std::size_t offset,
              std::uint16_t value) {
    (*buffer)[offset] = static_cast<std::uint8_t>(value & 0xffu);
    (*buffer)[offset + 1] =
        static_cast<std::uint8_t>((value >> 8) & 0xffu);
}

void WriteU32(std::vector<std::uint8_t>* buffer, std::size_t offset,
              std::uint32_t value) {
    for (unsigned int index = 0; index < 4; ++index) {
        (*buffer)[offset + index] =
            static_cast<std::uint8_t>((value >> (index * 8)) & 0xffu);
    }
}

void WriteS32(std::vector<std::uint8_t>* buffer, std::size_t offset,
              std::int32_t value) {
    WriteU32(buffer, offset, static_cast<std::uint32_t>(value));
}

// Layout of the valid fixture:
//   0      root uoffset -> 24
//   4      file identifier "TST1"
//   8      root vtable (5 fields)
//   24     root table
//   28     string offset -> 64
//   32     vector offset -> 88
//   36     nested table offset -> 112
//   40     union type (UInt8) = 1
//   44     union value offset -> 160
//   64     string "hello\0"
//   88     vector<UInt32> [1, 2, 3]
//   104    nested table vtable
//   112    nested table { UInt32 = 42 }
//   152    union variant table vtable
//   160    empty union variant table
std::vector<std::uint8_t> MakeValidBuffer() {
    std::vector<std::uint8_t> buffer(256, 0);

    WriteU32(&buffer, 0, 24);
    std::memcpy(buffer.data() + 4, "TST1", 4);

    WriteU16(&buffer, 8, 14);
    WriteU16(&buffer, 10, 24);
    WriteU16(&buffer, 12, 4);
    WriteU16(&buffer, 14, 8);
    WriteU16(&buffer, 16, 12);
    WriteU16(&buffer, 18, 16);
    WriteU16(&buffer, 20, 20);

    WriteS32(&buffer, 24, 16);
    WriteU32(&buffer, 28, 64 - 28);
    WriteU32(&buffer, 32, 88 - 32);
    WriteU32(&buffer, 36, 112 - 36);
    buffer[40] = 1;
    WriteU32(&buffer, 44, 160 - 44);

    WriteU32(&buffer, 64, 5);
    std::memcpy(buffer.data() + 68, "hello", 5);
    buffer[73] = 0;

    WriteU32(&buffer, 88, 3);
    WriteU32(&buffer, 92, 1);
    WriteU32(&buffer, 96, 2);
    WriteU32(&buffer, 100, 3);

    WriteU16(&buffer, 104, 6);
    WriteU16(&buffer, 106, 8);
    WriteU16(&buffer, 108, 4);
    WriteS32(&buffer, 112, 8);
    WriteU32(&buffer, 116, 42);

    WriteU16(&buffer, 152, 4);
    WriteU16(&buffer, 154, 4);
    WriteS32(&buffer, 160, 8);

    return buffer;
}

struct Fixture {
    TableLayout empty{"Empty", nullptr, 0};
    FieldLayout nested_fields[1] = {
        FieldLayout{0, netsec::FieldType::UInt32, true, 0, 0, nullptr,
                    nullptr, nullptr},
    };
    TableLayout nested{"Nested", nested_fields, 1};
    VectorLayout uint32_vector{netsec::FieldType::UInt32, 0, 0, nullptr,
                               nullptr};
    UnionVariant variants[1] = {
        UnionVariant{1, &empty},
    };
    UnionLayout union_layout{3, 4, netsec::FieldType::UInt8, variants, 1};
    FieldLayout root_fields[4] = {
        FieldLayout{0, netsec::FieldType::String, true, 0, 0, nullptr,
                    nullptr, nullptr},
        FieldLayout{1, netsec::FieldType::Vector, true, 0, 0, nullptr,
                    &uint32_vector, nullptr},
        FieldLayout{2, netsec::FieldType::Table, true, 0, 0, &nested,
                    nullptr, nullptr},
        FieldLayout{4, netsec::FieldType::Union, true, 0, 0, nullptr,
                    nullptr, &union_layout},
    };
    TableLayout root{"Root", root_fields, 4};
    std::array<char, 4> identifier{{'T', 'S', 'T', '1'}};
    BufferSchema schema{&root, identifier, true};
};

void ExpectFailure(const std::vector<std::uint8_t>& buffer,
                   const BufferSchema& schema,
                   const netsec::VerifyOptions& options,
                   netsec::VerificationError expected,
                   const std::string& description) {
    const netsec::VerificationResult result =
        netsec::VerifyFlatBuffer(buffer.data(), buffer.size(), schema, options);
    Check(!result.ok, description + " should fail");
    if (!result.ok) {
        Check(result.error == expected,
              description + " error was \"" + result.message + "\"");
    }
}

void TestValidBuffer() {
    Fixture fixture;
    const std::vector<std::uint8_t> buffer = MakeValidBuffer();
    const netsec::VerificationResult result = netsec::VerifyFlatBuffer(
        buffer.data(), buffer.size(), fixture.schema);
    Check(result.ok, "valid buffer should pass");
    Check(result.error == netsec::VerificationError::None,
          "valid buffer has no error");
    Check(netsec::VerifyBuffer(buffer.data(), buffer.size(), fixture.schema,
                               nullptr),
          "boolean wrapper accepts valid buffer");

    netsec::BufferVerifier verifier(fixture.schema);
    Check(verifier.Verify(buffer.data(), buffer.size()).ok,
          "reusable verifier accepts valid buffer");
}

void TestFileIdentifierAndRoot() {
    Fixture fixture;
    std::vector<std::uint8_t> buffer = MakeValidBuffer();
    buffer[4] = 'X';
    ExpectFailure(buffer, fixture.schema, netsec::VerifyOptions(),
                  netsec::VerificationError::FileIdentifierMismatch,
                  "wrong file identifier");

    buffer = MakeValidBuffer();
    WriteU32(&buffer, 0, 0xfffffff0u);
    ExpectFailure(buffer, fixture.schema, netsec::VerifyOptions(),
                  netsec::VerificationError::RootOffsetOutOfBounds,
                  "root offset outside buffer");

    buffer = MakeValidBuffer();
    WriteU32(&buffer, 0, 26);
    ExpectFailure(buffer, fixture.schema, netsec::VerifyOptions(),
                  netsec::VerificationError::RootOffsetUnaligned,
                  "unaligned root offset");
}

void TestSelfReferentialVtable() {
    Fixture fixture;
    std::vector<std::uint8_t> buffer = MakeValidBuffer();
    WriteS32(&buffer, 24, 0);
    ExpectFailure(buffer, fixture.schema, netsec::VerifyOptions(),
                  netsec::VerificationError::VtableSelfReference,
                  "vtable offset zero");

    buffer = MakeValidBuffer();
    WriteS32(&buffer, 24, -4);
    WriteU16(&buffer, 28, 4);
    WriteU16(&buffer, 30, 24);
    ExpectFailure(buffer, fixture.schema, netsec::VerifyOptions(),
                  netsec::VerificationError::VtableObjectOverlap,
                  "vtable placed inside its object");
}

void TestVectorLengthOverflow() {
    Fixture fixture;
    std::vector<std::uint8_t> buffer = MakeValidBuffer();
    WriteU32(&buffer, 88, 0xffffffffu);
    const netsec::VerificationResult result =
        netsec::VerifyFlatBuffer(buffer.data(), buffer.size(), fixture.schema);
    Check(!result.ok, "oversized vector length is rejected");
    Check(result.error == netsec::VerificationError::VectorOutOfBounds ||
              result.error == netsec::VerificationError::VectorSizeOverflow,
          "oversized vector reports bounds or overflow");

    netsec::VerifyOptions options;
    options.max_vector_elements = 2;
    buffer = MakeValidBuffer();
    ExpectFailure(buffer, fixture.schema, options,
                  netsec::VerificationError::VectorTooLong,
                  "vector element limit");
}

void TestStringTermination() {
    Fixture fixture;
    std::vector<std::uint8_t> buffer = MakeValidBuffer();
    buffer[73] = '!';
    ExpectFailure(buffer, fixture.schema, netsec::VerifyOptions(),
                  netsec::VerificationError::StringNotTerminated,
                  "string without NUL terminator");
}

void TestUnionPairing() {
    Fixture fixture;
    std::vector<std::uint8_t> buffer = MakeValidBuffer();
    buffer[40] = 0;
    ExpectFailure(buffer, fixture.schema, netsec::VerifyOptions(),
                  netsec::VerificationError::UnionValueForNone,
                  "union value with NONE type");

    buffer = MakeValidBuffer();
    WriteU16(&buffer, 20, 0);
    ExpectFailure(buffer, fixture.schema, netsec::VerifyOptions(),
                  netsec::VerificationError::UnionTypeWithoutValue,
                  "union type without value");

    buffer = MakeValidBuffer();
    buffer[40] = 9;
    ExpectFailure(buffer, fixture.schema, netsec::VerifyOptions(),
                  netsec::VerificationError::UnionUnknownType,
                  "unknown union type");
}

void TestTraversalLimits() {
    Fixture fixture;
    const std::vector<std::uint8_t> buffer = MakeValidBuffer();

    netsec::VerifyOptions options;
    options.max_depth = 0;
    ExpectFailure(buffer, fixture.schema, options,
                  netsec::VerificationError::MaxDepthExceeded,
                  "maximum depth");

    options = netsec::VerifyOptions();
    options.max_tables = 1;
    ExpectFailure(buffer, fixture.schema, options,
                  netsec::VerificationError::MaxTablesExceeded,
                  "maximum table count");
}

void TestTruncatedBuffersAreSafe() {
    Fixture fixture;
    const std::vector<std::uint8_t> buffer = MakeValidBuffer();
    for (std::size_t size = 0; size < 160; ++size) {
        const netsec::VerificationResult result = netsec::VerifyFlatBuffer(
            buffer.data(), size, fixture.schema);
        Check(!result.ok, "truncated buffer is rejected");
    }
    Check(!netsec::VerifyFlatBuffer(nullptr, 0, fixture.schema).ok,
          "null buffer is rejected");
}

}  // namespace

int main() {
    TestValidBuffer();
    TestFileIdentifierAndRoot();
    TestSelfReferentialVtable();
    TestVectorLengthOverflow();
    TestStringTermination();
    TestUnionPairing();
    TestTraversalLimits();
    TestTruncatedBuffersAreSafe();

    if (failures != 0) {
        std::cerr << failures << " test(s) failed\n";
        return 1;
    }
    std::cout << "all tests passed\n";
    return 0;
}
