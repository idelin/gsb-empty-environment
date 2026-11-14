#include "netsec.hpp"

#include <array>
#include <cstdint>

namespace netsec {
namespace {

ParseResult fail(ParseError error, int field = 0) {
    return ParseResult{std::nullopt, error, field};
}

bool bitmap_bit(std::span<const std::byte> bitmap, int field_number) {
    const auto index = static_cast<std::size_t>(field_number - 1);
    const auto byte = static_cast<unsigned char>(bitmap[index / 8]);
    return (byte >> (7 - (index % 8))) & 1u;
}

bool is_digits(std::string_view value) {
    for (char c : value) {
        if (c < '0' || c > '9') return false;
    }
    return !value.empty();
}

bool is_printable(std::string_view value) {
    for (char c : value) {
        if (c < 0x20 || c > 0x7e) return false;
    }
    return true;
}

bool valid_amount(std::string_view value) {
    if (value.size() != 12 || !is_digits(value)) return false;
    std::uint64_t amount = 0;
    for (char c : value) {
        const auto digit = static_cast<std::uint64_t>(c - '0');
        if (amount > (UINT64_MAX - digit) / 10) return false;
        amount = amount * 10 + digit;
    }
    return true;
}

bool valid_currency(std::string_view value) {
    // ISO 4217 numeric codes accepted by this gateway.
    static constexpr std::array<std::string_view, 12> kCurrencies = {
        "156", "344", "392", "410", "702", "840",
        "826", "901", "036", "124", "978", "764",
    };
    if (value.size() != 3 || !is_digits(value)) return false;
    for (auto code : kCurrencies) {
        if (value == code) return true;
    }
    return false;
}

bool valid_account(std::string_view value) {
    if (value.size() < 11 || value.size() > 19 || !is_digits(value)) return false;
    int sum = 0;
    bool double_digit = false;
    for (auto it = value.rbegin(); it != value.rend(); ++it) {
        int digit = *it - '0';
        if (double_digit) {
            digit *= 2;
            if (digit > 9) digit -= 9;
        }
        sum += digit;
        double_digit = !double_digit;
    }
    return sum % 10 == 0;
}

bool valid_semantic(SemanticRule rule, std::string_view value) {
    switch (rule) {
        case SemanticRule::None: return true;
        case SemanticRule::Amount: return valid_amount(value);
        case SemanticRule::Currency: return valid_currency(value);
        case SemanticRule::Account: return valid_account(value);
    }
    return false;
}

ParseError semantic_error(SemanticRule rule) {
    switch (rule) {
        case SemanticRule::Amount: return ParseError::InvalidAmount;
        case SemanticRule::Currency: return ParseError::InvalidCurrency;
        case SemanticRule::Account: return ParseError::InvalidAccount;
        case SemanticRule::None: break;
    }
    return ParseError::InvalidEncoding;
}

}  // namespace

std::string_view to_string(ParseError error) noexcept {
    switch (error) {
        case ParseError::None: return "none";
        case ParseError::TruncatedBitmap: return "truncated bitmap";
        case ParseError::TruncatedLengthPrefix: return "truncated length prefix";
        case ParseError::TruncatedField: return "truncated field";
        case ParseError::UnknownField: return "unknown field";
        case ParseError::UnnegotiatedField: return "unnegotiated field";
        case ParseError::LengthOutOfBounds: return "length out of bounds";
        case ParseError::DuplicateField: return "duplicate field";
        case ParseError::InvalidEncoding: return "invalid encoding";
        case ParseError::InvalidAmount: return "invalid amount";
        case ParseError::InvalidCurrency: return "invalid currency";
        case ParseError::InvalidAccount: return "invalid account";
        case ParseError::MissingRequiredField: return "missing required field";
        case ParseError::TrailingBytes: return "trailing bytes";
    }
    return "unknown";
}

BitmapParser::BitmapParser(
    std::array<std::optional<FieldSpec>, kMaxFieldNumber + 1> specs,
    std::array<SemanticRule, kMaxFieldNumber + 1> semantics,
    std::bitset<kMaxFieldNumber + 1> negotiated)
    : specs_(std::move(specs)),
      semantics_(std::move(semantics)),
      negotiated_(negotiated) {}

ParseResult BitmapParser::parse(std::string_view message) const {
    return parse(std::span<const std::byte>(
        reinterpret_cast<const std::byte*>(message.data()), message.size()));
}

ParseResult BitmapParser::parse(std::span<const std::byte> message) const {
    if (message.size() < kBitmapBytes) {
        return fail(ParseError::TruncatedBitmap);
    }

    const bool has_secondary = bitmap_bit(message.first(kBitmapBytes), 1);
    const std::size_t bitmap_bytes = has_secondary ? 2 * kBitmapBytes : kBitmapBytes;
    if (message.size() < bitmap_bytes) {
        return fail(ParseError::TruncatedBitmap);
    }
    const auto bitmap = message.first(bitmap_bytes);
    const int max_field = has_secondary ? kMaxFieldNumber : 64;

    ParsedMessage parsed;
    parsed.secondary_bitmap_present = has_secondary;
    std::bitset<kMaxFieldNumber + 1> seen;
    std::size_t offset = bitmap_bytes;

    for (int field = 2; field <= max_field; ++field) {
        if (!bitmap_bit(bitmap, field)) continue;

        if (seen.test(field)) {
            return fail(ParseError::DuplicateField, field);
        }
        seen.set(field);

        const auto& spec = specs_[field];
        if (!spec.has_value()) {
            return fail(ParseError::UnknownField, field);
        }
        if (!negotiated_.test(field)) {
            return fail(ParseError::UnnegotiatedField, field);
        }

        std::size_t field_length = spec->length;
        if (spec->kind != FieldClass::Fixed) {
            const std::size_t prefix_digits = spec->kind == FieldClass::LLVar ? 2 : 3;
            if (message.size() - offset < prefix_digits) {
                return fail(ParseError::TruncatedLengthPrefix, field);
            }
            std::string prefix(prefix_digits, '\0');
            for (std::size_t i = 0; i < prefix_digits; ++i) {
                prefix[i] = static_cast<char>(message[offset + i]);
            }
            if (!is_digits(prefix)) {
                return fail(ParseError::InvalidEncoding, field);
            }
            std::size_t declared = 0;
            for (char c : prefix) {
                declared = declared * 10 + static_cast<std::size_t>(c - '0');
            }
            if (declared > spec->length) {
                return fail(ParseError::LengthOutOfBounds, field);
            }
            field_length = declared;
            offset += prefix_digits;
        }

        if (message.size() - offset < field_length) {
            return fail(ParseError::TruncatedField, field);
        }
        std::string value(field_length, '\0');
        for (std::size_t i = 0; i < field_length; ++i) {
            value[i] = static_cast<char>(message[offset + i]);
        }
        offset += field_length;

        bool encoding_ok = false;
        switch (spec->encoding) {
            case FieldEncoding::Numeric: encoding_ok = is_digits(value); break;
            case FieldEncoding::AlphaNumeric: encoding_ok = is_printable(value); break;
            case FieldEncoding::Binary: encoding_ok = true; break;
        }
        if (!encoding_ok) {
            return fail(ParseError::InvalidEncoding, field);
        }

        if (!valid_semantic(semantics_[field], value)) {
            return fail(semantic_error(semantics_[field]), field);
        }

        parsed.fields.emplace(field, FieldValue{std::move(value)});
    }

    if (offset != message.size()) {
        // Unconsumed bytes would allow a second interpretation of the
        // message (e.g. a re-parsed amount); reject to keep parsing unique.
        return fail(ParseError::TrailingBytes);
    }

    for (int field = 2; field <= kMaxFieldNumber; ++field) {
        if (specs_[field].has_value() && specs_[field]->required &&
            negotiated_.test(field) && !seen.test(field)) {
            return fail(ParseError::MissingRequiredField, field);
        }
    }

    parsed.consumed = offset;
    return ParseResult{std::move(parsed), ParseError::None, 0};
}

}  // namespace netsec
