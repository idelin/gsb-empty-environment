#include "netsec.hpp"

#include <cstddef>
#include <cstdint>
#include <cstring>
#include <iostream>
#include <limits>
#include <string>
#include <vector>

namespace {

int failures = 0;

void Check(bool condition, const std::string& message) {
    if (!condition) {
        std::cerr << "FAIL: " << message << '\n';
        ++failures;
    }
}

void PutU16(std::vector<std::uint8_t>& buffer, std::size_t offset, std::uint16_t value) {
    buffer[offset] = static_cast<std::uint8_t>(value);
    buffer[offset + 1] = static_cast<std::uint8_t>(value >> 8);
}

void PutU32(std::vector<std::uint8_t>& buffer, std::size_t offset, std::uint32_t value) {
    buffer[offset] = static_cast<std::uint8_t>(value);
    buffer[offset + 1] = static_cast<std::uint8_t>(value >> 8);
    buffer[offset + 2] = static_cast<std::uint8_t>(value >> 16);
    buffer[offset + 3] = static_cast<std::uint8_t>(value >> 24);
}

void PutI32(std::vector<std::uint8_t>& buffer, std::size_t offset, std::int32_t value) {
    std::uint32_t raw = 0;
    std::memcpy(&raw, &value, sizeof(raw));
    PutU32(buffer, offset, raw);
}

const netsec::TypeLayout kUInt8{netsec::FieldType::UInt8};
const netsec::TypeLayout kUInt32{netsec::FieldType::UInt32};
const netsec::TypeLayout kString{netsec::FieldType::String};

const netsec::FieldLayout kVariantFields[] = {
    {0, kUInt32, true},
};
const netsec::TableLayout kVariantTable{kVariantFields, 1, "Variant"};

const netsec::VectorLayout kUInt32Vector{kUInt32};
const netsec::TypeLayout kUInt32VectorType{
    netsec::FieldType::Vector, nullptr, nullptr, &kUInt32Vector, nullptr};

const netsec::UnionCase kUnionCases[] = {
    {1, netsec::TypeLayout{
            netsec::FieldType::Table, nullptr, &kVariantTable, nullptr, nullptr}},
};
const netsec::UnionLayout kUnionLayout{3, kUnionCases, 1};

const netsec::FieldLayout kRootFields[] = {
    {0, kUInt32, true},
    {1, kString, true},
    {2, kUInt32VectorType, true},
    {3, kUInt8, true},
    {4, netsec::TypeLayout{
            netsec::FieldType::Union, nullptr, nullptr, nullptr, &kUnionLayout}, false},
};
const netsec::TableLayout kRootTable{kRootFields, 5, "Root"};
const netsec::RootLayout kRoot{&kRootTable, "TST1"};

std::vector<std::uint8_t> MakeValidBuffer() {
    std::vector<std::uint8_t> buffer(92, 0);
    PutU32(buffer, 0, 24);
    std::memcpy(buffer.data() + 4, "TST1", 4);

    PutU16(buffer, 8, 14);
    PutU16(buffer, 10, 24);
    PutU16(buffer, 12, 4);
    PutU16(buffer, 14, 8);
    PutU16(buffer, 16, 12);
    PutU16(buffer, 18, 16);
    PutU16(buffer, 20, 20);

    PutI32(buffer, 24, 16);
    PutU32(buffer, 28, 0x12345678u);
    PutU32(buffer, 32, 16);
    PutU32(buffer, 36, 24);
    buffer[40] = 1;
    PutU32(buffer, 44, 40);

    PutU32(buffer, 48, 5);
    std::memcpy(buffer.data() + 52, "hello", 5);
    buffer[57] = 0;

    PutU32(buffer, 60, 3);
    PutU32(buffer, 64, 10);
    PutU32(buffer, 68, 20);
    PutU32(buffer, 72, 30);

    PutU16(buffer, 76, 6);
    PutU16(buffer, 78, 8);
    PutU16(buffer, 80, 4);

    PutI32(buffer, 84, 8);
    PutU32(buffer, 88, 7);
    return buffer;
}

void TestValidBuffer() {
    const std::vector<std::uint8_t> buffer = MakeValidBuffer();
    const auto result = netsec::ValidateFlatBuffer(buffer.data(), buffer.size(), kRoot);
    Check(result.valid, "valid buffer accepted");
    Check(result.error == netsec::ValidationError::None, "valid buffer has no error");
    Check(result.table_count == 2, "valid buffer has two tables");
    Check(result.max_depth_seen == 2, "valid buffer depth is two");
}

void TestSelfReferentialVtable() {
    std::vector<std::uint8_t> buffer = MakeValidBuffer();
    PutI32(buffer, 24, 0);
    const auto result = netsec::ValidateFlatBuffer(buffer.data(), buffer.size(), kRoot);
    Check(!result.valid, "self-referential vtable rejected");
    Check(
        result.error == netsec::ValidationError::SelfReferentialVtable,
        "self-referential vtable reports the right error");
}

void TestVectorLengthOverflow() {
    netsec::StructLayout huge;
    huge.size = std::numeric_limits<std::size_t>::max() - 7;
    huge.alignment = 8;
    const netsec::TypeLayout struct_type{
        netsec::FieldType::Struct, &huge, nullptr, nullptr, nullptr};
    const netsec::VectorLayout huge_vector{struct_type};
    const netsec::TypeLayout vector_type{
        netsec::FieldType::Vector, nullptr, nullptr, &huge_vector, nullptr};
    const netsec::FieldLayout fields[] = {
        {0, vector_type, true},
    };
    const netsec::TableLayout table{fields, 1, "OverflowRoot"};
    const netsec::RootLayout root{&table, nullptr};

    std::vector<std::uint8_t> buffer(32, 0);
    PutU32(buffer, 0, 12);
    PutU16(buffer, 4, 6);
    PutU16(buffer, 6, 8);
    PutU16(buffer, 8, 4);
    PutI32(buffer, 12, 8);
    PutU32(buffer, 16, 4);
    PutU32(buffer, 20, 2);

    const auto result = netsec::ValidateFlatBuffer(buffer.data(), buffer.size(), root);
    Check(!result.valid, "overflowing vector rejected");
    Check(
        result.error == netsec::ValidationError::LengthOverflow,
        "overflowing vector reports the right error");
}

void TestStringTerminator() {
    std::vector<std::uint8_t> buffer = MakeValidBuffer();
    buffer[57] = 'x';
    const auto result = netsec::ValidateFlatBuffer(buffer.data(), buffer.size(), kRoot);
    Check(!result.valid, "unterminated string rejected");
    Check(
        result.error == netsec::ValidationError::StringNotTerminated,
        "unterminated string reports the right error");
}

void TestUnionPairing() {
    std::vector<std::uint8_t> missing_value = MakeValidBuffer();
    PutU32(missing_value, 44, 0);
    const auto missing = netsec::ValidateFlatBuffer(
        missing_value.data(), missing_value.size(), kRoot);
    Check(!missing.valid, "union tag without value rejected");
    Check(
        missing.error == netsec::ValidationError::UnionValueMissing,
        "missing union value reports the right error");

    std::vector<std::uint8_t> unexpected_value = MakeValidBuffer();
    unexpected_value[40] = 0;
    const auto unexpected = netsec::ValidateFlatBuffer(
        unexpected_value.data(), unexpected_value.size(), kRoot);
    Check(!unexpected.valid, "union value without tag rejected");
    Check(
        unexpected.error == netsec::ValidationError::UnionValueUnexpected,
        "unexpected union value reports the right error");
}

void TestLimits() {
    const std::vector<std::uint8_t> buffer = MakeValidBuffer();
    netsec::ValidationLimits limits;
    limits.max_tables = 1;
    const auto table_result =
        netsec::ValidateFlatBuffer(buffer.data(), buffer.size(), kRoot, limits);
    Check(!table_result.valid, "table limit enforced");
    Check(
        table_result.error == netsec::ValidationError::TableLimitExceeded,
        "table limit reports the right error");

    limits = netsec::ValidationLimits{};
    limits.max_depth = 1;
    const auto depth_result =
        netsec::ValidateFlatBuffer(buffer.data(), buffer.size(), kRoot, limits);
    Check(!depth_result.valid, "depth limit enforced");
    Check(
        depth_result.error == netsec::ValidationError::DepthLimitExceeded,
        "depth limit reports the right error");
}

void TestFileIdentifier() {
    std::vector<std::uint8_t> buffer = MakeValidBuffer();
    std::memcpy(buffer.data() + 4, "BAD!", 4);
    const auto result = netsec::ValidateFlatBuffer(buffer.data(), buffer.size(), kRoot);
    Check(!result.valid, "file identifier mismatch rejected");
    Check(
        result.error == netsec::ValidationError::FileIdentifierMismatch,
        "file identifier mismatch reports the right error");
}

void TestTruncationAndMutation() {
    const std::vector<std::uint8_t> buffer = MakeValidBuffer();
    for (std::size_t size = 0; size < buffer.size(); ++size) {
        const auto result =
            netsec::ValidateFlatBuffer(buffer.data(), size, kRoot);
        Check(!result.valid, "truncated buffer rejected at size " + std::to_string(size));
    }

    std::vector<std::uint8_t> mutated = buffer;
    std::uint32_t state = 0x12345678u;
    for (std::size_t i = 0; i < 2000; ++i) {
        state = state * 1103515245u + 12345u;
        const std::size_t offset = state % mutated.size();
        state = state * 1103515245u + 12345u;
        mutated[offset] = static_cast<std::uint8_t>(state >> 24);
        (void)netsec::ValidateFlatBuffer(mutated.data(), mutated.size(), kRoot);
    }
}

}  // namespace

int main() {
    TestValidBuffer();
    TestSelfReferentialVtable();
    TestVectorLengthOverflow();
    TestStringTerminator();
    TestUnionPairing();
    TestLimits();
    TestFileIdentifier();
    TestTruncationAndMutation();

    if (failures != 0) {
        std::cerr << failures << " test(s) failed\n";
        return 1;
    }
    std::cout << "all netsec tests passed\n";
    return 0;
}
