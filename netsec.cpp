#include "netsec.hpp"

#include <algorithm>
#include <cstring>
#include <limits>
#include <vector>

namespace netsec {
namespace {

constexpr std::size_t kNoIndex = static_cast<std::size_t>(-1);

inline bool InRange(std::size_t offset, std::size_t length, std::size_t size) noexcept {
    return offset <= size && length <= size - offset;
}

inline std::uint16_t LoadU16(const std::uint8_t* data) noexcept {
    return static_cast<std::uint16_t>(data[0]) |
           static_cast<std::uint16_t>(static_cast<std::uint16_t>(data[1]) << 8);
}

inline std::uint32_t LoadU32(const std::uint8_t* data) noexcept {
    return static_cast<std::uint32_t>(data[0]) |
           (static_cast<std::uint32_t>(data[1]) << 8) |
           (static_cast<std::uint32_t>(data[2]) << 16) |
           (static_cast<std::uint32_t>(data[3]) << 24);
}

inline std::int32_t LoadI32(const std::uint8_t* data) noexcept {
    const std::uint32_t raw = LoadU32(data);
    std::int32_t value = 0;
    std::memcpy(&value, &raw, sizeof(value));
    return value;
}

inline bool IsPowerOfTwo(std::size_t value) noexcept {
    return value != 0 && (value & (value - 1)) == 0;
}

inline bool IsScalar(FieldType type) noexcept {
    switch (type) {
        case FieldType::Bool:
        case FieldType::Int8:
        case FieldType::UInt8:
        case FieldType::Int16:
        case FieldType::UInt16:
        case FieldType::Int32:
        case FieldType::UInt32:
        case FieldType::Int64:
        case FieldType::UInt64:
        case FieldType::Float32:
        case FieldType::Float64:
            return true;
        default:
            return false;
    }
}

inline std::size_t ScalarSize(FieldType type) noexcept {
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

}  // namespace

const char* ValidationErrorName(ValidationError error) noexcept {
    switch (error) {
        case ValidationError::None:
            return "ok";
        case ValidationError::InvalidArgument:
            return "invalid argument";
        case ValidationError::InvalidLayout:
            return "invalid layout";
        case ValidationError::BufferTooSmall:
            return "buffer too small";
        case ValidationError::RootOutOfBounds:
            return "root offset out of bounds";
        case ValidationError::RootMisaligned:
            return "root offset misaligned";
        case ValidationError::RootOverlapsHeader:
            return "root table overlaps header";
        case ValidationError::FileIdentifierMismatch:
            return "file identifier mismatch";
        case ValidationError::VtableOffsetInvalid:
            return "invalid vtable offset";
        case ValidationError::SelfReferentialVtable:
            return "self-referential vtable";
        case ValidationError::VtableOutOfBounds:
            return "vtable out of bounds";
        case ValidationError::VtableMisaligned:
            return "vtable misaligned";
        case ValidationError::VtableSizeInvalid:
            return "invalid vtable size";
        case ValidationError::VtableOverlapsTable:
            return "vtable overlaps table";
        case ValidationError::ObjectOutOfBounds:
            return "table object out of bounds";
        case ValidationError::ObjectTooSmall:
            return "table object too small";
        case ValidationError::FieldOutOfBounds:
            return "field out of bounds";
        case ValidationError::FieldMisaligned:
            return "field misaligned";
        case ValidationError::RequiredFieldMissing:
            return "required field missing";
        case ValidationError::UnionValueMissing:
            return "union value missing";
        case ValidationError::UnionValueUnexpected:
            return "union value present for NONE type";
        case ValidationError::UnionTypeUnknown:
            return "unknown union type";
        case ValidationError::OffsetInvalid:
            return "invalid forward offset";
        case ValidationError::StringOutOfBounds:
            return "string out of bounds";
        case ValidationError::StringTooLong:
            return "string too long";
        case ValidationError::StringNotTerminated:
            return "string not NUL-terminated";
        case ValidationError::VectorOutOfBounds:
            return "vector out of bounds";
        case ValidationError::VectorTooLarge:
            return "vector too large";
        case ValidationError::LengthOverflow:
            return "vector length multiplication overflow";
        case ValidationError::StructOutOfBounds:
            return "struct out of bounds";
        case ValidationError::StructMisaligned:
            return "struct misaligned";
        case ValidationError::StructFieldOutOfBounds:
            return "struct field out of bounds";
        case ValidationError::DepthLimitExceeded:
            return "nesting depth limit exceeded";
        case ValidationError::TableLimitExceeded:
            return "table count limit exceeded";
        case ValidationError::TableCycle:
            return "table cycle";
        case ValidationError::ElementLimitExceeded:
            return "vector element limit exceeded";
        case ValidationError::OutOfMemory:
            return "validation state allocation failed";
    }
    return "unknown validation error";
}

namespace {

class Validator {
public:
    Validator(
        const std::uint8_t* data,
        std::size_t size,
        const RootLayout& root,
        const ValidationLimits& limits)
        : data_(data), size_(size), root_(root), limits_(limits) {
        visits_.reserve(std::min<std::size_t>(limits_.max_tables, 1024));
    }

