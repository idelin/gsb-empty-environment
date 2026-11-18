#include "netsec.hpp"

#include <algorithm>
#include <cstring>
#include <limits>

namespace netsec {
namespace {

constexpr std::uint32_t kHardMaxDepth = 1024;

bool IsPowerOfTwo(std::uint32_t value) noexcept {
    return value != 0 && (value & (value - 1)) == 0;
}

std::uint32_t ScalarSize(FieldType type) noexcept {
    switch (type) {
        case FieldType::Bool:
        case FieldType::Int8:
        case FieldType::UInt8:
            return 1;
        case FieldType::Int16:
        case FieldType::UInt16:
            return 2;
        case FieldType::Int32:
        case FieldType::UInt32:
        case FieldType::Float32:
            return 4;
        case FieldType::Int64:
        case FieldType::UInt64:
        case FieldType::Float64:
            return 8;
        default:
            return 0;
    }
}

bool IsUnsignedInteger(FieldType type) noexcept {
    switch (type) {
        case FieldType::Bool:
        case FieldType::UInt8:
        case FieldType::UInt16:
        case FieldType::UInt32:
        case FieldType::UInt64:
            return true;
        default:
            return false;
    }
}

std::uint16_t ReadU16(const std::uint8_t* data) noexcept {
    return static_cast<std::uint16_t>(
        static_cast<std::uint16_t>(data[0]) |
        static_cast<std::uint16_t>(static_cast<std::uint16_t>(data[1]) << 8));
}

std::uint32_t ReadU32(const std::uint8_t* data) noexcept {
    return static_cast<std::uint32_t>(data[0]) |
           (static_cast<std::uint32_t>(data[1]) << 8) |
           (static_cast<std::uint32_t>(data[2]) << 16) |
           (static_cast<std::uint32_t>(data[3]) << 24);
}

std::uint64_t ReadU64(const std::uint8_t* data) noexcept {
    std::uint64_t value = 0;
    for (unsigned int index = 0; index < 8; ++index) {
        value |= static_cast<std::uint64_t>(data[index]) << (index * 8);
    }
    return value;
}

std::int64_t ReadS32(const std::uint8_t* data) noexcept {
    const std::uint32_t value = ReadU32(data);
    if ((value & 0x80000000u) != 0) {
        return static_cast<std::int64_t>(value) - 0x100000000LL;
    }
    return static_cast<std::int64_t>(value);
}

std::uint64_t ReadUnsigned(const std::uint8_t* data, std::uint32_t size) noexcept {
    switch (size) {
        case 1:
            return data[0];
        case 2:
            return ReadU16(data);
        case 4:
            return ReadU32(data);
        case 8:
            return ReadU64(data);
        default:
            return 0;
    }
}

struct FieldGeometry {
    std::uint32_t size = 0;
    std::uint32_t alignment = 1;
};

struct VectorGeometry {
    std::uint32_t element_size = 0;
    std::uint32_t alignment = 1;
};

class VerifierImpl {
public:
    VerifierImpl(
        const std::uint8_t* data,
        std::size_t size,
        const BufferSchema& schema,
        const VerifyOptions& options) noexcept
        : data_(data),
          size_(size),
          schema_(schema),
          options_(options),
          max_depth_(std::min(options.max_depth, kHardMaxDepth)) {
        result_.ok = true;
    }

    VerificationResult Run() noexcept {
        if (!ValidateSchema()) {
            return result_;
        }
        if (data_ == nullptr) {
            return Fail(VerificationError::NullBuffer, 0, 0);
        }
        if (size_ < 4) {
            return Fail(VerificationError::BufferTooSmall, 0, 0);
        }

        if (!VerifyFileIdentifier()) {
            return result_;
        }

        const std::uint32_t root_offset = ReadU32(data_);
        if (root_offset < 4) {
            return Fail(VerificationError::RootOffsetBackwards, 0, 0);
        }
        if ((root_offset & 3u) != 0) {
            return Fail(VerificationError::RootOffsetUnaligned, 0, 0);
        }
        if (root_offset > size_ - 4) {
            return Fail(VerificationError::RootOffsetOutOfBounds, 0, 0);
        }

        if (!VerifyTable(root_offset, *schema_.root_table, 0)) {
            return result_;
        }

        result_.ok = true;
        result_.error = VerificationError::None;
        result_.offset = 0;
        result_.message = ToString(VerificationError::None);
        result_.depth = 0;
        return result_;
    }

private:
    bool InRange(std::size_t position, std::size_t length) const noexcept {
        return position <= size_ && length <= size_ - position;
    }

