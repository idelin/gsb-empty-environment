#include "netsec.hpp"

#include <algorithm>
#include <array>
#include <charconv>
#include <cstring>
#include <limits>
#include <stdexcept>
#include <utility>

namespace netsec {
namespace {

constexpr std::uint32_t rotate_right(std::uint32_t value,
                                     unsigned int amount) noexcept {
    return (value >> amount) | (value << (32U - amount));
}

std::uint32_t load_be32(const std::uint8_t* data) noexcept {
    return (static_cast<std::uint32_t>(data[0]) << 24U) |
           (static_cast<std::uint32_t>(data[1]) << 16U) |
           (static_cast<std::uint32_t>(data[2]) << 8U) |
           static_cast<std::uint32_t>(data[3]);
}

void store_be32(std::uint8_t* data, std::uint32_t value) noexcept {
    data[0] = static_cast<std::uint8_t>(value >> 24U);
    data[1] = static_cast<std::uint8_t>(value >> 16U);
    data[2] = static_cast<std::uint8_t>(value >> 8U);
    data[3] = static_cast<std::uint8_t>(value);
}

class Sha256 final {
public:
    void update(std::string_view input) {
        update(reinterpret_cast<const std::uint8_t*>(input.data()),
               input.size());
    }

    void update(const std::uint8_t* input, std::size_t length) {
        total_bytes_ += length;
        while (length > 0) {
            const std::size_t available = block_.size() - block_size_;
            const std::size_t take = std::min(available, length);
            std::memcpy(block_.data() + block_size_, input, take);
            block_size_ += take;
            input += take;
            length -= take;
            if (block_size_ == block_.size()) {
                transform(block_.data());
                block_size_ = 0;
            }
        }
    }