    ValidationResult Run() {
        result_.message = ValidationErrorName(ValidationError::InvalidArgument);
        if (data_ == nullptr) {
            Fail(ValidationError::InvalidArgument, 0);
            return result_;
        }
        if (root_.root_table == nullptr) {
            Fail(ValidationError::InvalidLayout, 0);
            return result_;
        }
        if (size_ < 4) {
            Fail(ValidationError::BufferTooSmall, 0);
            return result_;
        }

        const std::size_t header_size = root_.file_identifier == nullptr ? 4 : 8;
        if (size_ < header_size) {
            Fail(ValidationError::BufferTooSmall, 0);
            return result_;
        }
        if (root_.file_identifier != nullptr &&
            std::memcmp(data_ + 4, root_.file_identifier, 4) != 0) {
            Fail(ValidationError::FileIdentifierMismatch, 4);
            return result_;
        }

        const std::uint32_t root_offset = LoadU32(data_);
        if (root_offset > size_ || root_offset < 4) {
            Fail(ValidationError::RootOutOfBounds, 0);
            return result_;
        }
        if (root_offset % 4 != 0) {
            Fail(ValidationError::RootMisaligned, root_offset);
            return result_;
        }
        if (root_offset < header_size + 4) {
            Fail(ValidationError::RootOverlapsHeader, root_offset);
            return result_;
        }

        if (!ValidateTable(root_offset, *root_.root_table, 1)) {
            return result_;
        }

        result_.valid = true;
        result_.error = ValidationError::None;
        result_.offset = 0;
        result_.message = ValidationErrorName(ValidationError::None);
        SynchronizeCounters();
        return result_;
    }

private:
    struct TableVisit {
        std::size_t offset;
        const TableLayout* layout;
        bool active;
    };

    struct ActiveVisit {
        explicit ActiveVisit(std::vector<TableVisit>& visits) : visits_(visits) {}
        ~ActiveVisit() {
            if (!visits_.empty()) {
                visits_.back().active = false;
            }
        }
        std::vector<TableVisit>& visits_;
    };

    enum class EnterResult {
        New,
        AlreadyValidated,
        Rejected,
    };

    bool Fail(
        ValidationError error,
        std::size_t offset,
        std::size_t table_offset = kNoIndex,
        std::size_t field_id = kNoIndex) {
        if (failed_) {
            return false;
        }
        failed_ = true;
        result_.valid = false;
        result_.error = error;
        result_.offset = offset;
        result_.message = ValidationErrorName(error);
        if (table_offset != kNoIndex) {
            result_.table_offset = table_offset;
        }
        if (field_id != kNoIndex) {
            result_.field_id = field_id;
        }
        SynchronizeCounters();
        return false;
    }