    VerificationResult Fail(
        VerificationError error,
        std::size_t offset,
        std::uint32_t depth) noexcept {
        result_.ok = false;
        result_.error = error;
        result_.offset = offset;
        result_.message = ToString(error);
        result_.depth = depth;
        return result_;
    }

    bool FailBool(
        VerificationError error,
        std::size_t offset,
        std::uint32_t depth) noexcept {
        Fail(error, offset, depth);
        return false;
    }

    bool ValidateSchema() noexcept {
        if (schema_.root_table == nullptr) {
            return FailBool(VerificationError::InvalidLayout, 0, 0);
        }
        if (schema_.root_table->field_count != 0 &&
            schema_.root_table->fields == nullptr) {
            return FailBool(VerificationError::InvalidLayout, 0, 0);
        }
        if (schema_.has_file_identifier && options_.has_file_identifier &&
            std::memcmp(schema_.file_identifier.data(),
                        options_.file_identifier.data(),
                        schema_.file_identifier.size()) != 0) {
            return FailBool(VerificationError::InvalidLayout, 0, 0);
        }
        return true;
    }

    bool VerifyFileIdentifier() noexcept {
        if (!schema_.has_file_identifier && !options_.has_file_identifier) {
            return true;
        }

        const std::array<char, 4>& expected =
            schema_.has_file_identifier ? schema_.file_identifier
                                        : options_.file_identifier;
        if (size_ < 8) {
            return FailBool(VerificationError::FileIdentifierMismatch, 0, 0);
        }
        if (std::memcmp(data_ + 4, expected.data(), expected.size()) != 0) {
            return FailBool(VerificationError::FileIdentifierMismatch, 4, 0);
        }
        return true;
    }

    bool PrepareField(
        const FieldLayout& field,
        FieldGeometry* geometry) noexcept {
        if (geometry == nullptr ||
            (field.table == nullptr && field.type == FieldType::Table)) {
            return FailBool(VerificationError::InvalidLayout, 0, 0);
        }

        if (field.type == FieldType::Struct) {
            if (field.size == 0) {
                return FailBool(VerificationError::InvalidLayout, 0, 0);
            }
            const std::uint32_t alignment =
                field.alignment == 0 ? 1 : field.alignment;
            if (!IsPowerOfTwo(alignment) || alignment > field.size ||
                (field.size % alignment) != 0) {
                return FailBool(VerificationError::InvalidLayout, 0, 0);
            }
            geometry->size = field.size;
            geometry->alignment = alignment;
            return true;
        }

        if (field.type == FieldType::Vector) {
            if (field.vector == nullptr) {
                return FailBool(VerificationError::InvalidLayout, 0, 0);
            }
            geometry->size = 4;
            geometry->alignment = 4;
            return true;
        }

        if (field.type == FieldType::Union) {
            if (field.union_layout == nullptr ||
                field.union_layout->type_slot ==
                    field.union_layout->value_slot) {
                return FailBool(VerificationError::InvalidLayout, 0, 0);
            }
            geometry->size = 4;
            geometry->alignment = 4;
            return true;
        }

        if (field.type == FieldType::String || field.type == FieldType::Table) {
            geometry->size = 4;
            geometry->alignment = 4;
            return true;
        }

        const std::uint32_t scalar_size = ScalarSize(field.type);
        if (scalar_size == 0) {
            return FailBool(VerificationError::UnsupportedType, 0, 0);
        }
        if (field.size != 0 && field.size != scalar_size) {
            return FailBool(VerificationError::InvalidLayout, 0, 0);
        }
        if (field.alignment != 0 && field.alignment != scalar_size) {
            return FailBool(VerificationError::InvalidLayout, 0, 0);
        }
        geometry->size = scalar_size;
        geometry->alignment = scalar_size;
        return true;
    }

