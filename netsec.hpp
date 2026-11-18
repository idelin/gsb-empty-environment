#pragma once

#include <array>
#include <cstddef>
#include <cstdint>

namespace netsec {

// The verifier deliberately does not depend on generated FlatBuffers code.
// A receiver describes the trusted schema once; each received byte buffer is
// then checked against that description before any field is read.
enum class FieldType : std::uint8_t {
    Bool,
    Int8,
    UInt8,
    Int16,
    UInt16,
    Int32,
    UInt32,
    Int64,
    UInt64,
    Float32,
    Float64,
    String,
    Table,
    Vector,
    Union,
    // An inline struct. Its size and alignment must be supplied explicitly
    // through FieldLayout::size and FieldLayout::alignment.
    Struct,
};

enum class VerificationError : std::uint8_t {
    None = 0,
    NullBuffer,
    BufferTooSmall,
    InvalidLayout,
    FileIdentifierMismatch,
    RootOffsetBackwards,
    RootOffsetOutOfBounds,
    RootOffsetUnaligned,
    TableOutOfBounds,
    TableUnaligned,
    VtableSelfReference,
    VtableOutOfBounds,
    VtableUnaligned,
    VtableTooSmall,
    VtableSizeInvalid,
    ObjectSizeInvalid,
    ObjectOutOfBounds,
    VtableObjectOverlap,
    FieldOutOfBounds,
    FieldUnaligned,
    RequiredFieldMissing,
    OffsetBackwards,
    OffsetOutOfBounds,
    OffsetUnaligned,
    StringOutOfBounds,
    StringNotTerminated,
    StringTooLong,
    VectorOutOfBounds,
    VectorUnaligned,
    VectorSizeOverflow,
    VectorTooLong,
    UnionTypeWithoutValue,
    UnionValueWithoutType,
    UnionValueForNone,
    UnionUnknownType,
    UnionInvalidType,
    MaxDepthExceeded,
    MaxTablesExceeded,
    UnsupportedType,
};

using VerifyError = VerificationError;

const char* ToString(VerificationError error) noexcept;

struct TableLayout;
struct VectorLayout;
struct UnionLayout;

// A union discriminator value and the table layout for that variant. TAG 0 is
// reserved for NONE by FlatBuffers and must not be listed here.
struct UnionVariant {
    std::uint64_t tag = 0;
    const TableLayout* table = nullptr;
};

// Describes one table field. SLOT is the zero-based vtable field index.
//
// For FieldType::Table, TABLE points at the nested table layout.
// For FieldType::String, no extra pointer is needed.
// For FieldType::Vector, VECTOR points at the vector layout.
// For FieldType::Union, SLOT is the value/offset slot and UNION_LAYOUT names
// the discriminator slot and its variants.
// For FieldType::Struct, SIZE and ALIGNMENT describe the inline struct.
//
// SIZE and ALIGNMENT may be left zero for scalar and offset fields; they are
// then derived from TYPE. They must be powers of two when supplied.
struct FieldLayout {
    std::uint16_t slot = 0;
    FieldType type = FieldType::UInt8;
    bool required = false;
    std::uint32_t size = 0;
    std::uint32_t alignment = 0;
    const TableLayout* table = nullptr;
    const VectorLayout* vector = nullptr;
    const UnionLayout* union_layout = nullptr;
};

// Describes a FlatBuffers vector. Element offsets (Table, String and nested
// Vector) are four-byte uoffsets. Scalars use their natural size. Inline
// Struct elements require an explicit ELEMENT_SIZE and ALIGNMENT.
struct VectorLayout {
    FieldType element_type = FieldType::UInt8;
    std::uint32_t element_size = 0;
    std::uint32_t alignment = 0;
    const TableLayout* table = nullptr;
    const VectorLayout* vector = nullptr;
};

struct TableLayout {
    const char* name = nullptr;
    const FieldLayout* fields = nullptr;
    std::size_t field_count = 0;
};

// FlatBuffers stores a union as two sibling fields: an unsigned discriminator
// and a uoffset value. TYPE_SLOT is the discriminator slot, VALUE_SLOT is the
// value slot, and TYPE_TYPE must be one of the unsigned integer types.
// A NONE discriminator (zero) requires the value field to be absent.
struct UnionLayout {
    std::uint16_t type_slot = 0;
    std::uint16_t value_slot = 0;
    FieldType type_type = FieldType::UInt8;
    const UnionVariant* variants = nullptr;
    std::size_t variant_count = 0;
};

struct VerifyOptions {
    // SCHEMA.file_identifier is normally used. These options are a convenient
    // fallback for callers that keep the identifier outside the schema.
    std::array<char, 4> file_identifier{};
    bool has_file_identifier = false;