    void SynchronizeCounters() {
        result_.table_count = unique_tables_;
        result_.max_depth_seen = max_depth_seen_;
    }

    void ObserveDepth(std::size_t depth) {
        max_depth_seen_ = std::max(max_depth_seen_, depth);
    }

    bool ResolveType(const TypeLayout& type, std::size_t& size, std::size_t& alignment) const {
        if (IsScalar(type.type)) {
            size = ScalarSize(type.type);
            alignment = size;
            return size != 0;
        }

        switch (type.type) {
            case FieldType::String:
                size = 4;
                alignment = 4;
                return true;
            case FieldType::Table:
                size = 4;
                alignment = 4;
                return type.table_layout != nullptr;
            case FieldType::Vector:
                size = 4;
                alignment = 4;
                return type.vector_layout != nullptr;
            case FieldType::Union:
                size = 4;
                alignment = 4;
                return type.union_layout != nullptr;
            case FieldType::Struct:
                if (type.struct_layout == nullptr) {
                    return false;
                }
                size = type.struct_layout->size;
                alignment = type.struct_layout->alignment;
                return size != 0 && IsPowerOfTwo(alignment) && size % alignment == 0;
            default:
                return false;
        }
    }

    EnterResult EnterTable(
        std::size_t offset,
        const TableLayout& layout,
        std::size_t depth) {
        if (depth > limits_.max_depth) {
            Fail(ValidationError::DepthLimitExceeded, offset, offset);
            return EnterResult::Rejected;
        }
        ObserveDepth(depth);

        bool seen_offset = false;
        for (const TableVisit& visit : visits_) {
            if (visit.offset != offset) {
                continue;
            }
            seen_offset = true;
            if (visit.active) {
                Fail(ValidationError::TableCycle, offset, offset);
                return EnterResult::Rejected;
            }
            if (visit.layout == &layout) {
                return EnterResult::AlreadyValidated;
            }
        }

        if (!seen_offset) {
            if (unique_tables_ >= limits_.max_tables) {
                Fail(ValidationError::TableLimitExceeded, offset, offset);
                return EnterResult::Rejected;
            }
            ++unique_tables_;
        }

        visits_.push_back(TableVisit{offset, &layout, true});
        return EnterResult::New;
    }

    std::uint16_t LookupSlot(
        const std::uint8_t* vtable,
        std::size_t vtable_size,
        std::uint16_t field_id) const {
        const std::size_t entry = 4 + static_cast<std::size_t>(field_id) * 2;
        if (entry > vtable_size || vtable_size - entry < 2) {
            return 0;
        }
        return LoadU16(vtable + entry);
    }

    bool ResolveSlot(
        std::size_t table_offset,
        std::size_t object_size,
        std::uint16_t field_offset,
        std::size_t field_size,
        std::size_t alignment,
        std::size_t& target,
        std::size_t field_id) {
        if (field_offset < 4 || field_offset >= object_size) {
            return Fail(
                ValidationError::FieldOutOfBounds,
                table_offset + field_offset,
                table_offset,
                field_id);
        }
        if (field_size > object_size - field_offset) {
            return Fail(
                ValidationError::FieldOutOfBounds,
                table_offset + field_offset,
                table_offset,
                field_id);
        }

        target = table_offset + field_offset;
        if (target % alignment != 0) {
            return Fail(
                ValidationError::FieldMisaligned,
                target,
                table_offset,
                field_id);
        }
        return true;
    }

    bool ReadOffsetTarget(std::size_t source, std::size_t& target) {
        if (!InRange(source, 4, size_)) {
            return Fail(ValidationError::OffsetInvalid, source);
        }
        const std::uint32_t offset = LoadU32(data_ + source);
        if (offset < 4) {
            return Fail(ValidationError::OffsetInvalid, source);
        }
        if (offset > size_ - source) {
            return Fail(ValidationError::OffsetInvalid, source);
        }
        target = source + offset;
        return true;
    }