    bool PrepareVector(
        const VectorLayout& vector,
        VectorGeometry* geometry) noexcept {
        if (geometry == nullptr) {
            return FailBool(VerificationError::InvalidLayout, 0, 0);
        }

        if (vector.element_type == FieldType::Struct) {
            if (vector.element_size == 0) {
                return FailBool(VerificationError::InvalidLayout, 0, 0);
            }
            const std::uint32_t alignment =
                vector.alignment == 0 ? 1 : vector.alignment;
            if (!IsPowerOfTwo(alignment) ||
                alignment > vector.element_size ||
                (vector.element_size % alignment) != 0) {
                return FailBool(VerificationError::InvalidLayout, 0, 0);
            }
            geometry->element_size = vector.element_size;
            geometry->alignment = alignment;
            return true;
        }

        if (vector.element_type == FieldType::Table) {
            if (vector.table == nullptr) {
                return FailBool(VerificationError::InvalidLayout, 0, 0);
            }
            geometry->element_size = 4;
            geometry->alignment = 4;
            return true;
        }

        if (vector.element_type == FieldType::String ||
            vector.element_type == FieldType::Vector) {
            if (vector.element_type == FieldType::Vector &&
                vector.vector == nullptr) {
                return FailBool(VerificationError::InvalidLayout, 0, 0);
            }
            geometry->element_size = 4;
            geometry->alignment = 4;
            return true;
        }

        if (vector.element_type == FieldType::Union) {
            return FailBool(VerificationError::UnsupportedType, 0, 0);
        }

        const std::uint32_t scalar_size = ScalarSize(vector.element_type);
        if (scalar_size == 0) {
            return FailBool(VerificationError::UnsupportedType, 0, 0);
        }
        if (vector.element_size != 0 &&
            vector.element_size != scalar_size) {
            return FailBool(VerificationError::InvalidLayout, 0, 0);
        }
        if (vector.alignment != 0 && vector.alignment != scalar_size) {
            return FailBool(VerificationError::InvalidLayout, 0, 0);
        }
        geometry->element_size = scalar_size;
        geometry->alignment = scalar_size;
        return true;
    }

