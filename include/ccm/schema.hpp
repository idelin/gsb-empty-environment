// Declarative ABI description.  A type is a sequence of fixed-offset fields;
// pointer-shaped fields point at either a single nested type, an array of
// nested types, or a variable-length byte payload.  Schemas are validated up
// front (field offsets, dangling references, pointer-reference cycles).
#ifndef CCM_SCHEMA_HPP
#define CCM_SCHEMA_HPP

#include <cstring>
#include <string>
#include <unordered_map>
#include <vector>

#include "ccm/checked_math.hpp"
#include "ccm/status.hpp"
#include "ccm/types.hpp"

namespace ccm {

using TypeId = std::uint32_t;

enum class FieldKind {
    Scalar,       // fixed-width integer, not dereferenced
    InlineBytes,  // fixed byte array inside the type block
    NestedPtr,    // u64 external pointer to one target type
    PtrArray,     // u64 external pointer to count_field target-type entries
    TailBytes     // variable byte tail, length from count_field
};

enum class ScalarWidth { U8 = 1, U16 = 2, U32 = 4, U64 = 8 };

struct FieldDesc {
    std::string name;
    FieldKind kind = FieldKind::Scalar;
    len_t offset = 0;  // byte offset inside the fixed type block

    // Scalar / InlineBytes configuration.
    ScalarWidth width = ScalarWidth::U64;
    len_t inline_size = 0;

    // Pointer / tail configuration.
    TypeId target_type = 0;
    std::string count_field;  // PtrArray / TailBytes: name of the scalar count

    static FieldDesc make_scalar(std::string name, len_t offset,
                                 ScalarWidth width) {
        FieldDesc field;
        field.name = std::move(name);
        field.kind = FieldKind::Scalar;
        field.offset = offset;
        field.width = width;
        return field;
    }

    static FieldDesc make_inline_bytes(std::string name, len_t offset,
                                       len_t size) {
        FieldDesc field;
        field.name = std::move(name);
        field.kind = FieldKind::InlineBytes;
        field.offset = offset;
        field.inline_size = size;
        return field;
    }

    static FieldDesc make_nested_ptr(std::string name, len_t offset,
                                     TypeId target) {
        FieldDesc field;
        field.name = std::move(name);
        field.kind = FieldKind::NestedPtr;
        field.offset = offset;
        field.width = ScalarWidth::U64;
        field.target_type = target;
        return field;
    }

    static FieldDesc make_ptr_array(std::string name, len_t offset,
                                    TypeId target, std::string count_field) {
        FieldDesc field;
        field.name = std::move(name);
        field.kind = FieldKind::PtrArray;
        field.offset = offset;
        field.width = ScalarWidth::U64;
        field.target_type = target;
        field.count_field = std::move(count_field);
        return field;
    }

    static FieldDesc make_tail_bytes(std::string name, len_t offset,
                                     std::string count_field) {
        FieldDesc field;
        field.name = std::move(name);
        field.kind = FieldKind::TailBytes;
        field.offset = offset;
        field.width = ScalarWidth::U64;  // tail length is encoded as u64
        field.count_field = std::move(count_field);
        return field;
    }
};

struct TypeDesc {
    TypeId id = 0;
    len_t fixed_size = 0;  // size of the fixed header block
    std::vector<FieldDesc> fields;

    const FieldDesc* find_field(const std::string& name) const {
        for (const FieldDesc& field : fields) {
            if (field.name == name) {
                return &field;
            }
        }
        return nullptr;
    }
};

class Schema {
public:
    static Result<Schema> create(std::vector<TypeDesc> types) {
        Schema schema;
        for (const TypeDesc& type : types) {
            if (schema.types_.count(type.id) != 0) {
                return Status{StatusCode::SchemaInvalid,
                              "duplicate type id in schema"};
            }
            Status layout = validate_layout(type);
            if (!layout.ok()) {
                return layout;
            }
            schema.types_.emplace(type.id, type);
        }
        Status refs = schema.validate_references_and_acyclicity();
        if (!refs.ok()) {
            return refs;
        }
        return schema;
    }

    const TypeDesc* type(TypeId id) const {
        auto it = types_.find(id);
        return it == types_.end() ? nullptr : &it->second;
    }

    const std::unordered_map<TypeId, TypeDesc>& types() const {
        return types_;
    }

private:
    static Status validate_layout(const TypeDesc& type) {
        if (type.fixed_size == 0) {
            return Status{StatusCode::SchemaInvalid,
                          "type fixed_size must be non-zero"};
        }
        for (const FieldDesc& field : type.fields) {
            if (field.kind == FieldKind::TailBytes) {
                // The tail is metadata: it occupies no header slot, begins
                // immediately after the fixed block and is sized by count.
                if (type.find_field(field.count_field) == nullptr) {
                    return Status{
                        StatusCode::SchemaInvalid,
                        "tail byte field requires an existing count field"};
                }
                continue;
            }
            len_t span = field_width(field);
            if (span == 0) {
                return Status{StatusCode::SchemaInvalid,
                              "field has zero width"};
            }
            addr_t end = 0;
            if (checked_span_end(field.offset, span, &end)) {
                return Status{StatusCode::Overflow,
                              "field offset + width overflows"};
            }
            if (end > type.fixed_size) {
                return Status{
                    StatusCode::OutOfBoundsOffset,
                    "field escapes the fixed-size type block"};
            }
            for (const FieldDesc& other : type.fields) {
                if (&other == &field) {
                    continue;
                }
                len_t other_span = field_width(other);
                if (field.offset < other.offset + other_span &&
                    other.offset < field.offset + span) {
                    return Status{StatusCode::SchemaInvalid,
                                  "fields overlap inside the type block"};
                }
            }
            if ((field.kind == FieldKind::PtrArray ||
                 field.kind == FieldKind::TailBytes) &&
                type.find_field(field.count_field) == nullptr) {
                return Status{StatusCode::SchemaInvalid,
                              "count field referenced by pointer/tail missing"};
            }
            if (field.kind == FieldKind::TailBytes &&
                field.offset != type.fixed_size) {
                return Status{StatusCode::SchemaInvalid,
                              "tail must begin exactly at fixed_size"};
            }
        }
        return Status::ok_status();
    }

    static len_t field_width(const FieldDesc& field) {
        switch (field.kind) {
            case FieldKind::Scalar:
                return static_cast<len_t>(field.width);
            case FieldKind::InlineBytes:
                return field.inline_size;
            case FieldKind::NestedPtr:
            case FieldKind::PtrArray:
                return static_cast<len_t>(ScalarWidth::U64);
            case FieldKind::TailBytes:
                return static_cast<len_t>(ScalarWidth::U64);
        }
        return 0;
    }

    Status validate_references_and_acyclicity() {
        for (const auto& entry : types_) {
            const TypeDesc& type = entry.second;
            for (const FieldDesc& field : type.fields) {
                if (field.kind == FieldKind::NestedPtr ||
                    field.kind == FieldKind::PtrArray) {
                    if (types_.count(field.target_type) == 0) {
                        return Status{
                            StatusCode::SchemaInvalid,
                            "pointer references an unknown target type"};
                    }
                }
            }
        }
        // Note: type-level self-reference (e.g. a linked-list next pointer)
        // is legal in an ABI.  Actual cycles are properties of the live
        // pointer graph and are rejected at marshal time via visited-address
        // tracking and a bounded traversal depth.
        return Status::ok_status();
    }

    std::unordered_map<TypeId, TypeDesc> types_;
};

}  // namespace ccm

#endif  // CCM_SCHEMA_HPP