    bool ValidateValue(
        std::size_t target,
        const TypeLayout& type,
        std::size_t depth) {
        if (IsScalar(type.type)) {
            const std::size_t size = ScalarSize(type.type);
            if (!InRange(target, size, size_)) {
                return Fail(ValidationError::FieldOutOfBounds, target);
            }
            if (target % size != 0) {
                return Fail(ValidationError::FieldMisaligned, target);
            }
            return true;
        }

        switch (type.type) {
            case FieldType::String: {
                std::size_t string_offset = 0;
                if (!ReadOffsetTarget(target, string_offset)) {
                    return false;
                }
                return ValidateString(string_offset, depth);
            }
            case FieldType::Table: {
                if (type.table_layout == nullptr) {
                    return Fail(ValidationError::InvalidLayout, target);
                }
                std::size_t table_offset = 0;
                if (!ReadOffsetTarget(target, table_offset)) {
                    return false;
                }
                return ValidateTable(table_offset, *type.table_layout, depth);
            }
            case FieldType::Struct:
                if (type.struct_layout == nullptr) {
                    return Fail(ValidationError::InvalidLayout, target);
                }
                return ValidateStruct(target, *type.struct_layout, depth);
            case FieldType::Vector: {
                if (type.vector_layout == nullptr) {
                    return Fail(ValidationError::InvalidLayout, target);
                }
                std::size_t vector_offset = 0;
                if (!ReadOffsetTarget(target, vector_offset)) {
                    return false;
                }
                return ValidateVector(vector_offset, *type.vector_layout, depth);
            }
            case FieldType::Union:
            case FieldType::Invalid:
                return Fail(ValidationError::InvalidLayout, target);
            default:
                return Fail(ValidationError::InvalidLayout, target);
        }
    }

    bool ValidateString(std::size_t offset, std::size_t depth) {
        if (depth > limits_.max_depth) {
            return Fail(ValidationError::DepthLimitExceeded, offset);
        }
        ObserveDepth(depth);

        if (offset % 4 != 0) {
            return Fail(ValidationError::FieldMisaligned, offset);
        }
        if (!InRange(offset, 4, size_)) {
            return Fail(ValidationError::StringOutOfBounds, offset);
        }
        const std::uint32_t length = LoadU32(data_ + offset);
        if (length > limits_.max_string_bytes) {
            return Fail(ValidationError::StringTooLong, offset);
        }

        const std::size_t data_offset = offset + 4;
        if (length >= size_ - data_offset) {
            return Fail(ValidationError::StringOutOfBounds, data_offset);
        }
        if (data_[data_offset + length] != 0) {
            return Fail(ValidationError::StringNotTerminated, data_offset + length);
        }
        return true;
    }