    bool VerifyTable(
        std::size_t table_position,
        const TableLayout& table,
        std::uint32_t depth) noexcept {
        if (!result_.ok) {
            return false;
        }
        if (depth > max_depth_) {
            return FailBool(
                VerificationError::MaxDepthExceeded, table_position, depth);
        }
        if (table_count_ >= options_.max_tables) {
            return FailBool(
                VerificationError::MaxTablesExceeded, table_position, depth);
        }
        ++table_count_;

        if (!InRange(table_position, 4)) {
            return FailBool(
                VerificationError::TableOutOfBounds, table_position, depth);
        }
        if ((table_position & 3u) != 0) {
            return FailBool(
                VerificationError::TableUnaligned, table_position, depth);
        }
        if (table.field_count != 0 && table.fields == nullptr) {
            return FailBool(
                VerificationError::InvalidLayout, table_position, depth);
        }

        const std::int64_t vtable_delta = ReadS32(data_ + table_position);
        if (vtable_delta == 0) {
            return FailBool(
                VerificationError::VtableSelfReference, table_position, depth);
        }
        const std::int64_t vtable_signed =
            static_cast<std::int64_t>(table_position) - vtable_delta;
        if (vtable_signed < 0 ||
            static_cast<std::uint64_t>(vtable_signed) > size_ - 4) {
            return FailBool(
                VerificationError::VtableOutOfBounds, table_position, depth);
        }
        const std::size_t vtable_position =
            static_cast<std::size_t>(vtable_signed);
        if ((vtable_position & 1u) != 0) {
            return FailBool(
                VerificationError::VtableUnaligned, vtable_position, depth);
        }

        const std::uint16_t vtable_size =
            ReadU16(data_ + vtable_position);
        const std::uint16_t object_size =
            ReadU16(data_ + vtable_position + 2);
        if (vtable_size < 4) {
            return FailBool(
                VerificationError::VtableTooSmall, vtable_position, depth);
        }
        if ((vtable_size & 1u) != 0) {
            return FailBool(
                VerificationError::VtableSizeInvalid, vtable_position, depth);
        }
        if (!InRange(vtable_position, vtable_size)) {
            return FailBool(
                VerificationError::VtableOutOfBounds, vtable_position, depth);
        }
        if (object_size < 4) {
            return FailBool(
                VerificationError::ObjectSizeInvalid, table_position, depth);
        }
        if (!InRange(table_position, object_size)) {
            return FailBool(
                VerificationError::ObjectOutOfBounds, table_position, depth);
        }

        const std::size_t vtable_end =
            vtable_position + static_cast<std::size_t>(vtable_size);
        const std::size_t object_end =
            table_position + static_cast<std::size_t>(object_size);
        if (!(vtable_end <= table_position ||
              object_end <= vtable_position)) {
            return FailBool(
                VerificationError::VtableObjectOverlap,
                vtable_position,
                depth);
        }

        const std::uint16_t vtable_fields = static_cast<std::uint16_t>(
            (vtable_size - 4) / 2);
        for (std::size_t index = 0; index < table.field_count; ++index) {
            const FieldLayout& field = table.fields[index];
            if (field.type == FieldType::Union) {
                if (!VerifyUnionField(
                        table_position,
                        object_size,
                        vtable_position,
                        vtable_fields,
                        field,
                        depth)) {
                    return false;
                }
                continue;
            }

            FieldGeometry geometry;
            if (!PrepareField(field, &geometry)) {
                return false;
            }

            const std::uint16_t field_offset = FieldOffset(
                vtable_position, vtable_fields, field.slot);
            if (field_offset == 0) {
                if (field.required) {
                    return FailBool(
                        VerificationError::RequiredFieldMissing,
                        table_position,
                        depth);
                }
                continue;
            }
            if (field_offset < 4) {
                return FailBool(
                    VerificationError::FieldOutOfBounds,
                    table_position + field_offset,
                    depth);
            }
            if (field_offset > object_size ||
                geometry.size > object_size - field_offset) {
                return FailBool(
                    VerificationError::FieldOutOfBounds,
                    table_position + field_offset,
                    depth);
            }

            const std::size_t field_position =
                table_position + field_offset;
            if ((field_position & (geometry.alignment - 1)) != 0) {
                return FailBool(
                    VerificationError::FieldUnaligned,
                    field_position,
                    depth);
            }

            if (!VerifyFieldValue(
                    field_position, field, depth, field_position)) {
                return false;
            }
        }
        return true;
    }

    std::uint16_t FieldOffset(
        std::size_t vtable_position,
        std::uint16_t vtable_fields,
        std::uint16_t slot) const noexcept {
        if (slot >= vtable_fields) {
            return 0;
        }
        return ReadU16(
            data_ + vtable_position + 4 +
            static_cast<std::size_t>(slot) * 2);
    }

    bool VerifyFieldValue(
        std::size_t field_position,
        const FieldLayout& field,
        std::uint32_t depth,
        std::size_t error_offset) noexcept {
        switch (field.type) {
            case FieldType::String: {
                std::size_t target = 0;
                if (!ResolveOffset(
                        field_position, &target, error_offset, depth)) {
                    return false;
                }
                return VerifyString(target, depth);
            }
            case FieldType::Table: {
                std::size_t target = 0;
                if (!ResolveOffset(
                        field_position, &target, error_offset, depth)) {
                    return false;
                }
                return VerifyTable(target, *field.table, depth + 1);
            }
            case FieldType::Vector: {
                std::size_t target = 0;
                if (!ResolveOffset(
                        field_position, &target, error_offset, depth)) {
                    return false;
                }
                return VerifyVector(target, *field.vector, depth);
            }
            case FieldType::Union:
                return FailBool(
                    VerificationError::InvalidLayout,
                    error_offset,
                    depth);
            default:
                return true;
        }
    }