    // Root tables are at depth zero. A value of zero therefore permits only
    // the root table. The hard recursion ceiling is 1024.
    std::uint32_t max_depth = 64;
    std::uint32_t max_tables = 1024;

    // Zero means "limited only by the containing buffer".
    std::uint32_t max_vector_elements = 0;
    std::uint32_t max_string_length = 0;
};

struct BufferSchema {
    const TableLayout* root_table = nullptr;
    std::array<char, 4> file_identifier{};
    bool has_file_identifier = false;
};

struct VerificationResult {
    bool ok = false;
    VerificationError error = VerificationError::None;
    std::size_t offset = 0;
    const char* message = "";
    std::uint32_t depth = 0;

    operator bool() const noexcept { return ok; }
};

// Verifies DATA[0..SIZE) against SCHEMA without reading outside that range.
// The function is reentrant and keeps no mutable global state, so a single
// immutable schema may be shared by multiple receiver threads.
VerificationResult VerifyFlatBuffer(
    const std::uint8_t* data,
    std::size_t size,
    const BufferSchema& schema,
    const VerifyOptions& options = VerifyOptions()) noexcept;

// Boolean convenience wrappers. RESULT, when non-null, receives the detailed
// failure location.
bool VerifyBuffer(
    const std::uint8_t* data,
    std::size_t size,
    const BufferSchema& schema,
    const VerifyOptions& options,
    VerificationResult* result = nullptr) noexcept;

bool VerifyBuffer(
    const std::uint8_t* data,
    std::size_t size,
    const BufferSchema& schema,
    VerificationResult* result) noexcept;

inline bool VerifyBuffer(
    const std::uint8_t* data,
    std::size_t size,
    const BufferSchema& schema) noexcept {
    return VerifyBuffer(data, size, schema, nullptr);
}

bool ValidateFlatBuffer(
    const std::uint8_t* data,
    std::size_t size,
    const BufferSchema& schema,
    const VerifyOptions& options,
    VerificationResult* result = nullptr) noexcept;

inline bool ValidateFlatBuffer(
    const std::uint8_t* data,
    std::size_t size,
    const BufferSchema& schema,
    VerificationResult* result = nullptr) noexcept {
    return ValidateFlatBuffer(data, size, schema, VerifyOptions(), result);
}

bool ValidateBuffer(
    const std::uint8_t* data,
    std::size_t size,
    const BufferSchema& schema,
    const VerifyOptions& options,
    VerificationResult* result = nullptr) noexcept;

inline bool ValidateBuffer(
    const std::uint8_t* data,
    std::size_t size,
    const BufferSchema& schema,
    VerificationResult* result = nullptr) noexcept {
    return ValidateBuffer(data, size, schema, VerifyOptions(), result);
}

// Short alias for callers that only need the detailed result.
inline VerificationResult Verify(
    const std::uint8_t* data,
    std::size_t size,
    const BufferSchema& schema,
    const VerifyOptions& options = VerifyOptions()) noexcept {
    return VerifyFlatBuffer(data, size, schema, options);
}

// Convenience overload when the buffer has no file identifier and the caller
// only has the root table description.
inline VerificationResult VerifyFlatBuffer(
    const std::uint8_t* data,
    std::size_t size,
    const TableLayout& root_table,
    const VerifyOptions& options = VerifyOptions()) noexcept {
    BufferSchema schema;
    schema.root_table = &root_table;
    return VerifyFlatBuffer(data, size, schema, options);
}

inline VerificationResult VerifyFlatBuffer(
    const std::uint8_t* data,
    std::size_t size,
    const TableLayout& root_table,
    const std::array<char, 4>& file_identifier,
    const VerifyOptions& options = VerifyOptions()) noexcept {
    BufferSchema schema;
    schema.root_table = &root_table;
    schema.file_identifier = file_identifier;
    schema.has_file_identifier = true;
    return VerifyFlatBuffer(data, size, schema, options);
}

class BufferVerifier {
public:
    explicit BufferVerifier(
        const BufferSchema& schema,
        const VerifyOptions& options = VerifyOptions()) noexcept;

    VerificationResult Verify(
        const std::uint8_t* data,
        std::size_t size) const noexcept;

private:
    BufferSchema schema_;
    VerifyOptions options_;
};

using FlatBufferVerifier = BufferVerifier;
using Verifier = BufferVerifier;
using Schema = BufferSchema;

}  // namespace netsec