    bool ValidateVector(
        std::size_t offset,
        const VectorLayout& layout,
        std::size_t depth) {
        if (depth > limits_.max_depth) {
            return Fail(ValidationError::DepthLimitExceeded, offset);
        }
        ObserveDepth(depth);

        if (offset % 4 != 0) {
            return Fail(ValidationError::FieldMisaligned, offset);
        }
        if (!InRange(offset, 4, size_)) {
            return Fail(ValidationError::VectorOutOfBounds, offset);
        }
        const std::uint32_t count = LoadU32(data_ + offset);

        std::size_t element_size = 0;
        std::size_t element_alignment = 0;
        if (!ResolveType(layout.element, element_size, element_alignment) ||
            layout.element.type == FieldType::Union) {
            return Fail(ValidationError::InvalidLayout, offset);
        }
        if (element_size == 0) {
            return Fail(ValidationError::InvalidLayout, offset);
        }
        if (count > std::numeric_limits<std::size_t>::max() / element_size) {
            return Fail(ValidationError::LengthOverflow, offset);
        }
        const std::size_t bytes = static_cast<std::size_t>(count) * element_size;
        const std::size_t data_offset = offset + 4;
        if (bytes > size_ - data_offset) {
            return Fail(ValidationError::VectorOutOfBounds, data_offset);
        }
        if (count > limits_.max_vector_elements) {
            return Fail(ValidationError::VectorTooLarge, offset);
        }
        if (total_elements_ > limits_.max_total_elements ||
            count > limits_.max_total_elements - total_elements_) {
            return Fail(ValidationError::ElementLimitExceeded, offset);
        }
        total_elements_ += count;

        if (count != 0 && data_offset % element_alignment != 0) {
            return Fail(ValidationError::FieldMisaligned, data_offset);
        }
        if (IsScalar(layout.element.type)) {
            return true;
        }

        for (std::size_t i = 0; i < count; ++i) {
            const std::size_t element_offset = data_offset + i * element_size;
            if (layout.element.type == FieldType::Struct) {
                if (!ValidateStruct(element_offset, *layout.element.struct_layout, depth + 1)) {
                    return false;
                }
                continue;
            }
            if (!ValidateValue(element_offset, layout.element, depth + 1)) {
                return false;
            }
        }
        return true;
    }

    bool ValidateStruct(
        std::size_t offset,
        const StructLayout& layout,
        std::size_t depth) {
        if (depth > limits_.max_depth) {
            return Fail(ValidationError::DepthLimitExceeded, offset);
        }
        ObserveDepth(depth);

        if (layout.size == 0 || !IsPowerOfTwo(layout.alignment) ||
            layout.size % layout.alignment != 0) {
            return Fail(ValidationError::InvalidLayout, offset);
        }
        if (offset % layout.alignment != 0) {
            return Fail(ValidationError::StructMisaligned, offset);
        }
        if (!InRange(offset, layout.size, size_)) {
            return Fail(ValidationError::StructOutOfBounds, offset);
        }
        if (layout.field_count != 0 && layout.fields == nullptr) {
            return Fail(ValidationError::InvalidLayout, offset);
        }

        for (std::size_t i = 0; i < layout.field_count; ++i) {
            const StructField& field = layout.fields[i];
            if (field.offset >= layout.size) {
                return Fail(ValidationError::StructFieldOutOfBounds, offset);
            }

            std::size_t field_size = 0;
            std::size_t field_alignment = 0;
            if (!ResolveType(field.type, field_size, field_alignment) ||
                (!IsScalar(field.type.type) && field.type.type != FieldType::Struct)) {
                return Fail(ValidationError::InvalidLayout, offset);
            }
            if (field_size > layout.size - field.offset) {
                return Fail(ValidationError::StructFieldOutOfBounds, offset);
            }

            const std::size_t field_offset = offset + field.offset;
            if (field_offset % field_alignment != 0) {
                return Fail(ValidationError::StructFieldOutOfBounds, field_offset);
            }
            if (field.type.type == FieldType::Struct &&
                !ValidateStruct(field_offset, *field.type.struct_layout, depth + 1)) {
                return false;
            }
        }
        return true;
    }