    bool ResolveOffset(
        std::size_t position,
        std::size_t* target,
        std::size_t error_offset,
        std::uint32_t depth) noexcept {
        if (!InRange(position, 4)) {
            return FailBool(
                VerificationError::OffsetOutOfBounds, error_offset, depth);
        }
        const std::uint32_t offset = ReadU32(data_ + position);
        if (offset < 4) {
            return FailBool(
                VerificationError::OffsetBackwards, error_offset, depth);
        }
        if (offset > size_ - position) {
            return FailBool(
                VerificationError::OffsetOutOfBounds, error_offset, depth);
        }
        const std::size_t resolved = position + offset;
        if ((resolved & 3u) != 0) {
            return FailBool(
                VerificationError::OffsetUnaligned, error_offset, depth);
        }
        *target = resolved;
        return true;
    }

    bool VerifyString(std::size_t string_position, std::uint32_t depth) noexcept {
        if (!InRange(string_position, 4)) {
            return FailBool(
                VerificationError::StringOutOfBounds, string_position, depth);
        }
        const std::uint32_t length = ReadU32(data_ + string_position);
        if (options_.max_string_length != 0 &&
            length > options_.max_string_length) {
            return FailBool(
                VerificationError::StringTooLong, string_position, depth);
        }

        const std::size_t data_position = string_position + 4;
        if (data_position >= size_) {
            return FailBool(
                VerificationError::StringOutOfBounds, string_position, depth);
        }
        const std::size_t available = size_ - data_position;
        if (static_cast<std::uint64_t>(length) >= available) {
            return FailBool(
                VerificationError::StringOutOfBounds, string_position, depth);
        }
        if (data_[data_position + length] != 0) {
            return FailBool(
                VerificationError::StringNotTerminated,
                data_position + length,
                depth);
        }
        return true;
    }

    bool VerifyVector(
        std::size_t vector_position,
        const VectorLayout& vector,
        std::uint32_t depth) noexcept {
        if (depth > max_depth_) {
            return FailBool(
                VerificationError::MaxDepthExceeded, vector_position, depth);
        }
        if (!InRange(vector_position, 4)) {
            return FailBool(
                VerificationError::VectorOutOfBounds, vector_position, depth);
        }
        if ((vector_position & 3u) != 0) {
            return FailBool(
                VerificationError::VectorUnaligned, vector_position, depth);
        }

        VectorGeometry geometry;
        if (!PrepareVector(vector, &geometry)) {
            return false;
        }

        const std::uint32_t length = ReadU32(data_ + vector_position);
        if (options_.max_vector_elements != 0 &&
            length > options_.max_vector_elements) {
            return FailBool(
                VerificationError::VectorTooLong, vector_position, depth);
        }

        const std::size_t data_position = vector_position + 4;
        if (geometry.element_size == 0 ||
            static_cast<std::uint64_t>(length) >
                std::numeric_limits<std::size_t>::max() /
                    geometry.element_size) {
            return FailBool(
                VerificationError::VectorSizeOverflow,
                vector_position,
                depth);
        }
        if (data_position > size_) {
            return FailBool(
                VerificationError::VectorOutOfBounds, vector_position, depth);
        }
        const std::size_t available = size_ - data_position;
        if (length > available / geometry.element_size) {
            return FailBool(
                VerificationError::VectorOutOfBounds,
                vector_position,
                depth);
        }
        if ((data_position & (geometry.alignment - 1)) != 0) {
            return FailBool(
                VerificationError::VectorUnaligned, data_position, depth);
        }

        if (vector.element_type != FieldType::Table &&
            vector.element_type != FieldType::String &&
            vector.element_type != FieldType::Vector) {
            return true;
        }

        for (std::uint32_t index = 0; index < length; ++index) {
            const std::size_t element_position =
                data_position +
                static_cast<std::size_t>(index) * geometry.element_size;
            std::size_t target = 0;
            if (!ResolveOffset(
                    element_position, &target, element_position, depth)) {
                return false;
            }

            switch (vector.element_type) {
                case FieldType::Table:
                    if (!VerifyTable(target, *vector.table, depth + 1)) {
                        return false;
                    }
                    break;
                case FieldType::String:
                    if (!VerifyString(target, depth)) {
                        return false;
                    }
                    break;
                case FieldType::Vector:
                    if (!VerifyVector(target, *vector.vector, depth + 1)) {
                        return false;
                    }
                    break;
                default:
                    break;
            }
        }
        return true;
    }

