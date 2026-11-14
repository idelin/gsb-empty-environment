#pragma once

#include <cstddef>
#include <optional>
#include <span>
#include <stdexcept>
#include <string>
#include <string_view>
#include <vector>

namespace netsec {

enum class FieldKind {
    SwiftText,
    Reference,
    Amount,
    Utf8Text,
};

enum class ErrorCode {
    InvalidFieldSpec,
    UnknownField,
    InvalidTag,
    EmptyValue,
    InvalidUtf8,
    LineBreak,
    FieldSeparator,
    ControlCharacter,
    DisallowedCharacter,
    LengthExceeded,
    InvalidReference,
    InvalidAmount,
    MalformedMessage,
};

[[nodiscard]] std::string_view error_code_name(ErrorCode code) noexcept;

struct FieldSpec {
    std::string tag;
    FieldKind kind{FieldKind::SwiftText};
    std::size_t max_chars{0};
};

struct Field {
    std::string tag;
    std::string value;
};

struct ValidationError {
    ErrorCode code{ErrorCode::MalformedMessage};
    std::size_t field_index{0};
    std::size_t byte_offset{0};
    std::size_t char_offset{0};
    std::string message;
};

struct ValidationResult {
    std::vector<ValidationError> errors;

    [[nodiscard]] bool ok() const noexcept { return errors.empty(); }
    [[nodiscard]] explicit operator bool() const noexcept { return ok(); }

    [[nodiscard]] const ValidationError* first_error() const noexcept {
        return errors.empty() ? nullptr : &errors.front();
    }
};

class CodecError : public std::runtime_error {
public:
    CodecError(std::string operation, ValidationError error);

    [[nodiscard]] const std::string& operation() const noexcept { return operation_; }
    [[nodiscard]] const ValidationError& error() const noexcept { return error_; }

private:
    std::string operation_;
    ValidationError error_;
};

class FieldCodec {
public:
    explicit FieldCodec(std::vector<FieldSpec> specs);

    [[nodiscard]] static FieldCodec swift_mt_standard();

    [[nodiscard]] const std::vector<FieldSpec>& specs() const noexcept { return specs_; }
    [[nodiscard]] const FieldSpec* find_spec(std::string_view tag) const noexcept;

    [[nodiscard]] ValidationResult validate(const Field& field) const;
    [[nodiscard]] ValidationResult validate(std::span<const Field> fields) const;

    [[nodiscard]] std::string encode(std::span<const Field> fields) const;
    [[nodiscard]] std::string encode(const std::vector<Field>& fields) const {
        return encode(std::span<const Field>(fields));
    }

    [[nodiscard]] std::optional<std::string> try_encode(
        std::span<const Field> fields,
        ValidationResult* result = nullptr) const;
    [[nodiscard]] std::optional<std::string> try_encode(
        const std::vector<Field>& fields,
        ValidationResult* result = nullptr) const {
        return try_encode(std::span<const Field>(fields), result);
    }

    [[nodiscard]] std::vector<Field> decode(std::string_view wire) const;
    [[nodiscard]] std::optional<std::vector<Field>> try_decode(
        std::string_view wire,
        ValidationResult* result = nullptr) const;

private:
    std::vector<FieldSpec> specs_;
};

using MtFieldCodec = FieldCodec;

[[nodiscard]] ValidationResult validate_fields(
    std::span<const Field> fields,
    const FieldCodec& codec);

[[nodiscard]] std::string encode_fields(
    std::span<const Field> fields,
    const FieldCodec& codec);

[[nodiscard]] std::vector<Field> decode_fields(
    std::string_view wire,
    const FieldCodec& codec);

}  // namespace netsec