    std::array<std::uint8_t, 32> finish() {
        const std::uint64_t bit_length = total_bytes_ * 8U;
        block_[block_size_++] = 0x80U;
        if (block_size_ > 56U) {
            std::fill(block_.begin() + static_cast<std::ptrdiff_t>(block_size_),
                      block_.end(), 0U);
            transform(block_.data());
            block_size_ = 0;
        }
        std::fill(block_.begin() + static_cast<std::ptrdiff_t>(block_size_),
                  block_.begin() + 56, 0U);
        for (std::size_t index = 0; index < 8; ++index) {
            block_[56 + index] = static_cast<std::uint8_t>(
                bit_length >> (56U - static_cast<unsigned int>(index) * 8U));
        }
        transform(block_.data());

        std::array<std::uint8_t, 32> digest{};
        for (std::size_t index = 0; index < state_.size(); ++index) {
            store_be32(digest.data() + index * 4U, state_[index]);
        }
        return digest;
    }

private:
    void transform(const std::uint8_t* chunk) noexcept {
        static constexpr std::array<std::uint32_t, 64> constants = {
            0x428a2f98U, 0x71374491U, 0xb5c0fbcfU, 0xe9b5dba5U,
            0x3956c25bU, 0x59f111f1U, 0x923f82a4U, 0xab1c5ed5U,
            0xd807aa98U, 0x12835b01U, 0x243185beU, 0x550c7dc3U,
            0x72be5d74U, 0x80deb1feU, 0x9bdc06a7U, 0xc19bf174U,
            0xe49b69c1U, 0xefbe4786U, 0x0fc19dc6U, 0x240ca1ccU,
            0x2de92c6fU, 0x4a7484aaU, 0x5cb0a9dcU, 0x76f988daU,
            0x983e5152U, 0xa831c66dU, 0xb00327c8U, 0xbf597fc7U,
            0xc6e00bf3U, 0xd5a79147U, 0x06ca6351U, 0x14292967U,
            0x27b70a85U, 0x2e1b2138U, 0x4d2c6dfcU, 0x53380d13U,
            0x650a7354U, 0x766a0abbU, 0x81c2c92eU, 0x92722c85U,
            0xa2bfe8a1U, 0xa81a664bU, 0xc24b8b70U, 0xc76c51a3U,
            0xd192e819U, 0xd6990624U, 0xf40e3585U, 0x106aa070U,
            0x19a4c116U, 0x1e376c08U, 0x2748774cU, 0x34b0bcb5U,
            0x391c0cb3U, 0x4ed8aa4aU, 0x5b9cca4fU, 0x682e6ff3U,
            0x748f82eeU, 0x78a5636fU, 0x84c87814U, 0x8cc70208U,
            0x90befffaU, 0xa4506cebU, 0xbef9a3f7U, 0xc67178f2U};

        std::array<std::uint32_t, 64> schedule{};
        for (std::size_t index = 0; index < 16; ++index) {
            schedule[index] = load_be32(chunk + index * 4U);
        }
        for (std::size_t index = 16; index < schedule.size(); ++index) {
            const std::uint32_t s0 =
                rotate_right(schedule[index - 15], 7U) ^
                rotate_right(schedule[index - 15], 18U) ^
                (schedule[index - 15] >> 3U);
            const std::uint32_t s1 =
                rotate_right(schedule[index - 2], 17U) ^
                rotate_right(schedule[index - 2], 19U) ^
                (schedule[index - 2] >> 10U);
            schedule[index] = schedule[index - 16] + s0 +
                              schedule[index - 7] + s1;
        }

        std::uint32_t a = state_[0];
        std::uint32_t b = state_[1];
        std::uint32_t c = state_[2];
        std::uint32_t d = state_[3];
        std::uint32_t e = state_[4];
        std::uint32_t f = state_[5];
        std::uint32_t g = state_[6];
        std::uint32_t h = state_[7];

        for (std::size_t index = 0; index < schedule.size(); ++index) {
            const std::uint32_t sum1 =
                rotate_right(e, 6U) ^ rotate_right(e, 11U) ^
                rotate_right(e, 25U);
            const std::uint32_t choose = (e & f) ^ ((~e) & g);
            const std::uint32_t temp1 =
                h + sum1 + choose + constants[index] + schedule[index];
            const std::uint32_t sum0 =
                rotate_right(a, 2U) ^ rotate_right(a, 13U) ^
                rotate_right(a, 22U);
            const std::uint32_t majority = (a & b) ^ (a & c) ^ (b & c);
            const std::uint32_t temp2 = sum0 + majority;

            h = g;
            g = f;
            f = e;
            e = d + temp1;
            d = c;
            c = b;
            b = a;
            a = temp1 + temp2;
        }

        state_[0] += a;
        state_[1] += b;
        state_[2] += c;
        state_[3] += d;
        state_[4] += e;
        state_[5] += f;
        state_[6] += g;
        state_[7] += h;
    }