    bool VerifyUnionField(
        std::size_t table_position,
        std::uint16_t object_size,
        std::size_t vtable_position,
        std::uint16_t vtable_fields,
        const FieldLayout& field,
        std::uint32_t depth) noexcept {
        if (field.union_layout == nullptr ||
            field.union_layout->type_slot == field.union_layout->value_slot ||
            (field.union_layout->variant_count != 0 &&
             field.union_layout->variants == nullptr)) {
            return FailBool(
                VerificationError::InvalidLayout, table_position, depth);
        }
        const UnionLayout& union_layout = *field.union_layout;
        if (!IsUnsignedInteger(union_layout.type_type)) {
            return FailBool(
                VerificationError::UnionInvalidType, table_position, depth);
        }

        const std::uint32_t type_size = ScalarSize(union_layout.type_type);
        const std::uint16_t type_offset = FieldOffset(
            vtable_position, vtable_fields, union_layout.type_slot);
        const std::uint16_t value_offset = FieldOffset(
            vtable_position, vtable_fields, union_layout.value_slot);

        if (type_offset == 0) {
            if (value_offset != 0) {
                return FailBool(
                    VerificationError::UnionValueWithoutType,
                    table_position + value_offset,
                    depth);
            }
            if (field.required) {
                return FailBool(
                    VerificationError::RequiredFieldMissing,
                    table_position,
                    depth);
            }
            return true;
        }

        if (type_offset < 4 || type_offset > object_size ||
            type_size > object_size - type_offset) {
            return FailBool(
                VerificationError::FieldOutOfBounds,
                table_position + type_offset,
                depth);
        }
        const std::size_t type_position = table_position + type_offset;
        if ((type_position & (type_size - 1)) != 0) {
            return FailBool(
                VerificationError::FieldUnaligned, type_position, depth);
        }
        const std::uint64_t tag =
            ReadUnsigned(data_ + type_position, type_size);

        if (tag == 0) {
            if (value_offset != 0) {
                return FailBool(
                    VerificationError::UnionValueForNone,
                    table_position + value_offset,
                    depth);
            }
            return true;
        }

        if (value_offset == 0) {
            return FailBool(
                VerificationError::UnionTypeWithoutValue,
                table_position,
                depth);
        }
        if (value_offset < 4 || value_offset > object_size ||
            4 > object_size - value_offset) {
            return FailBool(
                VerificationError::FieldOutOfBounds,
                table_position + value_offset,
                depth);
        }
        const std::size_t value_position =
            table_position + value_offset;
        if ((value_position & 3u) != 0) {
            return FailBool(
                VerificationError::FieldUnaligned, value_position, depth);
        }

        const TableLayout* variant_table = nullptr;
        for (std::size_t index = 0; index < union_layout.variant_count;
             ++index) {
            if (union_layout.variants[index].tag == tag) {
                variant_table = union_layout.variants[index].table;
                break;
            }
        }
        if (variant_table == nullptr) {
            return FailBool(
                VerificationError::UnionUnknownType, value_position, depth);
        }

        std::size_t target = 0;
        if (!ResolveOffset(value_position, &target, value_position, depth)) {
            return false;
        }
        return VerifyTable(target, *variant_table, depth + 1);
    }

