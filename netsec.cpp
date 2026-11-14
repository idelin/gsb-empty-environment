#include "netsec.hpp"

#include "netsec_crypto.hpp"

#include <algorithm>
#include <array>
#include <cctype>
#include <cstddef>
#include <cstdint>
#include <limits>
#include <optional>
#include <string>
#include <vector>

namespace netsec {
namespace {

// ---------------------------------------------------------------------------
// Small helpers
// ---------------------------------------------------------------------------

Error make_error(ErrorCode code, std::string message, std::size_t offset = 0) {
    return Error{code, std::move(message), offset};
}

bool is_printable_ascii(std::string_view text) {
    return std::all_of(text.begin(), text.end(), [](char character) {
        const auto value = static_cast<unsigned char>(character);
        return value >= 0x20U && value <= 0x7eU;
    });
}

char hex_digit(unsigned value) {
    return static_cast<char>(value < 10 ? '0' + value : 'a' + (value - 10));
}

std::string hex_encode(ByteView bytes, bool with_prefix = true) {
    std::string output;
    output.reserve(bytes.size() * 2U + (with_prefix ? 2U : 0U));
    if (with_prefix) {
        output += "0x";
    }
    for (const std::uint8_t byte : bytes) {
        output.push_back(hex_digit(byte >> 4U));
        output.push_back(hex_digit(byte & 0x0fU));
    }
    return output;
}

int hex_value(char character) {
    if (character >= '0' && character <= '9') {
        return character - '0';
    }
    if (character >= 'a' && character <= 'f') {
        return character - 'a' + 10;
    }
    if (character >= 'A' && character <= 'F') {
        return character - 'A' + 10;
    }
    return -1;
}

std::string_view strip_hex_prefix(std::string_view text) {
    if (text.size() >= 2 && text[0] == '0' &&
        (text[1] == 'x' || text[1] == 'X')) {
        return text.substr(2);
    }
    return text;
}

// ---------------------------------------------------------------------------
// 256-bit integer helpers (the public Uint256 stores network byte order).
// ---------------------------------------------------------------------------

struct Big {
    std::uint64_t word[4]{};
};

void multiply_64x64(std::uint64_t left,
                    std::uint64_t right,
                    std::uint64_t& high,
                    std::uint64_t& low) {
    const std::uint64_t left_low = left & 0xffffffffULL;
    const std::uint64_t left_high = left >> 32U;
    const std::uint64_t right_low = right & 0xffffffffULL;
    const std::uint64_t right_high = right >> 32U;
    const std::uint64_t p0 = left_low * right_low;
    const std::uint64_t p1 = left_low * right_high;
    const std::uint64_t p2 = left_high * right_low;
    const std::uint64_t p3 = left_high * right_high;
    const std::uint64_t middle =
        (p0 >> 32U) + (p1 & 0xffffffffULL) + (p2 & 0xffffffffULL);
    low = (middle << 32U) | (p0 & 0xffffffffULL);
    high = p3 + (p1 >> 32U) + (p2 >> 32U) + (middle >> 32U);
}

std::uint64_t add_limbs(std::uint64_t left,
                        std::uint64_t right,
                        std::uint64_t carry,
                        std::uint64_t& result) {
    result = left + right;
    std::uint64_t next = result < left ? 1U : 0U;
    const std::uint64_t with_carry = result + carry;
    if (with_carry < result) {
        ++next;
    }
    result = with_carry;
    return next;
}

Big big_from_be(const std::array<std::uint8_t, 32>& bytes) {
    Big value;
    for (unsigned index = 0; index < 4; ++index) {
        std::uint64_t lane = 0;
        for (unsigned byte = 0; byte < 8; ++byte) {
            lane = (lane << 8U) | bytes[index * 8U + byte];
        }
        value.word[3U - index] = lane;
    }
    return value;
}

std::array<std::uint8_t, 32> big_to_be(const Big& value) {
    std::array<std::uint8_t, 32> bytes{};
    for (unsigned index = 0; index < 4; ++index) {
        std::uint64_t lane = value.word[3U - index];
        for (unsigned byte = 0; byte < 8; ++byte) {
            bytes[index * 8U + byte] =
                static_cast<std::uint8_t>(lane >> (56U - 8U * byte));
        }
    }
    return bytes;
}

bool big_zero(const Big& value) {
    return (value.word[0] | value.word[1] | value.word[2] | value.word[3]) == 0;
}

int big_compare(const Big& left, const Big& right) {
    for (int index = 3; index >= 0; --index) {
        if (left.word[index] < right.word[index]) {
            return -1;
        }
        if (left.word[index] > right.word[index]) {
            return 1;
        }
    }
    return 0;
}

bool big_multiply_checked(const Big& left, const Big& right, Big& output) {
    std::uint64_t product[8] = {};
    for (unsigned i = 0; i < 4; ++i) {
        std::uint64_t carry = 0;
        for (unsigned j = 0; j < 4; ++j) {
            std::uint64_t high = 0;
            std::uint64_t low = 0;
            multiply_64x64(left.word[i], right.word[j], high, low);
            std::uint64_t next = 0;
            const std::uint64_t carry_out =
                add_limbs(low, product[i + j], carry, next);
            product[i + j] = next;
            carry = high + carry_out;
        }
        product[i + 4] += carry;
    }
    if ((product[4] | product[5] | product[6] | product[7]) != 0) {
        return false;
    }
    output.word[0] = product[0];
    output.word[1] = product[1];
    output.word[2] = product[2];
    output.word[3] = product[3];
    return true;
}

std::uint64_t divide_128_by_64(std::uint64_t high,
                               std::uint64_t low,
                               std::uint64_t divisor,
                               std::uint64_t& remainder) {
    std::uint64_t quotient = 0;
    std::uint64_t current = high;
    for (int bit = 63; bit >= 0; --bit) {
        const std::uint64_t carry = current >> 63U;
        current = (current << 1U) | ((low >> static_cast<unsigned>(bit)) & 1U);
        if (carry != 0 || current >= divisor) {
            current -= divisor;
            quotient |= (std::uint64_t{1} << static_cast<unsigned>(bit));
        }
    }
    remainder = current;
    return quotient;
}

std::string big_decimal(Big value) {
    if (big_zero(value)) {
        return "0";
    }
    std::string reversed;
    while (!big_zero(value)) {
        std::uint64_t remainder = 0;
        for (int index = 3; index >= 0; --index) {
            value.word[index] =
                divide_128_by_64(remainder, value.word[index], 10U, remainder);
        }
        reversed.push_back(static_cast<char>('0' + remainder));
    }
    std::reverse(reversed.begin(), reversed.end());
    return reversed;
}

std::string format_units(const Big& value,
                         std::uint8_t decimals,
                         std::string_view symbol) {
    const std::string base = big_decimal(value);
    std::string display;
    if (decimals == 0) {
        display = base;
    } else if (base.size() <= decimals) {
        display = "0.";
        display.append(static_cast<std::size_t>(decimals) - base.size(), '0');
        display += base;
    } else {
        const std::size_t split = base.size() - decimals;
        display = base.substr(0, split);
        display.push_back('.');
        display += base.substr(split);
    }

    if (display.find('.') != std::string::npos) {
        while (!display.empty() && display.back() == '0') {
            display.pop_back();
        }
        if (!display.empty() && display.back() == '.') {
            display.pop_back();
        }
    }
    if (!symbol.empty()) {
        display.push_back(' ');
        display += symbol;
    }
    return display;
}

// ---------------------------------------------------------------------------
// Strict canonical RLP
// ---------------------------------------------------------------------------

struct RlpItem {
    enum class Kind : std::uint8_t { String, List };

    Kind kind = Kind::String;
    std::size_t start = 0;
    std::size_t payload_start = 0;
    std::size_t payload_size = 0;