    std::array<std::uint32_t, 8> state_{
        0x6a09e667U, 0xbb67ae85U, 0x3c6ef372U, 0xa54ff53aU,
        0x510e527fU, 0x9b05688cU, 0x1f83d9abU, 0x5be0cd19U};
    std::array<std::uint8_t, 64> block_{};
    std::size_t block_size_{};
    std::uint64_t total_bytes_{};
};

std::string hex_encode(const std::uint8_t* data, std::size_t size) {
    static constexpr char digits[] = "0123456789abcdef";
    std::string result;
    result.reserve(size * 2U);
    for (std::size_t index = 0; index < size; ++index) {
        result.push_back(digits[data[index] >> 4U]);
        result.push_back(digits[data[index] & 0x0fU]);
    }
    return result;
}

std::string sha256_hex(std::string_view input) {
    Sha256 hash;
    hash.update(input);
    const auto digest = hash.finish();
    return hex_encode(digest.data(), digest.size());
}

std::string hmac_sha256_hex(std::string_view key, std::string_view message) {
    constexpr std::size_t block_size = 64;
    std::array<std::uint8_t, block_size> key_block{};

    if (key.size() > block_size) {
        Sha256 key_hash;
        key_hash.update(key);
        const auto digest = key_hash.finish();
        std::copy(digest.begin(), digest.end(), key_block.begin());
    } else {
        std::memcpy(key_block.data(), key.data(), key.size());
    }

    std::array<std::uint8_t, block_size> inner_pad{};
    std::array<std::uint8_t, block_size> outer_pad{};
    for (std::size_t index = 0; index < block_size; ++index) {
        inner_pad[index] =
            static_cast<std::uint8_t>(key_block[index] ^ 0x36U);
        outer_pad[index] =
            static_cast<std::uint8_t>(key_block[index] ^ 0x5cU);
    }

    Sha256 inner;
    inner.update(inner_pad.data(), inner_pad.size());
    inner.update(message);
    const auto inner_digest = inner.finish();

    Sha256 outer;
    outer.update(outer_pad.data(), outer_pad.size());
    outer.update(inner_digest.data(), inner_digest.size());
    const auto digest = outer.finish();
    return hex_encode(digest.data(), digest.size());
}

bool constant_time_equal(std::string_view left, std::string_view right) {
    if (left.size() != right.size()) {
        return false;
    }
    unsigned int difference = 0;
    for (std::size_t index = 0; index < left.size(); ++index) {
        difference |= static_cast<unsigned int>(
            static_cast<unsigned char>(left[index]) ^
            static_cast<unsigned char>(right[index]));
    }
    return difference == 0;
}

bool decode_utf8(std::string_view input, std::size_t position,
                 std::uint32_t& code_point, std::size_t& length) noexcept {
    if (position >= input.size()) {
        return false;
    }
    const auto first =
        static_cast<std::uint8_t>(static_cast<unsigned char>(input[position]));
    if (first < 0x80U) {
        code_point = first;
        length = 1;
        return true;
    }

    std::size_t count = 0;
    std::uint32_t value = 0;
    std::uint32_t minimum = 0;
    if (first >= 0xc2U && first <= 0xdfU) {
        count = 2;
        value = first & 0x1fU;
        minimum = 0x80U;
    } else if (first >= 0xe0U && first <= 0xefU) {
        count = 3;
        value = first & 0x0fU;
        minimum = 0x800U;
    } else if (first >= 0xf0U && first <= 0xf4U) {
        count = 4;
        value = first & 0x07U;
        minimum = 0x10000U;
    } else {
        return false;
    }

    if (position + count > input.size()) {
        return false;
    }
    for (std::size_t index = 1; index < count; ++index) {
        const auto next = static_cast<std::uint8_t>(
            static_cast<unsigned char>(input[position + index]));
        if ((next & 0xc0U) != 0x80U) {
            return false;
        }
        value = (value << 6U) | (next & 0x3fU);
    }

    if (value < minimum || value > 0x10ffffU ||
        (value >= 0xd800U && value <= 0xdfffU)) {
        return false;
    }
    code_point = value;
    length = count;
    return true;
}

void append_utf8(std::string& output, std::uint32_t code_point) {
    if (code_point <= 0x7fU) {
        output.push_back(static_cast<char>(code_point));
    } else if (code_point <= 0x7ffU) {
        output.push_back(static_cast<char>(0xc0U | (code_point >> 6U)));
        output.push_back(static_cast<char>(0x80U | (code_point & 0x3fU)));
    } else if (code_point <= 0xffffU) {
        output.push_back(static_cast<char>(0xe0U | (code_point >> 12U)));
        output.push_back(
            static_cast<char>(0x80U | ((code_point >> 6U) & 0x3fU)));
        output.push_back(static_cast<char>(0x80U | (code_point & 0x3fU)));
    } else {
        output.push_back(static_cast<char>(0xf0U | (code_point >> 18U)));
        output.push_back(
            static_cast<char>(0x80U | ((code_point >> 12U) & 0x3fU)));
        output.push_back(
            static_cast<char>(0x80U | ((code_point >> 6U) & 0x3fU)));
        output.push_back(static_cast<char>(0x80U | (code_point & 0x3fU)));
    }
}

void append_unicode_escape(std::string& output, std::uint32_t code_point) {
    static constexpr char digits[] = "0123456789abcdef";
    output += "\\u";
    output.push_back(digits[(code_point >> 12U) & 0x0fU]);
    output.push_back(digits[(code_point >> 8U) & 0x0fU]);
    output.push_back(digits[(code_point >> 4U) & 0x0fU]);
    output.push_back(digits[code_point & 0x0fU]);
}

std::string escape_json_string(std::string_view value) {
    std::string output;
    output.reserve(value.size() + 8U);

    for (std::size_t index = 0; index < value.size();) {
        const auto byte = static_cast<std::uint8_t>(
            static_cast<unsigned char>(value[index]));
        switch (byte) {
            case '"':
                output += "\\\"";
                ++index;
                continue;
            case '\\':
                output += "\\\\";
                ++index;
                continue;
            case '\b':
                output += "\\b";
                ++index;
                continue;
            case '\f':
                output += "\\f";
                ++index;
                continue;
            case '\n':
                output += "\\n";
                ++index;
                continue;
            case '\r':
                output += "\\r";
                ++index;
                continue;
            case '\t':
                output += "\\t";
                ++index;
                continue;
            default:
                break;
        }

        if (byte < 0x20U) {
            append_unicode_escape(output, byte);
            ++index;
            continue;
        }
        if (byte == 0x7fU) {
            append_unicode_escape(output, byte);
            ++index;
            continue;
        }
        if (byte < 0x80U) {
            output.push_back(static_cast<char>(byte));
            ++index;
            continue;
        }

        std::uint32_t code_point = 0;
        std::size_t length = 0;
        if (!decode_utf8(value, index, code_point, length)) {
            throw std::invalid_argument(
                "netsec: audit field contains invalid UTF-8");
        }
        if ((code_point >= 0x80U && code_point <= 0x9fU) ||
            code_point == 0x2028U || code_point == 0x2029U) {
            append_unicode_escape(output, code_point);
        } else {
            output.append(value.substr(index, length));
        }
        index += length;
    }
    return output;
}

bool is_lower_hex(std::string_view value, std::size_t expected_size) {
    if (value.size() != expected_size) {
        return false;
    }
    return std::all_of(value.begin(), value.end(), [](char character) {
        return (character >= '0' && character <= '9') ||
               (character >= 'a' && character <= 'f');
    });
}

void expect_literal(std::string_view input, std::size_t& position,
                    std::string_view expected) {
    if (input.substr(position, expected.size()) != expected) {
        throw std::invalid_argument("netsec: malformed canonical record");
    }
    position += expected.size();
}

std::uint64_t parse_unsigned(std::string_view input, std::size_t& position) {
    const std::size_t start = position;
    while (position < input.size() && input[position] >= '0' &&
           input[position] <= '9') {
        ++position;
    }
    if (start == position || (position - start > 1 && input[start] == '0')) {
        throw std::invalid_argument("netsec: invalid unsigned integer");
    }

    std::uint64_t value = 0;
    const auto result =
        std::from_chars(input.data() + start, input.data() + position, value);
    if (result.ec != std::errc{} || result.ptr != input.data() + position) {
        throw std::invalid_argument("netsec: unsigned integer overflow");
    }
    return value;
}

std::int64_t parse_signed(std::string_view input, std::size_t& position) {
    const bool negative =
        position < input.size() && input[position] == '-';
    if (negative) {
        ++position;
    }
    const std::uint64_t magnitude = parse_unsigned(input, position);
    if (negative) {
        constexpr std::uint64_t minimum_magnitude =
            static_cast<std::uint64_t>(std::numeric_limits<std::int64_t>::max()) +
            1U;
        if (magnitude > minimum_magnitude) {
            throw std::invalid_argument("netsec: signed integer overflow");
        }
        if (magnitude == minimum_magnitude) {
            return std::numeric_limits<std::int64_t>::min();
        }
        return -static_cast<std::int64_t>(magnitude);
    }
    if (magnitude >
        static_cast<std::uint64_t>(std::numeric_limits<std::int64_t>::max())) {
        throw std::invalid_argument("netsec: signed integer overflow");
    }
    return static_cast<std::int64_t>(magnitude);
}

std::uint32_t parse_hex4(std::string_view input, std::size_t& position) {
    if (position + 4 > input.size()) {
        throw std::invalid_argument("netsec: truncated Unicode escape");
    }
    std::uint32_t value = 0;
    for (std::size_t index = 0; index < 4; ++index) {
        const char character = input[position + index];
        value <<= 4U;
        if (character >= '0' && character <= '9') {
            value |= static_cast<std::uint32_t>(character - '0');
        } else if (character >= 'a' && character <= 'f') {
            value |= static_cast<std::uint32_t>(character - 'a' + 10);
        } else if (character >= 'A' && character <= 'F') {
            value |= static_cast<std::uint32_t>(character - 'A' + 10);
        } else {
            throw std::invalid_argument("netsec: invalid Unicode escape");
        }
    }
    position += 4;
    return value;
}

std::string parse_json_string(std::string_view input,
                              std::size_t& position) {
    if (position >= input.size() || input[position] != '"') {
        throw std::invalid_argument("netsec: expected JSON string");
    }
    ++position;
    std::string output;

    while (position < input.size()) {
        const char character = input[position];
        if (character == '"') {
            ++position;
            return output;
        }
        if (character == '\\') {
            ++position;
            if (position >= input.size()) {
                throw std::invalid_argument("netsec: truncated JSON escape");
            }
            const char escape = input[position++];
            switch (escape) {
                case '"':
                    output.push_back('"');
                    break;
                case '\\':
                    output.push_back('\\');
                    break;
                case '/':
                    output.push_back('/');
                    break;
                case 'b':
                    output.push_back('\b');
                    break;
                case 'f':
                    output.push_back('\f');
                    break;
                case 'n':
                    output.push_back('\n');
                    break;
                case 'r':
                    output.push_back('\r');
                    break;
                case 't':
                    output.push_back('\t');
                    break;
                case 'u': {
                    const std::uint32_t first = parse_hex4(input, position);
                    std::uint32_t code_point = first;
                    if (first >= 0xd800U && first <= 0xdbffU) {
                        if (position + 2 > input.size() ||
                            input[position] != '\\' ||
                            input[position + 1] != 'u') {
                            throw std::invalid_argument(
                                "netsec: missing low surrogate");
                        }
                        position += 2;
                        const std::uint32_t second =
                            parse_hex4(input, position);
                        if (second < 0xdc00U || second > 0xdfffU) {
                            throw std::invalid_argument(
                                "netsec: invalid low surrogate");
                        }
                        code_point =
                            0x10000U + ((first - 0xd800U) << 10U) +
                            (second - 0xdc00U);
                    } else if (first >= 0xdc00U && first <= 0xdfffU) {
                        throw std::invalid_argument(
                            "netsec: lone low surrogate");
                    }
                    append_utf8(output, code_point);
                    break;
                }
                default:
                    throw std::invalid_argument(
                        "netsec: invalid JSON escape");
            }
            continue;
        }

        const auto byte = static_cast<std::uint8_t>(
            static_cast<unsigned char>(character));
        if (byte < 0x20U) {
            throw std::invalid_argument(
                "netsec: raw control character in JSON string");
        }
        if (byte < 0x80U) {
            output.push_back(character);
            ++position;
            continue;
        }

        std::uint32_t code_point = 0;
        std::size_t length = 0;
        if (!decode_utf8(input, position, code_point, length)) {
            throw std::invalid_argument(
                "netsec: invalid UTF-8 in JSON string");
        }
        output.append(input.substr(position, length));
        position += length;
    }
    throw std::invalid_argument("netsec: unterminated JSON string");
}

AuditRecord parse_canonical_record(std::string_view line) {
    AuditRecord record;
    std::size_t position = 0;

    expect_literal(line, position, R"({"version":)");
    record.version = parse_unsigned(line, position);
    expect_literal(line, position, R"(,"sequence":)");
    record.sequence = parse_unsigned(line, position);
    expect_literal(line, position, R"(,"timestamp_ms":)");
    record.timestamp_ms = parse_signed(line, position);
    expect_literal(line, position, R"(,"principal":)");
    record.principal = parse_json_string(line, position);
    expect_literal(line, position, R"(,"action":)");
    record.action = parse_json_string(line, position);
    expect_literal(line, position, R"(,"object":)");
    record.object = parse_json_string(line, position);
    expect_literal(line, position, R"(,"outcome":)");
    record.outcome = parse_json_string(line, position);
    expect_literal(line, position, R"(,"prev_hash":)");
    record.previous_hash = parse_json_string(line, position);
    expect_literal(line, position, R"(,"hash":)");
    record.hash = parse_json_string(line, position);
    expect_literal(line, position, R"(,"signature":)");
    record.signature = parse_json_string(line, position);
    expect_literal(line, position, "}");

    if (position != line.size()) {
        throw std::invalid_argument(
            "netsec: trailing data after canonical record");
    }
    if (record.version != audit_format_version) {
        throw std::invalid_argument("netsec: unsupported audit format version");
    }
    if (!is_lower_hex(record.previous_hash, audit_hash_hex_size) ||
        !is_lower_hex(record.hash, audit_hash_hex_size) ||
        !is_lower_hex(record.signature, audit_signature_hex_size)) {
        throw std::invalid_argument(
            "netsec: hash and signature must be lowercase SHA-256 hex");
    }

    return record;
}

}  // namespace

std::string canonical_payload(const AuditRecord& record) {
    if (record.version != audit_format_version) {
        throw std::invalid_argument("netsec: unsupported audit format version");
    }

    std::string output;
    output.reserve(256U + record.principal.size() + record.action.size() +
                   record.object.size() + record.outcome.size());
    output += R"({"version":)";
    output += std::to_string(record.version);
    output += R"(,"sequence":)";
    output += std::to_string(record.sequence);
    output += R"(,"timestamp_ms":)";
    output += std::to_string(record.timestamp_ms);
    output += R"(,"principal":")";
    output += escape_json_string(record.principal);
    output += R"(","action":")";
    output += escape_json_string(record.action);
    output += R"(","object":")";
    output += escape_json_string(record.object);
    output += R"(","outcome":")";
    output += escape_json_string(record.outcome);
    output += R"(","prev_hash":")";
    output += escape_json_string(record.previous_hash);
    output += R"("})";
    return output;
}