    const std::uint8_t* data_ = nullptr;
    std::size_t size_ = 0;
    BufferSchema schema_;
    VerifyOptions options_;
    std::uint32_t max_depth_ = 0;
    std::uint32_t table_count_ = 0;
    VerificationResult result_;
};

}  // namespace

const char* ToString(VerificationError error) noexcept {
    switch (error) {
        case VerificationError::None:
            return "ok";
        case VerificationError::NullBuffer:
            return "null buffer";
        case VerificationError::BufferTooSmall:
            return "buffer too small";
        case VerificationError::InvalidLayout:
            return "invalid table layout";
        case VerificationError::FileIdentifierMismatch:
            return "file identifier mismatch";
        case VerificationError::RootOffsetBackwards:
            return "root offset points backwards";
        case VerificationError::RootOffsetOutOfBounds:
            return "root offset out of bounds";
        case VerificationError::RootOffsetUnaligned:
            return "root offset unaligned";
        case VerificationError::TableOutOfBounds:
            return "table out of bounds";
        case VerificationError::TableUnaligned:
            return "table unaligned";
        case VerificationError::VtableSelfReference:
            return "vtable self reference";
        case VerificationError::VtableOutOfBounds:
            return "vtable out of bounds";
        case VerificationError::VtableUnaligned:
            return "vtable unaligned";
        case VerificationError::VtableTooSmall:
            return "vtable too small";
        case VerificationError::VtableSizeInvalid:
            return "vtable size invalid";
        case VerificationError::ObjectSizeInvalid:
            return "object size invalid";
        case VerificationError::ObjectOutOfBounds:
            return "object out of bounds";
        case VerificationError::VtableObjectOverlap:
            return "vtable overlaps object";
        case VerificationError::FieldOutOfBounds:
            return "field out of bounds";
        case VerificationError::FieldUnaligned:
            return "field unaligned";
        case VerificationError::RequiredFieldMissing:
            return "required field missing";
        case VerificationError::OffsetBackwards:
            return "offset points backwards";
        case VerificationError::OffsetOutOfBounds:
            return "offset out of bounds";
        case VerificationError::OffsetUnaligned:
            return "offset unaligned";
        case VerificationError::StringOutOfBounds:
            return "string out of bounds";
        case VerificationError::StringNotTerminated:
            return "string not NUL terminated";
        case VerificationError::StringTooLong:
            return "string too long";
        case VerificationError::VectorOutOfBounds:
            return "vector out of bounds";
        case VerificationError::VectorUnaligned:
            return "vector unaligned";
        case VerificationError::VectorSizeOverflow:
            return "vector size overflow";
        case VerificationError::VectorTooLong:
            return "vector too long";
        case VerificationError::UnionTypeWithoutValue:
            return "union type without value";
        case VerificationError::UnionValueWithoutType:
            return "union value without type";
        case VerificationError::UnionValueForNone:
            return "union value present for NONE";
        case VerificationError::UnionUnknownType:
            return "unknown union type";
        case VerificationError::UnionInvalidType:
            return "invalid union discriminator type";
        case VerificationError::MaxDepthExceeded:
            return "maximum nesting depth exceeded";
        case VerificationError::MaxTablesExceeded:
            return "maximum table count exceeded";
        case VerificationError::UnsupportedType:
            return "unsupported field type";
    }
    return "unknown verification error";
}

VerificationResult VerifyFlatBuffer(
    const std::uint8_t* data,
    std::size_t size,
    const BufferSchema& schema,
    const VerifyOptions& options) noexcept {
    VerifierImpl verifier(data, size, schema, options);
    return verifier.Run();
}

bool VerifyBuffer(
    const std::uint8_t* data,
    std::size_t size,
    const BufferSchema& schema,
    const VerifyOptions& options,
    VerificationResult* result) noexcept {
    const VerificationResult verification =
        VerifyFlatBuffer(data, size, schema, options);
    if (result != nullptr) {
        *result = verification;
    }
    return verification.ok;
}

bool VerifyBuffer(
    const std::uint8_t* data,
    std::size_t size,
    const BufferSchema& schema,
    VerificationResult* result) noexcept {
    return VerifyBuffer(data, size, schema, VerifyOptions(), result);
}

bool ValidateFlatBuffer(
    const std::uint8_t* data,
    std::size_t size,
    const BufferSchema& schema,
    const VerifyOptions& options,
    VerificationResult* result) noexcept {
    return VerifyBuffer(data, size, schema, options, result);
}

bool ValidateBuffer(
    const std::uint8_t* data,
    std::size_t size,
    const BufferSchema& schema,
    const VerifyOptions& options,
    VerificationResult* result) noexcept {
    return VerifyBuffer(data, size, schema, options, result);
}

BufferVerifier::BufferVerifier(
    const BufferSchema& schema,
    const VerifyOptions& options) noexcept
    : schema_(schema), options_(options) {}

VerificationResult BufferVerifier::Verify(
    const std::uint8_t* data,
    std::size_t size) const noexcept {
    return VerifyFlatBuffer(data, size, schema_, options_);
}

}  // namespace netsec