    bool ValidateUnionField(
        std::size_t table_offset,
        const std::uint8_t* vtable,
        std::size_t vtable_size,
        std::size_t object_size,
        const FieldLayout& field,
        std::size_t depth) {
        if (field.type.type != FieldType::Union || field.type.union_layout == nullptr) {
            return Fail(
                ValidationError::InvalidLayout,
                table_offset,
                table_offset,
                field.id);
        }
        const UnionLayout& layout = *field.type.union_layout;
        if (layout.type_field_id == field.id ||
            (layout.case_count != 0 && layout.cases == nullptr)) {
            return Fail(
                ValidationError::InvalidLayout,
                table_offset,
                table_offset,
                field.id);
        }

        std::uint8_t tag = 0;
        const std::uint16_t type_entry =
            LookupSlot(vtable, vtable_size, layout.type_field_id);
        if (type_entry != 0) {
            std::size_t type_target = 0;
            if (!ResolveSlot(
                    table_offset,
                    object_size,
                    type_entry,
                    1,
                    1,
                    type_target,
                    layout.type_field_id)) {
                return false;
            }
            tag = data_[type_target];
        }

        const std::uint16_t value_entry =
            LookupSlot(vtable, vtable_size, field.id);
        if (value_entry == 0) {
            if (field.required) {
                return Fail(
                    ValidationError::RequiredFieldMissing,
                    table_offset,
                    table_offset,
                    field.id);
            }
            if (tag != 0) {
                return Fail(
                    ValidationError::UnionValueMissing,
                    table_offset,
                    table_offset,
                    field.id);
            }
            return true;
        }

        std::size_t value_target = 0;
        if (!ResolveSlot(
                table_offset,
                object_size,
                value_entry,
                4,
                4,
                value_target,
                field.id)) {
            return false;
        }
        if (tag == 0) {
            return Fail(
                ValidationError::UnionValueUnexpected,
                value_target,
                table_offset,
                field.id);
        }
        if (LoadU32(data_ + value_target) == 0) {
            return Fail(
                ValidationError::UnionValueMissing,
                value_target,
                table_offset,
                field.id);
        }

        const UnionCase* matched = nullptr;
        for (std::size_t i = 0; i < layout.case_count; ++i) {
            if (layout.cases[i].type == tag) {
                matched = &layout.cases[i];
                break;
            }
        }
        if (matched == nullptr) {
            return Fail(
                ValidationError::UnionTypeUnknown,
                value_target,
                table_offset,
                field.id);
        }
        if (matched->type == 0 || IsScalar(matched->value.type) ||
            matched->value.type == FieldType::Union ||
            matched->value.type == FieldType::Invalid) {
            return Fail(
                ValidationError::InvalidLayout,
                value_target,
                table_offset,
                field.id);
        }

        std::size_t value_size = 0;
        std::size_t value_alignment = 0;
        if (!ResolveType(matched->value, value_size, value_alignment)) {
            return Fail(
                ValidationError::InvalidLayout,
                value_target,
                table_offset,
                field.id);
        }
        std::size_t object_target = 0;
        if (!ReadOffsetTarget(value_target, object_target)) {
            return false;
        }
        switch (matched->value.type) {
            case FieldType::Table:
                return ValidateTable(
                    object_target,
                    *matched->value.table_layout,
                    depth + 1);
            case FieldType::String:
                return ValidateString(object_target, depth + 1);
            case FieldType::Struct:
                return ValidateStruct(
                    object_target,
                    *matched->value.struct_layout,
                    depth + 1);
            case FieldType::Vector:
                return ValidateVector(
                    object_target,
                    *matched->value.vector_layout,
                    depth + 1);
            default:
                return Fail(
                    ValidationError::InvalidLayout,
                    value_target,
                    table_offset,
                    field.id);
        }
    }