std::string encode_record(const AuditRecord& record) {
    std::string output = canonical_payload(record);
    output.pop_back();
    output += R"(,"hash":")";
    output += escape_json_string(record.hash);
    output += R"(","signature":")";
    output += escape_json_string(record.signature);
    output += R"("})";
    return output;
}

AuditRecord parse_record(std::string_view line) {
    AuditRecord record = parse_canonical_record(line);
    if (encode_record(record) != line) {
        throw std::invalid_argument(
            "netsec: record is not in canonical JSON form");
    }
    return record;
}

AuditLogger::AuditLogger(std::string signing_key,
                         ServerContextProvider server_context,
                         std::uint64_t first_sequence,
                         std::string previous_hash)
    : signing_key_(std::move(signing_key)),
      server_context_(std::move(server_context)),
      next_sequence_(first_sequence),
      last_hash_(std::move(previous_hash)) {
    if (signing_key_.empty()) {
        throw std::invalid_argument("netsec: signing key must not be empty");
    }
    if (!server_context_) {
        throw std::invalid_argument(
            "netsec: server context provider must not be empty");
    }
    if (next_sequence_ == 0) {
        throw std::invalid_argument(
            "netsec: first audit sequence must be positive");
    }
    if (!is_lower_hex(last_hash_, audit_hash_hex_size)) {
        throw std::invalid_argument(
            "netsec: previous hash must be lowercase SHA-256 hex");
    }
}

