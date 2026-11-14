#pragma once

#include <array>
#include <bitset>
#include <cstddef>
#include <cstdint>
#include <map>
#include <optional>
#include <span>
#include <string>
#include <string_view>

namespace netsec {

inline constexpr int kMaxFieldNumber = 128;
inline constexpr std::size_t kBitmapBytes = 8;

enum class FieldClass { Fixed, LLVar, LLLVar };
enum class FieldEncoding { Numeric, AlphaNumeric, Binary };
enum class SemanticRule { None, Amount, Currency, Account };

struct FieldSpec {
    FieldClass kind = FieldClass::Fixed;
    FieldEncoding encoding = FieldEncoding::Numeric;
    std::uint16_t length = 0;  // Fixed: exact byte count; LLVar/LLLVar: max byte count
    bool required = false;
};

enum class ParseError {
    None,
    TruncatedBitmap,
    TruncatedLengthPrefix,
    TruncatedField,
    UnknownField,
    UnnegotiatedField,
    LengthOutOfBounds,
    DuplicateField,
    InvalidEncoding,
    InvalidAmount,
    InvalidCurrency,
    InvalidAccount,
    MissingRequiredField,
    TrailingBytes,
};

std::string_view to_string(ParseError error) noexcept;

struct FieldValue {
    std::string bytes;  // raw field content, length prefix excluded
};

struct ParsedMessage {
    std::map<int, FieldValue> fields;
    std::size_t consumed = 0;
    bool secondary_bitmap_present = false;
};

struct ParseResult {
    std::optional<ParsedMessage> message;
    ParseError error = ParseError::None;
    int field = 0;  // field involved in the error, 0 when not applicable

    explicit operator bool() const noexcept { return message.has_value(); }
};

// Bitmap-driven message parser for the financial gateway.
//
// Wire format: 8-byte primary bitmap (bit 1 = secondary bitmap present),
// optional 8-byte secondary bitmap, then field data in field-number order.
// Bit n (1-based, MSB first) signals field n. LLVar fields carry a 2-digit
// ASCII decimal length prefix, LLLVar a 3-digit prefix.
//
// The parser enforces: negotiated-field allowlist, per-field length bounds,
// encoding checks, at-most-once occurrence, semantic rules for
// amount/currency/account fields, and exact buffer consumption so every
// accepted message has exactly one parse result.
class BitmapParser {
public:
    BitmapParser(std::array<std::optional<FieldSpec>, kMaxFieldNumber + 1> specs,
                 std::array<SemanticRule, kMaxFieldNumber + 1> semantics,
                 std::bitset<kMaxFieldNumber + 1> negotiated);

    ParseResult parse(std::span<const std::byte> message) const;
    ParseResult parse(std::string_view message) const;

private:
    std::array<std::optional<FieldSpec>, kMaxFieldNumber + 1> specs_;
    std::array<SemanticRule, kMaxFieldNumber + 1> semantics_;
    std::bitset<kMaxFieldNumber + 1> negotiated_;
};

}  // namespace netsec