    [[nodiscard]] std::size_t end() const { return payload_start + payload_size; }
};

constexpr std::size_t kMaximumRlpDepth = 24;
constexpr std::size_t kMaximumRlpChildren = 4096;

std::optional<RlpItem> decode_rlp_item(ByteView input,
                                       std::size_t offset,
                                       std::size_t limit,
                                       std::size_t depth,
                                       Error& error) {
    if (depth > kMaximumRlpDepth) {
        error = make_error(ErrorCode::MalformedRlp, "RLP nesting is too deep",
                           offset);
        return std::nullopt;
    }
    if (offset >= limit || limit > input.size()) {
        error = make_error(ErrorCode::MalformedRlp, "truncated RLP item", offset);
        return std::nullopt;
    }

    const std::uint8_t prefix = input[offset];
    if (prefix <= 0x7fU) {
        return RlpItem{RlpItem::Kind::String, offset, offset, 1};
    }

    if (prefix <= 0xb7U) {
        const std::size_t length = prefix - 0x80U;
        if (length > limit - (offset + 1U)) {
            error = make_error(ErrorCode::MalformedRlp,
                               "RLP string exceeds its container", offset);
            return std::nullopt;
        }
        if (length == 1U && input[offset + 1U] < 0x80U) {
            error = make_error(ErrorCode::NonCanonicalRlp,
                               "single byte must use the immediate form",
                               offset);
            return std::nullopt;
        }
        return RlpItem{RlpItem::Kind::String, offset, offset + 1U, length};
    }

    if (prefix <= 0xbfU) {
        const std::size_t length_of_length = prefix - 0xb7U;
        if (length_of_length > limit - (offset + 1U)) {
            error = make_error(ErrorCode::MalformedRlp,
                               "truncated RLP string length", offset);
            return std::nullopt;
        }
        if (input[offset + 1U] == 0) {
            error = make_error(ErrorCode::NonCanonicalRlp,
                               "RLP length has a leading zero", offset);
            return std::nullopt;
        }
        std::uint64_t length = 0;
        for (std::size_t index = 0; index < length_of_length; ++index) {
            if (length > (std::numeric_limits<std::uint64_t>::max() >> 8U)) {
                error = make_error(ErrorCode::IntegerOverflow,
                                   "RLP string length is too large", offset);
                return std::nullopt;
            }
            length = (length << 8U) | input[offset + 1U + index];
        }
        if (length <= 55U) {
            error = make_error(ErrorCode::NonCanonicalRlp,
                               "long RLP form used for a short string", offset);
            return std::nullopt;
        }
        const std::size_t payload_start = offset + 1U + length_of_length;
        if (length > limit - payload_start) {
            error = make_error(ErrorCode::MalformedRlp,
                               "RLP string exceeds its container", offset);
            return std::nullopt;
        }
        return RlpItem{RlpItem::Kind::String, offset, payload_start,
                       static_cast<std::size_t>(length)};
    }

    if (prefix <= 0xf7U) {
        const std::size_t length = prefix - 0xc0U;
        if (length > limit - (offset + 1U)) {
            error = make_error(ErrorCode::MalformedRlp,
                               "RLP list exceeds its container", offset);
            return std::nullopt;
        }
        return RlpItem{RlpItem::Kind::List, offset, offset + 1U, length};
    }

    const std::size_t length_of_length = prefix - 0xf7U;
    if (length_of_length > limit - (offset + 1U)) {
        error = make_error(ErrorCode::MalformedRlp, "truncated RLP list length",
                           offset);
        return std::nullopt;
    }
    if (input[offset + 1U] == 0) {
        error = make_error(ErrorCode::NonCanonicalRlp,
                           "RLP length has a leading zero", offset);
        return std::nullopt;
    }
    std::uint64_t length = 0;
    for (std::size_t index = 0; index < length_of_length; ++index) {
        if (length > (std::numeric_limits<std::uint64_t>::max() >> 8U)) {
            error = make_error(ErrorCode::IntegerOverflow,
                               "RLP list length is too large", offset);
            return std::nullopt;
        }
        length = (length << 8U) | input[offset + 1U + index];
    }
    if (length <= 55U) {
        error = make_error(ErrorCode::NonCanonicalRlp,
                           "long RLP form used for a short list", offset);
        return std::nullopt;
    }
    const std::size_t payload_start = offset + 1U + length_of_length;
    if (length > limit - payload_start) {
        error = make_error(ErrorCode::MalformedRlp,
                           "RLP list exceeds its container", offset);
        return std::nullopt;
    }
    return RlpItem{RlpItem::Kind::List, offset, payload_start,
                   static_cast<std::size_t>(length)};
}

std::optional<std::vector<RlpItem>> rlp_children(ByteView input,
                                                 const RlpItem& list,
                                                 std::size_t depth,
                                                 Error& error) {
    if (list.kind != RlpItem::Kind::List) {
        error = make_error(ErrorCode::MalformedRlp, "RLP item is not a list",
                           list.start);
        return std::nullopt;
    }

    std::vector<RlpItem> children;
    std::size_t offset = list.payload_start;
    while (offset < list.end()) {
        const auto child = decode_rlp_item(input, offset, list.end(), depth,
                                           error);
        if (!child.has_value()) {
            return std::nullopt;
        }
        children.push_back(*child);
        if (children.size() > kMaximumRlpChildren) {
            error = make_error(ErrorCode::MalformedRlp, "too many RLP items",
                               offset);
            return std::nullopt;
        }
        offset = child->end();
    }
    return children;
}

bool require_string(const RlpItem& item, Error& error, const char* name) {
    if (item.kind != RlpItem::Kind::String) {
        error = make_error(ErrorCode::InvalidFieldEncoding,
                           std::string(name) + " must be an RLP string",
                           item.start);
        return false;
    }
    return true;
}

bool parse_uint_checked(ByteView input,
                        const RlpItem& item,
                        std::size_t maximum_bytes,
                        Big& output,
                        Error& error,
                        const char* name) {
    if (!require_string(item, error, name)) {
        return false;
    }
    if (item.payload_size > maximum_bytes) {
        error = make_error(ErrorCode::IntegerOverflow,
                           std::string(name) + " is too large", item.start);
        return false;
    }
    if (item.payload_size == 0) {
        output = Big{};
        return true;
    }
    if (input[item.payload_start] == 0) {
        error = make_error(ErrorCode::NonCanonicalRlp,
                           std::string(name) + " has a leading zero",
                           item.start);
        return false;
    }
    if (item.payload_size > 32U) {
        error = make_error(ErrorCode::IntegerOverflow,
                           std::string(name) + " exceeds 256 bits", item.start);
        return false;
    }

    std::array<std::uint8_t, 32> bytes{};
    std::copy(input.begin() + static_cast<std::ptrdiff_t>(item.payload_start),
              input.begin() + static_cast<std::ptrdiff_t>(item.end()),
              bytes.begin() + static_cast<std::ptrdiff_t>(32 - item.payload_size));
    output = big_from_be(bytes);
    return true;
}

bool parse_u64_checked(ByteView input,
                       const RlpItem& item,
                       std::uint64_t& output,
                       Error& error,
                       const char* name) {
    Big value;
    if (!parse_uint_checked(input, item, 8, value, error, name)) {
        return false;
    }
    if (value.word[1] != 0 || value.word[2] != 0 || value.word[3] != 0) {
        error = make_error(ErrorCode::IntegerOverflow,
                           std::string(name) + " does not fit uint64",
                           item.start);
        return false;
    }
    output = value.word[0];
    return true;
}

bool parse_address(ByteView input,
                   const RlpItem& item,
                   std::optional<Address>& output,
                   Error& error,
                   const char* name) {
    if (!require_string(item, error, name)) {
        return false;
    }
    if (item.payload_size == 0) {
        output.reset();
        return true;
    }
    if (item.payload_size != 20U) {
        error = make_error(ErrorCode::InvalidAddress,
                           std::string(name) + " must be exactly 20 bytes",
                           item.start);
        return false;
    }
    Address address;
    std::copy(input.begin() + static_cast<std::ptrdiff_t>(item.payload_start),
              input.begin() + static_cast<std::ptrdiff_t>(item.end()),
              address.bytes.begin());
    output = address;
    return true;
}

bool parse_data(ByteView input,
                const RlpItem& item,
                ByteView& output,
                std::size_t maximum_bytes,
                Error& error,
                const char* name) {
    if (!require_string(item, error, name)) {
        return false;
    }
    if (item.payload_size > maximum_bytes) {
        error = make_error(ErrorCode::TransactionTooLarge,
                           std::string(name) + " exceeds the policy limit",
                           item.start);
        return false;
    }
    output = input.subspan(item.payload_start, item.payload_size);
    return true;
}

Bytes rlp_encode_length(std::size_t length,
                        std::uint8_t short_base,
                        std::uint8_t long_base) {
    Bytes output;
    if (length <= 55U) {
        output.push_back(static_cast<std::uint8_t>(short_base + length));
        return output;
    }

    std::array<std::uint8_t, 8> encoded{};
    std::size_t count = 0;
    std::size_t remaining = length;
    while (remaining != 0) {
        encoded[encoded.size() - 1U - count] =
            static_cast<std::uint8_t>(remaining & 0xffU);
        remaining >>= 8U;
        ++count;
    }
    output.push_back(static_cast<std::uint8_t>(long_base + count));
    output.insert(output.end(), encoded.end() - static_cast<std::ptrdiff_t>(count),
                  encoded.end());
    return output;
}

Bytes rlp_encode_string(ByteView value) {
    Bytes output;
    if (value.size() == 1U && value[0] < 0x80U) {
        output.push_back(value[0]);
        return output;
    }
    Bytes header = rlp_encode_length(value.size(), 0x80U, 0xb7U);
    output.reserve(header.size() + value.size());
    output.insert(output.end(), header.begin(), header.end());
    output.insert(output.end(), value.begin(), value.end());
    return output;
}

Bytes rlp_encode_uint(const Big& value) {
    const auto bytes = big_to_be(value);
    std::size_t first = 0;
    while (first < bytes.size() && bytes[first] == 0) {
        ++first;
    }
    return rlp_encode_string(ByteView(bytes).subspan(first));
}

Bytes rlp_encode_list_payload(const std::vector<Bytes>& items) {
    std::size_t payload_size = 0;
    for (const Bytes& item : items) {
        payload_size += item.size();
    }
    Bytes output = rlp_encode_length(payload_size, 0xc0U, 0xf7U);
    output.reserve(output.size() + payload_size);
    for (const Bytes& item : items) {
        output.insert(output.end(), item.begin(), item.end());
    }
    return output;
}

Bytes raw_item(ByteView input, const RlpItem& item) {
    const auto begin = input.begin() + static_cast<std::ptrdiff_t>(item.start);
    const auto finish = input.begin() + static_cast<std::ptrdiff_t>(item.end());
    return Bytes(begin, finish);
}

// ---------------------------------------------------------------------------
// Transaction model / parser
// ---------------------------------------------------------------------------

enum class TxType : std::uint8_t {
    Legacy = 0x00,
    AccessList = 0x01,
    DynamicFee = 0x02,
};

struct AccessListEntry {
    Address address;
    std::vector<std::array<std::uint8_t, 32>> storage_keys;
};

struct ParsedTransaction {
    TxType type = TxType::Legacy;
    bool signed_input = false;
    bool unprotected_legacy = false;
    std::uint64_t chain_id = 0;
    Big nonce;
    Big gas_limit;
    Big gas_price;
    Big max_priority_fee_per_gas;
    Big max_fee_per_gas;
    std::optional<Address> to;
    Big value;
    ByteView data;
    std::size_t data_offset = 0;
    std::vector<AccessListEntry> access_list;
    std::size_t access_list_entries = 0;
    Hash256 access_list_hash{};
    bool has_access_list = false;
    Hash256 signing_digest{};
    std::optional<CompactSignature> signature;
    std::optional<Address> recovered_sender;
};

std::optional<ParsedTransaction> parse_legacy(ByteView input,
                                              const RlpItem& list,
                                              const TransactionPolicy& policy,
                                              Error& error) {
    auto children = rlp_children(input, list, 0, error);
    if (!children.has_value()) {
        return std::nullopt;
    }
    if (children->size() != 6U && children->size() != 9U) {
        error = make_error(ErrorCode::UnexpectedFieldCount,
                           "legacy transaction must have 6 or 9 fields",
                           list.start);
        return std::nullopt;
    }

    ParsedTransaction parsed;
    parsed.type = TxType::Legacy;

    if (!parse_uint_checked(input, (*children)[0], 32, parsed.nonce, error,
                            "nonce") ||
        !parse_uint_checked(input, (*children)[1], 32, parsed.gas_price, error,
                            "gasPrice") ||
        !parse_uint_checked(input, (*children)[2], 32, parsed.gas_limit, error,
                            "gasLimit") ||
        !parse_address(input, (*children)[3], parsed.to, error, "to") ||
        !parse_uint_checked(input, (*children)[4], 32, parsed.value, error,
                            "value") ||
        !parse_data(input, (*children)[5], parsed.data,
                    policy.max_calldata_bytes, error, "data")) {
        return std::nullopt;
    }
    parsed.data_offset = (*children)[5].start;

    std::vector<Bytes> signing_fields;
    signing_fields.reserve(9);
    for (std::size_t index = 0; index < 6U; ++index) {
        signing_fields.push_back(raw_item(input, (*children)[index]));
    }

    if (children->size() == 6U) {
        // A bare legacy transaction has no chain field. The device must inject
        // its own policy chain into the EIP-155 signing preimage; the host is
        // never allowed to nominate the chain.
        parsed.chain_id = policy.chain_id;
        signing_fields.push_back(rlp_encode_uint(Big{{policy.chain_id, 0, 0, 0}}));
        signing_fields.push_back(Bytes{0x80});
        signing_fields.push_back(Bytes{0x80});
        parsed.signing_digest =
            detail::keccak256(rlp_encode_list_payload(signing_fields));
        return parsed;
    }

    Big v;
    Big r;
    Big s;
    if (!parse_uint_checked(input, (*children)[6], 8, v, error, "v") ||
        !parse_uint_checked(input, (*children)[7], 32, r, error, "r") ||
        !parse_uint_checked(input, (*children)[8], 32, s, error, "s")) {
        return std::nullopt;
    }

    if (big_zero(r) && big_zero(s)) {
        // Unsigned EIP-155 form: [...., chainId, 0, 0].
        std::uint64_t chain_id = 0;
        if (!parse_u64_checked(input, (*children)[6], chain_id, error,
                               "chainId")) {
            return std::nullopt;
        }
        if (chain_id == 0) {
            error = make_error(ErrorCode::InvalidChainId, "chainId is zero",
                               (*children)[6].start);
            return std::nullopt;
        }
        if (chain_id != policy.chain_id) {
            error = make_error(ErrorCode::ChainMismatch,
                               "transaction chain does not match device policy",
                               (*children)[6].start);
            return std::nullopt;
        }
        parsed.chain_id = chain_id;
        signing_fields.push_back(rlp_encode_uint(Big{{chain_id, 0, 0, 0}}));
        signing_fields.push_back(Bytes{0x80});
        signing_fields.push_back(Bytes{0x80});
        parsed.signing_digest =
            detail::keccak256(rlp_encode_list_payload(signing_fields));
        return parsed;
    }

    if (v.word[1] != 0 || v.word[2] != 0 || v.word[3] != 0) {
        error = make_error(ErrorCode::IntegerOverflow, "v is too large",
                           (*children)[6].start);
        return std::nullopt;
    }
    const std::uint64_t v_value = v.word[0];
    std::uint8_t recovery_id = 0;

    if (v_value == 27U || v_value == 28U) {
        if (!policy.allow_unprotected_legacy) {
            error = make_error(ErrorCode::UnprotectedLegacyTransaction,
                               "pre-EIP-155 transactions do not bind a chain",
                               (*children)[6].start);
            return std::nullopt;
        }
        parsed.unprotected_legacy = true;
        parsed.chain_id = policy.chain_id;
        recovery_id = static_cast<std::uint8_t>(v_value - 27U);
        parsed.signing_digest =
            detail::keccak256(rlp_encode_list_payload(signing_fields));
    } else if (v_value >= 35U) {
        const std::uint64_t chain_id = (v_value - 35U) / 2U;
        recovery_id = static_cast<std::uint8_t>((v_value - 35U) % 2U);
        if (chain_id == 0) {
            error = make_error(ErrorCode::InvalidChainId, "chainId is zero",
                               (*children)[6].start);
            return std::nullopt;
        }
        if (chain_id != policy.chain_id) {
            error = make_error(ErrorCode::ChainMismatch,
                               "signature chain does not match device policy",
                               (*children)[6].start);
            return std::nullopt;
        }
        parsed.chain_id = chain_id;
        signing_fields.push_back(rlp_encode_uint(Big{{chain_id, 0, 0, 0}}));
        signing_fields.push_back(Bytes{0x80});
        signing_fields.push_back(Bytes{0x80});
        parsed.signing_digest =
            detail::keccak256(rlp_encode_list_payload(signing_fields));
    } else {
        error = make_error(ErrorCode::InvalidSignatureEncoding,
                           "legacy v is not a chain-bound recovery id",
                           (*children)[6].start);
        return std::nullopt;
    }

    CompactSignature signature;
    signature.recovery_id = recovery_id;
    const auto r_bytes = big_to_be(r);
    const auto s_bytes = big_to_be(s);
    std::copy(r_bytes.begin(), r_bytes.end(), signature.bytes.begin());
    std::copy(s_bytes.begin(), s_bytes.end(), signature.bytes.begin() + 32);

    if (!detail::scalar_is_valid(r_bytes) || !detail::scalar_is_valid(s_bytes)) {
        error = make_error(ErrorCode::InvalidSignature,
                           "signature scalar is out of range",
                           (*children)[7].start);
        return std::nullopt;
    }
    if (policy.require_low_s_signature &&
        !detail::scalar_is_low_s(s_bytes)) {
        error = make_error(ErrorCode::InvalidSignature,
                           "high-s signature is not allowed",
                           (*children)[8].start);
        return std::nullopt;
    }

    const auto sender = detail::recover_address(
        parsed.signing_digest, signature, policy.require_low_s_signature);
    if (!sender.has_value()) {
        error = make_error(ErrorCode::InvalidSignature,
                           "signature does not recover to a valid key",
                           (*children)[7].start);
        return std::nullopt;
    }
    if (policy.expected_signer.has_value() &&
        *policy.expected_signer != *sender) {
        error = make_error(ErrorCode::SignatureMismatch,
                           "signature is not from the device policy signer",
                           (*children)[7].start);
        return std::nullopt;
    }

    parsed.signed_input = true;
    parsed.signature = signature;
    parsed.recovered_sender = *sender;
    return parsed;
}

std::optional<std::vector<AccessListEntry>> parse_access_list(
    ByteView input,
    const RlpItem& item,
    Hash256& digest,
    Error& error) {
    if (item.kind != RlpItem::Kind::List) {
        error = make_error(ErrorCode::InvalidAccessList,
                           "accessList must be an RLP list", item.start);
        return std::nullopt;
    }
    auto entries = rlp_children(input, item, 1, error);
    if (!entries.has_value()) {
        return std::nullopt;
    }

    std::vector<AccessListEntry> output;
    output.reserve(entries->size());
    for (const RlpItem& entry : *entries) {
        if (entry.kind != RlpItem::Kind::List) {
            error = make_error(ErrorCode::InvalidAccessList,
                               "access list entry must be a list",
                               entry.start);
            return std::nullopt;
        }
        auto fields = rlp_children(input, entry, 2, error);
        if (!fields.has_value()) {
            return std::nullopt;
        }
        if (fields->size() != 2U) {
            error = make_error(ErrorCode::InvalidAccessList,
                               "access list entry must have two fields",
                               entry.start);
            return std::nullopt;
        }

        std::optional<Address> address;
        if (!parse_address(input, (*fields)[0], address, error,
                           "accessList.address") ||
            !address.has_value()) {
            if (!error) {
                error = make_error(ErrorCode::InvalidAccessList,
                                   "access list address must be 20 bytes",
                                   (*fields)[0].start);
            }
            return std::nullopt;
        }

        auto keys = rlp_children(input, (*fields)[1], 3, error);
        if (!keys.has_value()) {
            return std::nullopt;
        }
        if (keys->size() > 4096U) {
            error = make_error(ErrorCode::InvalidAccessList,
                               "access list entry has too many storage keys",
                               (*fields)[1].start);
            return std::nullopt;
        }

        AccessListEntry parsed_entry;
        parsed_entry.address = *address;
        parsed_entry.storage_keys.reserve(keys->size());
        for (const RlpItem& key : *keys) {
            if (key.kind != RlpItem::Kind::String) {
                error = make_error(ErrorCode::InvalidAccessList,
                                   "storage key must be an RLP string",
                                   key.start);
                return std::nullopt;
            }
            if (key.payload_size != 32U) {
                error = make_error(ErrorCode::InvalidAccessList,
                                   "storage key must be exactly 32 bytes",
                                   key.start);
                return std::nullopt;
            }
            std::array<std::uint8_t, 32> decoded{};
            std::copy(input.begin() +
                          static_cast<std::ptrdiff_t>(key.payload_start),
                      input.begin() + static_cast<std::ptrdiff_t>(key.end()),
                      decoded.begin());
            parsed_entry.storage_keys.push_back(decoded);
        }
        output.push_back(std::move(parsed_entry));
    }

    digest = detail::keccak256(
        input.subspan(item.start, item.end() - item.start));
    return output;
}

std::optional<ParsedTransaction> parse_typed(ByteView input,
                                             TxType type,
                                             const TransactionPolicy& policy,
                                             Error& error) {
    Error local_error;
    const auto decoded = decode_rlp_item(input, 1, input.size(), 0, local_error);
    if (!decoded.has_value()) {
        error = local_error;
        return std::nullopt;
    }
    if (decoded->kind != RlpItem::Kind::List) {
        error = make_error(ErrorCode::MalformedRlp,
                           "typed transaction payload must be an RLP list", 1);
        return std::nullopt;
    }
    if (decoded->end() != input.size()) {
        error = make_error(ErrorCode::MalformedRlp,
                           "trailing bytes after typed transaction", decoded->end());
        return std::nullopt;
    }

    auto children = rlp_children(input, *decoded, 0, error);
    if (!children.has_value()) {
        return std::nullopt;
    }

    const std::size_t base_fields = type == TxType::AccessList ? 8U : 9U;
    const bool signed_input =
        children->size() == base_fields + 3U;
    if (children->size() != base_fields && !signed_input) {
        error = make_error(ErrorCode::UnexpectedFieldCount,
                           "typed transaction has the wrong field count",
                           decoded->start);
        return std::nullopt;
    }

    ParsedTransaction parsed;
    parsed.type = type;
    parsed.signed_input = signed_input;

    std::uint64_t chain_id = 0;
    if (!parse_u64_checked(input, (*children)[0], chain_id, error, "chainId")) {
        return std::nullopt;
    }
    if (chain_id == 0) {
        error = make_error(ErrorCode::InvalidChainId, "chainId is zero",
                           (*children)[0].start);
        return std::nullopt;
    }
    if (chain_id != policy.chain_id) {
        error = make_error(ErrorCode::ChainMismatch,
                           "transaction chain does not match device policy",
                           (*children)[0].start);
        return std::nullopt;
    }
    parsed.chain_id = chain_id;

    std::size_t data_index = 0;
    std::size_t access_list_index = 0;

    if (type == TxType::AccessList) {
        if (!parse_uint_checked(input, (*children)[1], 32, parsed.nonce, error,
                                "nonce") ||
            !parse_uint_checked(input, (*children)[2], 32, parsed.gas_price,
                                error, "gasPrice") ||
            !parse_uint_checked(input, (*children)[3], 32, parsed.gas_limit,
                                error, "gasLimit") ||
            !parse_address(input, (*children)[4], parsed.to, error, "to") ||
            !parse_uint_checked(input, (*children)[5], 32, parsed.value, error,
                                "value") ||
            !parse_data(input, (*children)[6], parsed.data,
                        policy.max_calldata_bytes, error, "data")) {
            return std::nullopt;
        }
        data_index = 6U;
        access_list_index = 7U;
    } else {
        if (!parse_uint_checked(input, (*children)[1], 32, parsed.nonce, error,
                                "nonce") ||
            !parse_uint_checked(input, (*children)[2], 32,
                                parsed.max_priority_fee_per_gas, error,
                                "maxPriorityFeePerGas") ||
            !parse_uint_checked(input, (*children)[3], 32,
                                parsed.max_fee_per_gas, error,
                                "maxFeePerGas") ||
            !parse_uint_checked(input, (*children)[4], 32, parsed.gas_limit,
                                error, "gasLimit") ||
            !parse_address(input, (*children)[5], parsed.to, error, "to") ||
            !parse_uint_checked(input, (*children)[6], 32, parsed.value, error,
                                "value") ||
            !parse_data(input, (*children)[7], parsed.data,
                        policy.max_calldata_bytes, error, "data")) {
            return std::nullopt;
        }
        data_index = 7U;
        access_list_index = 8U;

        if (big_compare(parsed.max_priority_fee_per_gas,
                        parsed.max_fee_per_gas) > 0) {
            error = make_error(ErrorCode::PriorityFeeExceedsMaxFee,
                               "maxPriorityFeePerGas exceeds maxFeePerGas",
                               (*children)[2].start);
            return std::nullopt;
        }
    }
    parsed.data_offset = (*children)[data_index].start;

    std::optional<std::vector<AccessListEntry>> access_list =
        parse_access_list(input, (*children)[access_list_index],
                          parsed.access_list_hash, error);
    if (!access_list.has_value()) {
        return std::nullopt;
    }
    parsed.access_list = std::move(*access_list);
    parsed.access_list_entries = parsed.access_list.size();
    parsed.has_access_list = true;

    std::vector<Bytes> base_items;
    base_items.reserve(base_fields);
    for (std::size_t index = 0; index < base_fields; ++index) {
        base_items.push_back(raw_item(input, (*children)[index]));
    }
    Bytes preimage;
    preimage.push_back(static_cast<std::uint8_t>(type));
    const Bytes encoded_base = rlp_encode_list_payload(base_items);
    preimage.insert(preimage.end(), encoded_base.begin(), encoded_base.end());
    parsed.signing_digest = detail::keccak256(preimage);

    if (!signed_input) {
        return parsed;
    }

    std::uint64_t y_parity = 0;
    Big r;
    Big s;
    if (!parse_u64_checked(input, (*children)[base_fields], y_parity, error,
                           "yParity") ||
        !parse_uint_checked(input, (*children)[base_fields + 1U], 32, r, error,
                            "r") ||
        !parse_uint_checked(input, (*children)[base_fields + 2U], 32, s, error,
                            "s")) {
        return std::nullopt;
    }
    if (y_parity > 1U) {
        error = make_error(ErrorCode::InvalidSignatureEncoding,
                           "yParity must be zero or one",
                           (*children)[base_fields].start);
        return std::nullopt;
    }

    CompactSignature signature;
    signature.recovery_id = static_cast<std::uint8_t>(y_parity);
    const auto r_bytes = big_to_be(r);
    const auto s_bytes = big_to_be(s);
    std::copy(r_bytes.begin(), r_bytes.end(), signature.bytes.begin());
    std::copy(s_bytes.begin(), s_bytes.end(), signature.bytes.begin() + 32);

    if (!detail::scalar_is_valid(r_bytes) || !detail::scalar_is_valid(s_bytes)) {
        error = make_error(ErrorCode::InvalidSignature,
                           "signature scalar is out of range",
                           (*children)[base_fields + 1U].start);
        return std::nullopt;
    }
    if (policy.require_low_s_signature &&
        !detail::scalar_is_low_s(s_bytes)) {
        error = make_error(ErrorCode::InvalidSignature,
                           "high-s signature is not allowed",
                           (*children)[base_fields + 2U].start);
        return std::nullopt;
    }

    const auto sender = detail::recover_address(
        parsed.signing_digest, signature, policy.require_low_s_signature);
    if (!sender.has_value()) {
        error = make_error(ErrorCode::InvalidSignature,
                           "signature does not recover to a valid key",
                           (*children)[base_fields + 1U].start);
        return std::nullopt;
    }
    if (policy.expected_signer.has_value() &&
        *policy.expected_signer != *sender) {
        error = make_error(ErrorCode::SignatureMismatch,
                           "signature is not from the device policy signer",
                           (*children)[base_fields + 1U].start);
        return std::nullopt;
    }

    parsed.signature = signature;
    parsed.recovered_sender = *sender;
    return parsed;
}

std::optional<ParsedTransaction> parse_transaction(
    ByteView input,
    const TransactionPolicy& policy,
    Error& error) {
    if (input.empty()) {
        error = make_error(ErrorCode::EmptyTransaction, "empty transaction");
        return std::nullopt;
    }
    if (input.size() > policy.max_transaction_bytes) {
        error = make_error(ErrorCode::TransactionTooLarge,
                           "transaction exceeds the policy size limit");
        return std::nullopt;
    }

    if (input[0] == 0x01U || input[0] == 0x02U) {
        return parse_typed(input,
                           input[0] == 0x01U ? TxType::AccessList
                                             : TxType::DynamicFee,
                           policy, error);
    }
    if (input[0] == 0x03U || input[0] == 0x04U) {
        error = make_error(ErrorCode::UnsupportedTransactionType,
                           "blob/account-abstraction transactions are not "
                           "supported by this firmware policy");
        return std::nullopt;
    }
    if (input[0] < 0xc0U) {
        error = make_error(ErrorCode::UnsupportedTransactionType,
                           "unknown typed transaction envelope", 0);
        return std::nullopt;
    }

    const auto list = decode_rlp_item(input, 0, input.size(), 0, error);
    if (!list.has_value()) {
        return std::nullopt;
    }
    if (list->kind != RlpItem::Kind::List) {
        error = make_error(ErrorCode::MalformedRlp,
                           "legacy transaction must be an RLP list", 0);
        return std::nullopt;
    }
    if (list->end() != input.size()) {
        error = make_error(ErrorCode::MalformedRlp,
                           "trailing bytes after legacy transaction",
                           list->end());
        return std::nullopt;
    }
    return parse_legacy(input, *list, policy, error);
}

// ---------------------------------------------------------------------------
// Display construction
// ---------------------------------------------------------------------------

struct DisplayBuildResult {
    DisplayContent display;
    bool unknown_contract_data = false;
};

std::string type_name(TxType type) {
    switch (type) {
        case TxType::Legacy:
            return "Legacy (0x00)";
        case TxType::AccessList:
            return "EIP-2930 (0x01)";
        case TxType::DynamicFee:
            return "EIP-1559 (0x02)";
    }
    return "Unknown";
}

std::string selector_text(ByteView data) {
    if (data.empty()) {
        return "(none)";
    }
    const std::size_t size = std::min<std::size_t>(data.size(), 4U);
    return hex_encode(data.first(size));
}

bool is_erc20_transfer_selector(ByteView data) {
    if (data.size() < 4U) {
        return false;
    }
    const std::uint8_t selector[4] = {0xa9U, 0x05U, 0x9cU, 0xbbU};
    return std::equal(std::begin(selector), std::end(selector), data.begin());
}

std::string base_units_suffix(bool native) {
    return native ? "wei" : "base units";
}

void append_field(DisplayContent& display,
                  std::string label,
                  std::string value) {
    display.fields.push_back(DisplayField{std::move(label), std::move(value)});
}

void append_warning(DisplayContent& display, std::string message) {
    append_field(display, "Warning", std::move(message));
}

std::optional<DisplayBuildResult> build_display(
    const ParsedTransaction& transaction,
    const TransactionPolicy& policy,
    Error& error) {
    DisplayContent display;
    display.kind = DisplayKind::NativeTransfer;
    display.chain_id = transaction.chain_id;
    display.chain_name = policy.chain_name;
    display.transaction_type = type_name(transaction.type);
    display.input_was_signed = transaction.signed_input;
    display.transaction_to = transaction.to;
    display.gas_limit = big_decimal(transaction.gas_limit);
    display.nonce = big_decimal(transaction.nonce);
    display.calldata_selector = selector_text(transaction.data);
    display.calldata_hash = hex_encode(detail::keccak256(transaction.data).bytes);
    display.access_list_entries = transaction.access_list_entries;
    display.access_list_hash =
        transaction.has_access_list
            ? hex_encode(transaction.access_list_hash.bytes)
            : "not applicable";

    const Big* price = nullptr;
    if (transaction.type == TxType::DynamicFee) {
        price = &transaction.max_fee_per_gas;
        display.gas_price = big_decimal(transaction.max_fee_per_gas);
        display.max_priority_fee_per_gas =
            big_decimal(transaction.max_priority_fee_per_gas);
    } else {
        price = &transaction.gas_price;
        display.gas_price = big_decimal(transaction.gas_price);
    }

    Big maximum_fee;
    if (!big_multiply_checked(transaction.gas_limit, *price, maximum_fee)) {
        error = make_error(ErrorCode::IntegerOverflow,
                           "gas limit multiplied by gas price overflows 256 bits");
        return std::nullopt;
    }
    if (policy.max_fee.has_value()) {
        const Big policy_limit =
            big_from_be(policy.max_fee->big_endian);
        if (big_compare(maximum_fee, policy_limit) > 0) {
            error = make_error(ErrorCode::FeeLimitExceeded,
                               "maximum network fee exceeds device policy");
            return std::nullopt;
        }
    }
    display.max_fee_base_units = big_decimal(maximum_fee);
    display.max_fee_display =
        format_units(maximum_fee, policy.native_decimals,
                     policy.native_symbol);

    bool native_amount = true;
    Big amount = transaction.value;
    std::string symbol = policy.native_symbol;
    std::uint8_t decimals = policy.native_decimals;
    const TokenPolicy* token = nullptr;

    if (transaction.to.has_value()) {
        for (const TokenPolicy& candidate : policy.tokens) {
            if (candidate.contract == *transaction.to) {
                token = &candidate;
                break;
            }
        }
    }

    if (!transaction.to.has_value()) {
        display.kind = DisplayKind::ContractCreation;
        if (!policy.allow_contract_creation) {
            error = make_error(ErrorCode::ContractCreationDisallowed,
                               "contract creation is disabled by device policy",
                               transaction.data_offset);
            return std::nullopt;
        }
        display.unknown_contract_data = false;
    } else if (transaction.data.empty()) {
        display.kind = DisplayKind::NativeTransfer;
        display.recipient = transaction.to;
    } else if (token != nullptr) {
        if (transaction.data.size() != 68U) {
            error = make_error(
                ErrorCode::InvalidContractData,
                "ERC-20 transfer calldata must be exactly 68 bytes",
                transaction.data_offset);
            return std::nullopt;
        }
        const std::uint8_t selector[4] = {0xa9U, 0x05U, 0x9cU, 0xbbU};
        if (!std::equal(std::begin(selector), std::end(selector),
                        transaction.data.begin())) {
            error = make_error(ErrorCode::UnknownContractCall,
                               "selected token function is not approved",
                               transaction.data_offset);
            return std::nullopt;
        }

        const ByteView address_word = transaction.data.subspan(4, 32);
        for (std::size_t index = 0; index < 12U; ++index) {
            if (address_word[index] != 0) {
                error = make_error(
                    ErrorCode::InvalidContractData,
                    "ERC-20 recipient is not a canonical ABI address word",
                    transaction.data_offset + 4U + index);
                return std::nullopt;
            }
        }

        display.kind = DisplayKind::TokenTransfer;
        display.token_contract = *transaction.to;
        Address recipient;
        std::copy(address_word.begin() + 12, address_word.end(),
                  recipient.bytes.begin());
        display.recipient = recipient;

        const auto amount_bytes = transaction.data.subspan(36, 32);
        std::array<std::uint8_t, 32> amount_array{};
        std::copy(amount_bytes.begin(), amount_bytes.end(),
                  amount_array.begin());
        amount = big_from_be(amount_array);
        if (token->max_transfer.has_value()) {
            const Big limit = big_from_be(token->max_transfer->big_endian);
            if (big_compare(amount, limit) > 0) {
                error = make_error(
                    ErrorCode::InvalidContractData,
                    "token amount exceeds the device token policy limit",
                    transaction.data_offset + 36U);
                return std::nullopt;
            }
        }
        native_amount = false;
        symbol = token->symbol;
        decimals = token->decimals;
    } else {
        if (transaction.data.size() < 4U) {
            error = make_error(ErrorCode::InvalidContractData,
                               "contract calldata lacks a selector",
                               transaction.data_offset);
            return std::nullopt;
        }
        if (is_erc20_transfer_selector(transaction.data)) {
            // The calldata claims to be an ERC-20 transfer but the contract is
            // not in the firmware-owned token list. Never render it as a
            // generic call: an untrusted contract could implement transfer()
            // with different semantics.
            error = make_error(
                ErrorCode::UntrustedContract,
                "ERC-20 transfer target is not a registered token contract",
                transaction.data_offset);
            return std::nullopt;
        }
        if (!policy.allow_unknown_contract_calls) {
            error = make_error(
                ErrorCode::UnknownContractCall,
                "contract call is not covered by a reviewed token policy",
                transaction.data_offset);
            return std::nullopt;
        }
        display.kind = DisplayKind::ContractCall;
        display.unknown_contract_data = true;
    }

    display.asset_symbol = symbol;
    display.asset_decimals = decimals;
    display.amount_base_units = big_decimal(amount);
    display.amount_display = format_units(amount, decimals, symbol);
    display.native_value_base_units = big_decimal(transaction.value);
    display.native_value_display =
        format_units(transaction.value, policy.native_decimals,
                     policy.native_symbol);

    append_field(display, "Chain",
                 policy.chain_name + " (" + std::to_string(transaction.chain_id) +
                     ")");
    append_field(display, "Type", display.transaction_type);
    append_field(display, "Status",
                 transaction.signed_input ? "SIGNED" : "UNSIGNED");
    if (transaction.signed_input && transaction.recovered_sender.has_value()) {
        append_field(display, "Sender (recovered)",
                     transaction.recovered_sender->checksum_hex());
    }

    switch (display.kind) {
        case DisplayKind::NativeTransfer:
            append_field(display, "Recipient",
                         transaction.to->checksum_hex());
            break;
        case DisplayKind::TokenTransfer:
            append_field(display, "To (token contract)",
                         transaction.to->checksum_hex());
            append_field(display, "Recipient",
                         display.recipient->checksum_hex());
            append_field(display, "Token", symbol);
            break;
        case DisplayKind::ContractCall:
            append_field(display, "To (contract)",
                         transaction.to->checksum_hex());
            append_warning(display, "unverified contract data");
            break;
        case DisplayKind::ContractCreation:
            append_field(display, "To", "(contract creation)");
            append_warning(display, "contract creation");
            break;
    }

    append_field(display, native_amount ? "Value" : "Amount",
                 display.amount_display + " (" + display.amount_base_units +
                     " " + base_units_suffix(native_amount) + ")");
    if (!native_amount) {
        append_field(display, "Native value",
                     display.native_value_display + " (" +
                         display.native_value_base_units + " wei)");
    }
    append_field(display, "Max network fee",
                 display.max_fee_display + " (" +
                     display.max_fee_base_units + " wei)");
    append_field(display, "Gas limit", display.gas_limit);
    append_field(display,
                 transaction.type == TxType::DynamicFee ? "Max fee per gas"
                                                        : "Gas price",
                 display.gas_price + " wei");
    if (transaction.type == TxType::DynamicFee) {
        append_field(display, "Max priority fee per gas",
                     display.max_priority_fee_per_gas + " wei");
    }
    append_field(display, "Nonce", display.nonce);
    append_field(display, "Calldata selector", display.calldata_selector);
    append_field(display, "Calldata hash", display.calldata_hash);
    append_field(display, "Access list entries",
                 std::to_string(display.access_list_entries));
    if (transaction.has_access_list) {
        append_field(display, "Access list hash", display.access_list_hash);
    }

    if (transaction.unprotected_legacy) {
        append_warning(display,
                       "legacy signature does not bind a chain identifier");
    }
    if (display.kind == DisplayKind::TokenTransfer &&
        display.recipient->is_zero()) {
        append_warning(display, "recipient is the zero address");
    }
    if (display.kind == DisplayKind::NativeTransfer &&
        display.recipient->is_zero()) {
        append_warning(display, "recipient is the zero address");
    }

    append_field(display, "Signing digest",
                 hex_encode(transaction.signing_digest.bytes));

    std::string text = "NETSEC TRANSACTION CONFIRMATION v1";
    for (const DisplayField& field : display.fields) {
        text.push_back('\n');
        text += field.label;
        text += ": ";
        text += field.value;
    }
    display.text = std::move(text);

    DisplayBuildResult result;
    result.display = std::move(display);
    result.unknown_contract_data = result.display.unknown_contract_data;
    return result;
}

std::optional<Error> validate_policy(const TransactionPolicy& policy) {
    if (policy.chain_id == 0) {
        return make_error(ErrorCode::InvalidArgument, "policy chainId is zero");
    }
    if (policy.chain_name.empty() || policy.chain_name.size() > 64U ||
        !is_printable_ascii(policy.chain_name)) {
        return make_error(ErrorCode::InvalidArgument,
                          "policy chain name must be short printable ASCII");
    }
    if (policy.native_symbol.empty() || policy.native_symbol.size() > 32U ||
        !is_printable_ascii(policy.native_symbol)) {
        return make_error(ErrorCode::InvalidArgument,
                          "native symbol must be short printable ASCII");
    }
    if (policy.native_decimals > 36U) {
        return make_error(ErrorCode::InvalidArgument,
                          "native decimals exceed 36");
    }
    if (policy.max_transaction_bytes == 0 ||
        policy.max_transaction_bytes > 1024U * 1024U) {
        return make_error(ErrorCode::InvalidArgument,
                          "invalid maximum transaction size");
    }
    if (policy.max_calldata_bytes > policy.max_transaction_bytes) {
        return make_error(ErrorCode::InvalidArgument,
                          "calldata limit exceeds transaction limit");
    }
    if (policy.expected_signer.has_value() &&
        policy.expected_signer->is_zero()) {
        return make_error(ErrorCode::InvalidArgument,
                          "expected signer is the zero address");
    }

    for (std::size_t index = 0; index < policy.tokens.size(); ++index) {
        const TokenPolicy& token = policy.tokens[index];
        if (token.contract.is_zero()) {
            return make_error(ErrorCode::InvalidArgument,
                              "token contract is the zero address");
        }
        if (token.symbol.empty() || token.symbol.size() > 32U ||
            !is_printable_ascii(token.symbol)) {
            return make_error(ErrorCode::InvalidArgument,
                              "token symbol must be short printable ASCII");
        }
        if (token.decimals > 36U) {
            return make_error(ErrorCode::InvalidArgument,
                              "token decimals exceed 36");
        }
        for (std::size_t other = 0; other < index; ++other) {
            if (policy.tokens[other].contract == token.contract) {
                return make_error(ErrorCode::InvalidArgument,
                                  "duplicate token contract in policy");
            }
        }
    }
    return std::nullopt;
}

}  // namespace

// ---------------------------------------------------------------------------
// Public value types
// ---------------------------------------------------------------------------

std::string Hash256::hex() const {
    return hex_encode(bytes);
}

std::optional<Hash256> Hash256::from_hex(std::string_view text) {
    const std::string_view digits = strip_hex_prefix(text);
    if (digits.size() != 64U) {
        return std::nullopt;
    }
    Hash256 output;
    for (std::size_t index = 0; index < 32U; ++index) {
        const int high = hex_value(digits[index * 2U]);
        const int low = hex_value(digits[index * 2U + 1U]);
        if (high < 0 || low < 0) {
            return std::nullopt;
        }
        output.bytes[index] =
            static_cast<std::uint8_t>((high << 4) | low);
    }
    return output;
}

bool Uint256::is_zero() const noexcept {
    return std::all_of(big_endian.begin(), big_endian.end(),
                       [](std::uint8_t byte) { return byte == 0; });
}

std::string Uint256::decimal() const {
    return big_decimal(big_from_be(big_endian));
}

std::optional<Uint256> Uint256::from_decimal(std::string_view text) {
    if (text.empty()) {
        return std::nullopt;
    }
    Big value;
    for (const char character : text) {
        if (character < '0' || character > '9') {
            return std::nullopt;
        }
        std::uint64_t carry = static_cast<std::uint64_t>(character - '0');
        for (unsigned index = 0; index < 4; ++index) {
            std::uint64_t high = 0;
            std::uint64_t low = 0;
            multiply_64x64(value.word[index], 10U, high, low);
            const std::uint64_t carry_out = add_limbs(low, carry, 0, low);
            value.word[index] = low;
            carry = high + carry_out;
        }
        if (carry != 0) {
            return std::nullopt;
        }
    }
    Uint256 output;
    output.big_endian = big_to_be(value);
    return output;
}

Uint256 Uint256::from_u64(std::uint64_t value) {
    Uint256 output;
    output.big_endian = big_to_be(Big{{value, 0, 0, 0}});
    return output;
}

std::uint64_t Uint256::to_u64() const {
    const Big value = big_from_be(big_endian);
    if (value.word[1] != 0 || value.word[2] != 0 || value.word[3] != 0) {
        return 0;
    }
    return value.word[0];
}

std::optional<Address> Address::from_hex(std::string_view text) {
    const std::string_view digits = strip_hex_prefix(text);
    if (digits.size() != 40U) {
        return std::nullopt;
    }

    Address output;
    bool has_lower = false;
    bool has_upper = false;
    for (std::size_t index = 0; index < 20U; ++index) {
        const char high_char = digits[index * 2U];
        const char low_char = digits[index * 2U + 1U];
        const int high = hex_value(high_char);
        const int low = hex_value(low_char);
        if (high < 0 || low < 0) {
            return std::nullopt;
        }
        output.bytes[index] =
            static_cast<std::uint8_t>((high << 4) | low);

        for (const char character : {high_char, low_char}) {
            if (character >= 'a' && character <= 'f') {
                has_lower = true;
            } else if (character >= 'A' && character <= 'F') {
                has_upper = true;
            }
        }
    }

    if (has_lower && has_upper) {
        const std::string checksummed = output.checksum_hex();
        if (checksummed.size() != digits.size() + 2U ||
            checksummed.substr(2) != digits) {
            return std::nullopt;
        }
    }
    return output;
}

std::string Address::checksum_hex() const {
    const std::string lowercase = lowercase_hex().substr(2);
    const Hash256 hash = detail::keccak256(
        ByteView(reinterpret_cast<const std::uint8_t*>(lowercase.data()),
                 lowercase.size()));

    std::string output = "0x";
    output.reserve(42);
    for (std::size_t index = 0; index < lowercase.size(); ++index) {
        const std::uint8_t nibble =
            (index % 2U == 0U) ? static_cast<std::uint8_t>(hash.bytes[index / 2U] >> 4U)
                               : static_cast<std::uint8_t>(hash.bytes[index / 2U] & 0x0fU);
        char character = lowercase[index];
        if (character >= 'a' && character <= 'f' && nibble >= 8U) {
            character = static_cast<char>(character - 'a' + 'A');
        }
        output.push_back(character);
    }
    return output;
}

std::string Address::lowercase_hex() const {
    return hex_encode(bytes);
}

bool Address::is_zero() const noexcept {
    return std::all_of(bytes.begin(), bytes.end(),
                       [](std::uint8_t byte) { return byte == 0; });
}

std::optional<std::string> DisplayContent::field(std::string_view label) const {
    for (const DisplayField& item : fields) {
        if (item.label == label) {
            return item.value;
        }
    }
    return std::nullopt;
}

// ---------------------------------------------------------------------------
// Display binding
// ---------------------------------------------------------------------------

Hash256 make_display_commitment(const Hash256& signing_digest,
                                ByteView display_bytes) {
    static constexpr std::string_view domain = "netsec.display.v1";

    Bytes preimage;
    preimage.reserve(domain.size() + 32U + 4U + display_bytes.size());
    preimage.insert(preimage.end(), domain.begin(), domain.end());
    preimage.insert(preimage.end(), signing_digest.bytes.begin(),
                    signing_digest.bytes.end());

    const std::uint64_t length = display_bytes.size();
    for (int shift = 24; shift >= 0; shift -= 8) {
        preimage.push_back(
            static_cast<std::uint8_t>((length >> static_cast<unsigned>(shift)) &
                                      0xffU));
    }
    preimage.insert(preimage.end(), display_bytes.begin(), display_bytes.end());
    return detail::keccak256(preimage);
}

bool verify_display_readback(const DisplayReadback& readback) {
    if (make_display_commitment(readback.signing_digest,
                                readback.display_bytes) !=
        readback.display_commitment) {
        return false;
    }
    return !readback.display_bytes.empty();
}

bool verify_display_readback(const Hash256& signing_digest,
                             const Hash256& expected_commitment,
                             ByteView display_bytes) {
    if (display_bytes.empty()) {
        return false;
    }
    return make_display_commitment(signing_digest, display_bytes) ==
           expected_commitment;
}

bool ApprovedTransaction::verify_readback() const {
    return verify_display_readback(signing_digest, display_commitment,
                                   ByteView(display_bytes));
}

Result<Address> recover_signer(const Hash256& signing_digest,
                               const CompactSignature& signature,
                               bool require_low_s) {
    const auto address =
        detail::recover_address(signing_digest, signature, require_low_s);
    if (!address.has_value()) {
        return make_error(ErrorCode::InvalidSignature,
                          "signature recovery failed");
    }
    return *address;
}

// ---------------------------------------------------------------------------
// Confirmation and signing flow
// ---------------------------------------------------------------------------

Confirmation::Confirmation(Bytes raw_transaction,
                           DisplayContent display,
                           Hash256 signing_digest,
                           Hash256 display_commitment,
                           std::optional<Address> recovered_sender,
                           std::optional<Address> expected_signer,
                           bool input_was_signed,
                           bool require_low_s)
    : raw_transaction_(std::move(raw_transaction)),
      display_(std::move(display)),
      signing_digest_(signing_digest),
      display_commitment_(display_commitment),
      recovered_sender_(recovered_sender),
      expected_signer_(expected_signer),
      input_was_signed_(input_was_signed),
      require_low_s_(require_low_s) {}

Result<DisplayReadback> Confirmation::readback() const {
    const auto begin =
        reinterpret_cast<const std::uint8_t*>(display_.text.data());
    const ByteView text(begin, display_.text.size());
    if (make_display_commitment(signing_digest_, text) !=
        display_commitment_) {
        return make_error(ErrorCode::DisplayBindingMismatch,
                          "display content is not bound to the signing digest");
    }
    return DisplayReadback{signing_digest_, display_commitment_,
                           Bytes(text.begin(), text.end())};
}

bool Confirmation::verify_readback(const DisplayReadback& readback) const {
    return readback.signing_digest == signing_digest_ &&
           readback.display_commitment == display_commitment_ &&
           verify_display_readback(readback);
}

bool Confirmation::verify_readback(ByteView frame_bytes) const {
    return verify_display_readback(signing_digest_, display_commitment_,
                                   frame_bytes);
}

Result<ApprovedTransaction> Confirmation::sign(
    const SignCallback& callback) const {
    if (!expected_signer_.has_value()) {
        return make_error(ErrorCode::InvalidArgument,
                          "policy does not configure an expected signer");
    }
    return sign(callback, *expected_signer_);
}

Result<ApprovedTransaction> Confirmation::sign(
    const SignCallback& callback,
    const Address& expected_signer) const {
    if (!callback) {
        return make_error(ErrorCode::InvalidArgument,
                          "signing callback is empty");
    }
    if (expected_signer.is_zero()) {
        return make_error(ErrorCode::InvalidArgument,
                          "expected signer is the zero address");
    }

    const Result<DisplayReadback> before = readback();
    if (!before) {
        return before.error();
    }

    CompactSignature signature;
    try {
        signature = callback(signing_digest_, display_commitment_);
    } catch (...) {
        return make_error(ErrorCode::SigningFailed,
                          "signing callback raised an exception");
    }

    const Result<DisplayReadback> after = readback();
    if (!after) {
        return after.error();
    }
    if (after->display_bytes != before->display_bytes ||
        after->display_commitment != before->display_commitment) {
        return make_error(ErrorCode::DisplayReadbackMismatch,
                          "device display changed during signing");
    }

    const auto recovered = detail::recover_address(
        signing_digest_, signature, require_low_s_);
    if (!recovered.has_value()) {
        return make_error(ErrorCode::InvalidSignature,
                          "device produced an invalid signature");
    }
    if (*recovered != expected_signer) {
        return make_error(
            ErrorCode::SignatureMismatch,
            "device signature does not cover the displayed transaction digest");
    }

    return ApprovedTransaction{signature, *recovered, signing_digest_,
                               display_commitment_, after->display_bytes};
}

TransactionConfirmer::TransactionConfirmer(TransactionPolicy policy)
    : policy_(std::move(policy)) {
    policy_error_ = validate_policy(policy_);
}

Result<Confirmation> TransactionConfirmer::confirm(
    const HostRequest& request) const {
    // request.untrusted_metadata is deliberately ignored. Only the raw bytes
    // are parsed, and every displayed value is rebuilt from those bytes plus
    // firmware policy.
    return confirm(ByteView(request.raw_transaction));
}

Result<Confirmation> TransactionConfirmer::confirm(
    ByteView raw_transaction) const {
    if (policy_error_.has_value()) {
        return *policy_error_;
    }

    Error error;
    const auto parsed = parse_transaction(raw_transaction, policy_, error);
    if (!parsed.has_value()) {
        return error;
    }

    const auto built = build_display(*parsed, policy_, error);
    if (!built.has_value()) {
        return error;
    }

    const auto text_begin =
        reinterpret_cast<const std::uint8_t*>(built->display.text.data());
    const ByteView text(text_begin, built->display.text.size());
    const Hash256 commitment =
        make_display_commitment(parsed->signing_digest, text);

    Bytes owned_raw(raw_transaction.begin(), raw_transaction.end());
    return Confirmation(std::move(owned_raw), built->display,
                        parsed->signing_digest, commitment,
                        parsed->recovered_sender, policy_.expected_signer,
                        parsed->signed_input,
                        policy_.require_low_s_signature);
}

Result<Confirmation> confirm_transaction(
    ByteView raw_transaction,
    const TransactionPolicy& policy,
    const HostMetadata& untrusted_host_metadata) {
    (void)untrusted_host_metadata;
    return TransactionConfirmer(policy).confirm(raw_transaction);
}

}  // namespace netsec