std::string AuditLogger::append(const AuditEvent& event) {
    std::lock_guard<std::mutex> lock(mutex_);
    if (next_sequence_ == 0) {
        throw std::overflow_error("netsec: audit sequence exhausted");
    }

    const ServerContext context = server_context_();
    if (context.principal.empty()) {
        throw std::invalid_argument(
            "netsec: authenticated principal must not be empty");
    }

    AuditRecord record;
    record.sequence = next_sequence_;
    record.timestamp_ms = context.unix_time_ms;
    record.principal = context.principal;
    record.action = event.action;
    record.object = event.object;
    record.outcome = event.outcome;
    record.previous_hash = last_hash_;

    const std::string payload = canonical_payload(record);
    record.hash = sha256_hex(payload);
    record.signature = hmac_sha256_hex(signing_key_, payload);
    std::string line = encode_record(record);
    line.push_back('\n');

    last_hash_ = record.hash;
    if (next_sequence_ == std::numeric_limits<std::uint64_t>::max()) {
        next_sequence_ = 0;
    } else {
        ++next_sequence_;
    }
    return line;
}

std::uint64_t AuditLogger::next_sequence() const {
    std::lock_guard<std::mutex> lock(mutex_);
    return next_sequence_;
}

std::string AuditLogger::last_hash() const {
    std::lock_guard<std::mutex> lock(mutex_);
    return last_hash_;
}

