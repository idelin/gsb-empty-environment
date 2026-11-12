#pragma once

#include <cstddef>
#include <cstdint>

namespace netsec {

enum class FieldType : std::uint8_t {
    Invalid = 0,
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
    Struct,
    Vector,
    Union,
};

struct StructLayout;
struct TableLayout;
struct VectorLayout;
struct UnionLayout;

// A type reference used by table, struct, vector, and union layouts.
// Only the pointer matching `type` is read.
struct TypeLayout {
    FieldType type = FieldType::Invalid;
    const StructLayout* struct_layout = nullptr;
    const TableLayout* table_layout = nullptr;
    const VectorLayout* vector_layout = nullptr;
    const UnionLayout* union_layout = nullptr;
};

struct FieldLayout {
    std::uint16_t id = 0;
    TypeLayout type;
    bool required = false;
};

struct StructField {
    std::uint32_t offset = 0;
    TypeLayout type;
};

struct StructLayout {
    std::size_t size = 0;
    std::size_t alignment = 1;
    const StructField* fields = nullptr;
    std::size_t field_count = 0;
    const char* name = nullptr;
};

struct VectorLayout {
    TypeLayout element;
};

struct UnionCase {
    std::uint8_t type = 0;
    TypeLayout value;
};

struct UnionLayout {
    // The value field is represented by a FieldLayout of type Union. This is
    // the vtable slot of the matching uint8 discriminator.
    std::uint16_t type_field_id = 0;
    const UnionCase* cases = nullptr;
    std::size_t case_count = 0;
};

struct TableLayout {
    const FieldLayout* fields = nullptr;
    std::size_t field_count = 0;
    const char* name = nullptr;
};

struct RootLayout {
    // The root table has no runtime type tag in FlatBuffers. Supplying this
    // layout is therefore the caller's assertion about the expected root type.
    const TableLayout* root_table = nullptr;

    // Optional four-byte FlatBuffers file identifier. Pass nullptr to skip the
    // check. A non-null value must point to at least four characters.
    const char* file_identifier = nullptr;
};

struct ValidationLimits {
    std::size_t max_depth = 64;
    std::size_t max_tables = 4096;
    std::size_t max_vtable_bytes = 4096;
    std::size_t max_vector_elements = 1u << 20;
    std::size_t max_string_bytes = 1u << 20;
    std::size_t max_total_elements = 4u << 20;
};

enum class ValidationError : std::uint8_t {
    None = 0,
    InvalidArgument,
    InvalidLayout,
    BufferTooSmall,
    RootOutOfBounds,
    RootMisaligned,
    RootOverlapsHeader,
    FileIdentifierMismatch,
    VtableOffsetInvalid,
    SelfReferentialVtable,
    VtableOutOfBounds,
    VtableMisaligned,
    VtableSizeInvalid,
    VtableOverlapsTable,
    ObjectOutOfBounds,
    ObjectTooSmall,
    FieldOutOfBounds,
    FieldMisaligned,
    RequiredFieldMissing,
    UnionValueMissing,
    UnionValueUnexpected,
    UnionTypeUnknown,
    OffsetInvalid,
    StringOutOfBounds,
    StringTooLong,
    StringNotTerminated,
    VectorOutOfBounds,
    VectorTooLarge,
    LengthOverflow,
    StructOutOfBounds,
    StructMisaligned,
    StructFieldOutOfBounds,
    DepthLimitExceeded,
    TableLimitExceeded,
    TableCycle,
    ElementLimitExceeded,
    OutOfMemory,
};

struct ValidationResult {
    bool valid = false;
    ValidationError error = ValidationError::None;
    std::size_t offset = 0;
    std::size_t table_offset = 0;
    std::size_t field_id = static_cast<std::size_t>(-1);
    std::size_t table_count = 0;
    std::size_t max_depth_seen = 0;
    const char* message = "not validated";

    explicit operator bool() const noexcept { return valid; }
};

const char* ValidationErrorName(ValidationError error) noexcept;

// Reentrant and stateless with respect to the caller: all validation state is
// local to this invocation. The buffer, schema, and file identifier must stay
// alive for the duration of the call.
ValidationResult ValidateFlatBuffer(
    const std::uint8_t* data,
    std::size_t size,
    const RootLayout& root,
    const ValidationLimits& limits = ValidationLimits{}) noexcept;

inline ValidationResult ValidateBuffer(
    const std::uint8_t* data,
    std::size_t size,
    const RootLayout& root,
    const ValidationLimits& limits = ValidationLimits{}) noexcept {
    return ValidateFlatBuffer(data, size, root, limits);
}

}  // namespace netsec