    bool ValidateTable(
        std::size_t offset,
        const TableLayout& layout,
        std::size_t depth) {
        const EnterResult entered = EnterTable(offset, layout, depth);
        if (entered == EnterResult::Rejected) {
            return false;
        }
        if (entered == EnterResult::AlreadyValidated) {
            return true;
        }
        ActiveVisit active(visits_);

        if (offset % 4 != 0) {
            return Fail(ValidationError::ObjectOutOfBounds, offset, offset);
        }
        if (!InRange(offset, 4, size_)) {
            return Fail(ValidationError::ObjectOutOfBounds, offset, offset);
        }

        const std::int32_t vtable_distance = LoadI32(data_ + offset);
        if (vtable_distance == 0) {
            return Fail(ValidationError::SelfReferentialVtable, offset, offset);
        }
        if (vtable_distance < 0 ||
            static_cast<std::size_t>(vtable_distance) > offset) {
            return Fail(ValidationError::VtableOffsetInvalid, offset, offset);
        }

        const std::size_t vtable_offset =
            offset - static_cast<std::size_t>(vtable_distance);
        if (vtable_offset % 2 != 0) {
            return Fail(ValidationError::VtableMisaligned, vtable_offset, offset);
        }
        if (!InRange(vtable_offset, 4, size_)) {
            return Fail(ValidationError::VtableOutOfBounds, vtable_offset, offset);
        }

        const std::uint16_t vtable_size = LoadU16(data_ + vtable_offset);
        const std::uint16_t object_size = LoadU16(data_ + vtable_offset + 2);
        if (vtable_size < 4 || (vtable_size & 1u) != 0) {
            return Fail(ValidationError::VtableSizeInvalid, vtable_offset, offset);
        }
        if (vtable_size > limits_.max_vtable_bytes) {
            return Fail(ValidationError::VtableSizeInvalid, vtable_offset, offset);
        }
        if (!InRange(vtable_offset, vtable_size, size_)) {
            return Fail(ValidationError::VtableOutOfBounds, vtable_offset, offset);
        }
        if (vtable_size > offset - vtable_offset) {
            return Fail(ValidationError::VtableOverlapsTable, vtable_offset, offset);
        }
        if (object_size < 4) {
            return Fail(ValidationError::ObjectTooSmall, offset, offset);
        }
        if (!InRange(offset, object_size, size_)) {
            return Fail(ValidationError::ObjectOutOfBounds, offset, offset);
        }
        if (layout.field_count != 0 && layout.fields == nullptr) {
            return Fail(ValidationError::InvalidLayout, offset, offset);
        }

        for (std::size_t i = 0; i < layout.field_count; ++i) {
            const FieldLayout& field = layout.fields[i];
            if (field.type.type == FieldType::Union) {
                if (!ValidateUnionField(
                        offset,
                        data_ + vtable_offset,
                        vtable_size,
                        object_size,
                        field,
                        depth)) {
                    return false;
                }
                continue;
            }

            const std::uint16_t field_offset =
                LookupSlot(data_ + vtable_offset, vtable_size, field.id);
            if (field_offset == 0) {
                if (field.required) {
                    return Fail(
                        ValidationError::RequiredFieldMissing,
                        offset,
                        offset,
                        field.id);
                }
                continue;
            }

            std::size_t field_size = 0;
            std::size_t field_alignment = 0;
            if (!ResolveType(field.type, field_size, field_alignment)) {
                return Fail(
                    ValidationError::InvalidLayout,
                    offset,
                    offset,
                    field.id);
            }

            std::size_t field_target = 0;
            if (!ResolveSlot(
                    offset,
                    object_size,
                    field_offset,
                    field_size,
                    field_alignment,
                    field_target,
                    field.id)) {
                return false;
            }
            if (!ValidateValue(field_target, field.type, depth + 1)) {
                return false;
            }
        }
        return true;
    }

    const std::uint8_t* data_;
    std::size_t size_;
    const RootLayout& root_;
    const ValidationLimits& limits_;
    ValidationResult result_;
    std::vector<TableVisit> visits_;
    std::size_t unique_tables_ = 0;
    std::size_t total_elements_ = 0;
    std::size_t max_depth_seen_ = 0;
    bool failed_ = false;
};

}  // namespace

ValidationResult ValidateFlatBuffer(
    const std::uint8_t* data,
    std::size_t size,
    const RootLayout& root,
    const ValidationLimits& limits) noexcept {
    ValidationResult result;
    try {
        Validator validator(data, size, root, limits);
        result = validator.Run();
    } catch (...) {
        result.valid = false;
        result.error = ValidationError::OutOfMemory;
        result.offset = 0;
        result.message = ValidationErrorName(ValidationError::OutOfMemory);
    }
    return result;
}

}  // namespace netsec