VerificationResult verify_log(std::string_view log,
                              std::string_view signing_key,
                              std::uint64_t first_sequence,
                              std::string_view first_previous_hash) {
    VerificationResult result;
    result.next_sequence = first_sequence;
    result.last_hash = std::string(first_previous_hash);

    const auto fail = [&result](VerificationFailure failure,
                                std::size_t record_number,
                                std::string message) {
        result.ok = false;
        result.failure = failure;
        result.failed_record = record_number;
        result.message = std::move(message);
        return result;
    };

    if (signing_key.empty()) {
        return fail(VerificationFailure::invalid_argument, 0,
                    "signing key must not be empty");
    }
    if (first_sequence == 0) {
        return fail(VerificationFailure::invalid_argument, 0,
                    "first sequence must be positive");
    }
    if (!is_lower_hex(first_previous_hash, audit_hash_hex_size)) {
        return fail(VerificationFailure::invalid_argument, 0,
                    "first previous hash must be lowercase SHA-256 hex");
    }
    if (log.empty()) {
        return fail(VerificationFailure::empty_log, 0,
                    "audit log contains no records");
    }

    std::uint64_t expected_sequence = first_sequence;
    std::size_t position = 0;
    while (position < log.size()) {
        const std::size_t newline = log.find('\n', position);
        const std::size_t end =
            newline == std::string_view::npos ? log.size() : newline;
        const std::string_view line = log.substr(position, end - position);
        const std::size_t record_number = result.records_verified + 1;

        if (line.empty()) {
            return fail(VerificationFailure::malformed_record, record_number,
                        "empty physical log line");
        }

        AuditRecord record;
        try {
            record = parse_record(line);
        } catch (const std::exception& error) {
            return fail(VerificationFailure::malformed_record, record_number,
                        error.what());
        }

        if (record.sequence != expected_sequence) {
            return fail(VerificationFailure::sequence_mismatch, record_number,
                        "audit sequence does not follow the previous record");
        }
        if (record.previous_hash != result.last_hash) {
            return fail(VerificationFailure::previous_hash_mismatch,
                        record_number,
                        "previous hash does not match the preceding record");
        }

        std::string payload;
        std::string expected_hash;
        std::string expected_signature;
        try {
            payload = canonical_payload(record);
            expected_hash = sha256_hex(payload);
            expected_signature = hmac_sha256_hex(signing_key, payload);
        } catch (const std::exception& error) {
            return fail(VerificationFailure::malformed_record, record_number,
                        error.what());
        }

        if (!constant_time_equal(record.hash, expected_hash)) {
            return fail(VerificationFailure::record_hash_mismatch,
                        record_number,
                        "record hash does not match its canonical payload");
        }
        if (!constant_time_equal(record.signature, expected_signature)) {
            return fail(VerificationFailure::signature_mismatch, record_number,
                        "record signature is invalid");
        }

        ++result.records_verified;
        result.last_hash = record.hash;
        if (expected_sequence ==
            std::numeric_limits<std::uint64_t>::max()) {
            expected_sequence = 0;
        } else {
            ++expected_sequence;
        }
        result.next_sequence = expected_sequence;

        position = newline == std::string_view::npos ? log.size() : newline + 1;
    }

    result.ok = true;
    result.failure = VerificationFailure::none;
    result.failed_record = 0;
    result.message.clear();
    return result;
}

}  // namespace netsec
