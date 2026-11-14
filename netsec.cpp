#include "netsec.hpp"

#include <algorithm>
#include <array>
#include <cstdint>
#include <sstream>
#include <unordered_set>
#include <utility>

namespace netsec {
namespace {

constexpr std::array<char, 72> kSwiftXChars = {
    'a', 'b', 'c', 'd', 'e', 'f', 'g', 'h', 'i', 'j', 'k', 'l', 'm',
    'n', 'o', 'p', 'q', 'r', 's', 't', 'u', 'v', 'w', 'x', 'y', 'z',
    'A', 'B', 'C', 'D', 'E', 'F', 'G', 'H', 'I', 'J', 'K', 'L', 'M',
    'N', 'O', 'P', 'Q', 'R', 'S', 'T', 'U', 'V', 'W', 'X', 'Y', 'Z',
    '0', '1', '2', '3', '4', '5', '6', '7', '8', '9',
    '/', '-', '?', '(', ')', '.', ',', '\'', '+', ' ',
};

ValidationError make_error(
    ErrorCode code,
    std::size_t field_index,
    std::size_t byte_offset,
    std::size_t char_offset,
    std::string message) {
    return ValidationError{code, field_index, byte_offset, char_offset, std::move(message)};
}

bool is_ascii_digit(char32_t cp) noexcept {
    return cp >= U'0' && cp <= U'9';
}

bool is_ascii_alpha(char32_t cp) noexcept {
    return (cp >= U'a' && cp <= U'z') || (cp >= U'A' && cp <= U'Z');
}

bool is_reference_char(char32_t cp) noexcept {
    return is_ascii_alpha(cp) || is_ascii_digit(cp) || cp == U'/' || cp == U'.'
        || cp == U'-';
}

bool is_swift_x_char(char32_t cp) noexcept {
    if (cp > 0x7F) {
        return false;
    }
    return std::find(kSwiftXChars.begin(), kSwiftXChars.end(), static_cast<char>(cp))
        != kSwiftXChars.end();
}

bool is_unicode_line_break(char32_t cp) noexcept {
    return cp == U'\n' || cp == U'\r' || cp == 0x85 || cp == 0x2028 || cp == 0x2029;
}

bool is_colon_confusable(char32_t cp) noexcept {
    return cp == 0xFF1A || cp == 0xFE55 || cp == 0xA789 || cp == 0x2236;
}

bool is_unicode_control(char32_t cp) noexcept {
    return cp < 0x20 || cp == 0x7F || (cp >= 0x80 && cp <= 0x9F);
}

bool is_unicode_space(char32_t cp) noexcept {
    return cp == 0xA0 || cp == 0x1680 || (cp >= 0x2000 && cp <= 0x200A)
        || cp == 0x202F || cp == 0x205F || cp == 0x3000;
}

bool is_unicode_format_control(char32_t cp) noexcept {
    return cp == 0xAD || cp == 0x34F || (cp >= 0x600 && cp <= 0x605) || cp == 0x61C
        || cp == 0x6DD || cp == 0x70F || (cp >= 0x890 && cp <= 0x891)
        || cp == 0x8E2 || (cp >= 0x180B && cp <= 0x180E)
        || (cp >= 0x200B && cp <= 0x200F)
        || (cp >= 0x202A && cp <= 0x202E) || (cp >= 0x2060 && cp <= 0x2064)
        || (cp >= 0x2066 && cp <= 0x206F) || cp == 0xFEFF
        || (cp >= 0xFE00 && cp <= 0xFE0F)
        || (cp >= 0xFFF9 && cp <= 0xFFFB) || cp == 0x110BD || cp == 0x110CD
        || cp == 0xE0001 || (cp >= 0xE0020 && cp <= 0xE007F)
        || (cp >= 0xE0100 && cp <= 0xE01EF);
}

bool is_noncharacter(char32_t cp) noexcept {
    return (cp >= 0xFDD0 && cp <= 0xFDEF) || (cp & 0xFFFE) == 0xFFFE;
}

bool decode_utf8(
    std::string_view text,
    std::size_t offset,
    char32_t& code_point,
    std::size_t& width) noexcept {
    const auto first = static_cast<unsigned char>(text[offset]);
    if (first <= 0x7F) {
        code_point = first;
        width = 1;
        return true;
    }

    std::size_t continuation_count = 0;
    unsigned char second_min = 0x80;
    unsigned char second_max = 0xBF;

    if (first >= 0xC2 && first <= 0xDF) {
        code_point = first & 0x1F;
        continuation_count = 1;
    } else if (first >= 0xE0 && first <= 0xEF) {
        code_point = first & 0x0F;
        continuation_count = 2;
        if (first == 0xE0) {
            second_min = 0xA0;
        } else if (first == 0xED) {
            second_max = 0x9F;
        }
    } else if (first >= 0xF0 && first <= 0xF4) {
        code_point = first & 0x07;
        continuation_count = 3;
        if (first == 0xF0) {
            second_min = 0x90;
        } else if (first == 0xF4) {
            second_max = 0x8F;
        }
    } else {
        return false;
    }

    if (offset + continuation_count >= text.size()) {
        return false;
    }

    for (std::size_t index = 1; index <= continuation_count; ++index) {
        const auto byte = static_cast<unsigned char>(text[offset + index]);
        if (index == 1) {
            if (byte < second_min || byte > second_max) {
                return false;
            }
        } else if (byte < 0x80 || byte > 0xBF) {
            return false;
        }
        code_point = (code_point << 6) | (byte & 0x3F);
    }

    width = continuation_count + 1;
    return code_point <= 0x10FFFF && !(code_point >= 0xD800 && code_point <= 0xDFFF);
}

bool valid_tag(std::string_view tag) noexcept {
    if (tag.size() != 2 && tag.size() != 3) {
        return false;
    }
    if (!is_ascii_digit(static_cast<unsigned char>(tag[0]))
        || !is_ascii_digit(static_cast<unsigned char>(tag[1]))) {
        return false;
    }
    return tag.size() == 2 || (tag[2] >= 'A' && tag[2] <= 'Z');
}

bool valid_reference(std::string_view value) noexcept {
    if (value.empty() || value.size() > 16) {
        return false;
    }
    const auto first = static_cast<unsigned char>(value[0]);
    if (!is_ascii_alpha(first) && !is_ascii_digit(first)) {
        return false;
    }
    return std::all_of(value.begin() + 1, value.end(), [](char ch) {
        return is_reference_char(static_cast<unsigned char>(ch));
    });
}

bool valid_amount(std::string_view value) noexcept {
    if (value.size() < 4 || value.size() > 18) {
        return false;
    }
    const auto dot = value.find('.');
    if (dot == std::string_view::npos || dot == 0 || dot > 15) {
        return false;
    }
    if (value.find('.', dot + 1) != std::string_view::npos
        || value.size() - dot - 1 != 2) {
        return false;
    }
    return std::all_of(value.begin(), value.begin() + static_cast<std::ptrdiff_t>(dot), [](char ch) {
               return ch >= '0' && ch <= '9';
           })
        && std::all_of(value.begin() + static_cast<std::ptrdiff_t>(dot + 1), value.end(), [](char ch) {
               return ch >= '0' && ch <= '9';
           });
}

std::optional<ValidationError> validate_value(
    const Field& field,
    const FieldSpec& spec,
    std::size_t field_index) {
    if (field.value.empty()) {
        return make_error(
            ErrorCode::EmptyValue,
            field_index,
            0,
            0,
            "field value must not be empty");
    }

    std::size_t byte_offset = 0;
    std::size_t char_count = 0;
    while (byte_offset < field.value.size()) {
        char32_t code_point = 0;
        std::size_t width = 0;
        if (!decode_utf8(field.value, byte_offset, code_point, width)) {
            return make_error(
                ErrorCode::InvalidUtf8,
                field_index,
                byte_offset,
                char_count,
                "field value is not valid UTF-8");
        }

        if (code_point == U'\n' || code_point == U'\r') {
            return make_error(
                ErrorCode::LineBreak,
                field_index,
                byte_offset,
                char_count,
                "carriage return and line feed are forbidden in a field");
        }
        if (code_point == U':') {
            return make_error(
                ErrorCode::FieldSeparator,
                field_index,
                byte_offset,
                char_count,
                "field separator ':' is forbidden in a field value");
        }
        if (is_unicode_line_break(code_point)) {
            return make_error(
                ErrorCode::LineBreak,
                field_index,
                byte_offset,
                char_count,
                "Unicode line separator is forbidden in a field");
        }
        if (is_colon_confusable(code_point)) {
            return make_error(
                ErrorCode::FieldSeparator,
                field_index,
                byte_offset,
                char_count,
                "colon-like character is forbidden in a field");
        }
        if (is_unicode_control(code_point)) {
            return make_error(
                ErrorCode::ControlCharacter,
                field_index,
                byte_offset,
                char_count,
                "control character is forbidden in a field");
        }
        if (is_unicode_space(code_point) || is_unicode_format_control(code_point)
            || is_noncharacter(code_point) || code_point == 0xFFFD) {
            return make_error(
                ErrorCode::DisallowedCharacter,
                field_index,
                byte_offset,
                char_count,
                "character is not allowed by the field character set");
        }

        if ((spec.kind == FieldKind::SwiftText || spec.kind == FieldKind::Reference
                || spec.kind == FieldKind::Amount)
            && !is_swift_x_char(code_point)) {
            return make_error(
                ErrorCode::DisallowedCharacter,
                field_index,
                byte_offset,
                char_count,
                "character is not allowed by the SWIFT X character set");
        }

        byte_offset += width;
        ++char_count;
    }

    if (char_count > spec.max_chars) {
        std::ostringstream message;
        message << "field length " << char_count << " exceeds limit " << spec.max_chars;
        return make_error(
            ErrorCode::LengthExceeded,
            field_index,
            field.value.size(),
            char_count,
            message.str());
    }

    if (spec.kind == FieldKind::Reference && !valid_reference(field.value)) {
        return make_error(
            ErrorCode::InvalidReference,
            field_index,
            0,
            0,
            "reference must match [A-Za-z0-9][A-Za-z0-9/.-]{0,15}");
    }
    if (spec.kind == FieldKind::Amount && !valid_amount(field.value)) {
        return make_error(
            ErrorCode::InvalidAmount,
            field_index,
            0,
            0,
            "amount must match [0-9]{1,15}\\.[0-9]{2}");
    }

    return std::nullopt;
}

ValidationError malformed(
    std::size_t field_index,
    std::size_t byte_offset,
    std::string message) {
    return make_error(
        ErrorCode::MalformedMessage,
        field_index,
        byte_offset,
        0,
        std::move(message));
}

std::string encode_validated(std::span<const Field> fields) {
    std::size_t total_size = 0;
    for (const auto& field : fields) {
        total_size += field.tag.size() + field.value.size() + 3;
    }

    std::string wire;
    wire.reserve(total_size);
    for (const auto& field : fields) {
        wire.push_back(':');
        wire.append(field.tag);
        wire.push_back(':');
        wire.append(field.value);
        wire.push_back('\n');
    }
    return wire;
}

}  // namespace

std::string_view error_code_name(ErrorCode code) noexcept {
    switch (code) {
    case ErrorCode::InvalidFieldSpec:
        return "InvalidFieldSpec";
    case ErrorCode::UnknownField:
        return "UnknownField";
    case ErrorCode::InvalidTag:
        return "InvalidTag";
    case ErrorCode::EmptyValue:
        return "EmptyValue";
    case ErrorCode::InvalidUtf8:
        return "InvalidUtf8";
    case ErrorCode::LineBreak:
        return "LineBreak";
    case ErrorCode::FieldSeparator:
        return "FieldSeparator";
    case ErrorCode::ControlCharacter:
        return "ControlCharacter";
    case ErrorCode::DisallowedCharacter:
        return "DisallowedCharacter";
    case ErrorCode::LengthExceeded:
        return "LengthExceeded";
    case ErrorCode::InvalidReference:
        return "InvalidReference";
    case ErrorCode::InvalidAmount:
        return "InvalidAmount";
    case ErrorCode::MalformedMessage:
        return "MalformedMessage";
    }
    return "Unknown";
}

CodecError::CodecError(std::string operation, ValidationError error)
    : std::runtime_error(
          operation + " failed: " + std::string(error_code_name(error.code)) + " at field "
          + std::to_string(error.field_index) + ": " + error.message),
      operation_(std::move(operation)),
      error_(std::move(error)) {}

FieldCodec::FieldCodec(std::vector<FieldSpec> specs) : specs_(std::move(specs)) {
    std::unordered_set<std::string> seen;
    for (const auto& spec : specs_) {
        if (!valid_tag(spec.tag)) {
            throw std::invalid_argument("field spec has an invalid MT tag: " + spec.tag);
        }
        if (spec.max_chars == 0) {
            throw std::invalid_argument("field spec max_chars must be greater than zero");
        }
        if (!seen.insert(spec.tag).second) {
            throw std::invalid_argument("duplicate field spec tag: " + spec.tag);
        }
    }
}

FieldCodec FieldCodec::swift_mt_standard() {
    return FieldCodec({
        {"20", FieldKind::Reference, 16},
        {"23B", FieldKind::SwiftText, 16},
        {"32A", FieldKind::Amount, 18},
        {"33B", FieldKind::Amount, 18},
        {"50K", FieldKind::SwiftText, 140},
        {"52A", FieldKind::SwiftText, 11},
        {"57A", FieldKind::SwiftText, 11},
        {"59", FieldKind::SwiftText, 140},
        {"70", FieldKind::SwiftText, 140},
        {"71A", FieldKind::SwiftText, 3},
    });
}

const FieldSpec* FieldCodec::find_spec(std::string_view tag) const noexcept {
    for (const auto& spec : specs_) {
        if (spec.tag == tag) {
            return &spec;
        }
    }
    return nullptr;
}

ValidationResult FieldCodec::validate(const Field& field) const {
    ValidationResult result;
    if (!valid_tag(field.tag)) {
        result.errors.push_back(make_error(
            ErrorCode::InvalidTag,
            0,
            0,
            0,
            "field tag must match [0-9]{2}[A-Z]?"));
        return result;
    }

    const auto* spec = find_spec(field.tag);
    if (spec == nullptr) {
        result.errors.push_back(make_error(
            ErrorCode::UnknownField,
            0,
            0,
            0,
            "field tag is not present in the codec specification"));
        return result;
    }

    if (auto error = validate_value(field, *spec, 0)) {
        result.errors.push_back(std::move(*error));
    }
    return result;
}

ValidationResult FieldCodec::validate(std::span<const Field> fields) const {
    ValidationResult result;
    for (std::size_t index = 0; index < fields.size(); ++index) {
        const auto& field = fields[index];
        if (!valid_tag(field.tag)) {
            result.errors.push_back(make_error(
                ErrorCode::InvalidTag,
                index,
                0,
                0,
                "field tag must match [0-9]{2}[A-Z]?"));
            continue;
        }

        const auto* spec = find_spec(field.tag);
        if (spec == nullptr) {
            result.errors.push_back(make_error(
                ErrorCode::UnknownField,
                index,
                0,
                0,
                "field tag is not present in the codec specification"));
            continue;
        }

        if (auto error = validate_value(field, *spec, index)) {
            result.errors.push_back(std::move(*error));
        }
    }
    return result;
}

std::string FieldCodec::encode(std::span<const Field> fields) const {
    const auto validation = validate(fields);
    if (!validation.ok()) {
        throw CodecError("encode", validation.errors.front());
    }
    return encode_validated(fields);
}

std::optional<std::string> FieldCodec::try_encode(
    std::span<const Field> fields,
    ValidationResult* result) const {
    auto validation = validate(fields);
    if (!validation.ok()) {
        if (result != nullptr) {
            *result = std::move(validation);
        }
        return std::nullopt;
    }
    if (result != nullptr) {
        *result = ValidationResult{};
    }
    return encode_validated(fields);
}

std::vector<Field> FieldCodec::decode(std::string_view wire) const {
    std::vector<Field> fields;
    std::size_t offset = 0;
    while (offset < wire.size()) {
        const std::size_t field_start = offset;
        if (wire[offset] != ':') {
            throw CodecError(
                "decode",
                malformed(fields.size(), offset, "field must start with ':'"));
        }

        const auto tag_end = wire.find(':', offset + 1);
        if (tag_end == std::string_view::npos) {
            throw CodecError(
                "decode",
                malformed(fields.size(), offset, "field tag is missing its closing ':'"));
        }
        const auto tag = wire.substr(offset + 1, tag_end - offset - 1);
        if (!valid_tag(tag)) {
            throw CodecError(
                "decode",
                make_error(
                    ErrorCode::InvalidTag,
                    fields.size(),
                    offset + 1,
                    0,
                    "field tag must match [0-9]{2}[A-Z]?"));
        }

        const auto value_start = tag_end + 1;
        const auto line_end = wire.find('\n', value_start);
        if (line_end == std::string_view::npos) {
            throw CodecError(
                "decode",
                malformed(fields.size(), field_start, "field is missing its line terminator"));
        }
        const auto carriage_return = wire.find('\r', value_start);
        if (carriage_return != std::string_view::npos && carriage_return < line_end) {
            throw CodecError(
                "decode",
                make_error(
                    ErrorCode::LineBreak,
                    fields.size(),
                    carriage_return,
                    0,
                    "carriage return is forbidden in a field"));
        }

        Field field{std::string(tag), std::string(wire.substr(value_start, line_end - value_start))};
        const auto* spec = find_spec(field.tag);
        if (spec == nullptr) {
            throw CodecError(
                "decode",
                make_error(
                    ErrorCode::UnknownField,
                    fields.size(),
                    offset + 1,
                    0,
                    "field tag is not present in the codec specification"));
        }
        if (auto error = validate_value(field, *spec, fields.size())) {
            throw CodecError("decode", std::move(*error));
        }

        fields.push_back(std::move(field));
        offset = line_end + 1;
    }
    return fields;
}

std::optional<std::vector<Field>> FieldCodec::try_decode(
    std::string_view wire,
    ValidationResult* result) const {
    try {
        auto fields = decode(wire);
        if (result != nullptr) {
            *result = ValidationResult{};
        }
        return fields;
    } catch (const CodecError& error) {
        if (result != nullptr) {
            result->errors.push_back(error.error());
        }
        return std::nullopt;
    }
}

ValidationResult validate_fields(
    std::span<const Field> fields,
    const FieldCodec& codec) {
    return codec.validate(fields);
}

std::string encode_fields(
    std::span<const Field> fields,
    const FieldCodec& codec) {
    return codec.encode(fields);
}

std::vector<Field> decode_fields(
    std::string_view wire,
    const FieldCodec& codec) {
    return codec.decode(wire);
}

}  // namespace netsec
