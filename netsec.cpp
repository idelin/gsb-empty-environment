#include "netsec.hpp"

#include <algorithm>
#include <array>
#include <utility>

namespace netsec {
namespace {

constexpr std::array<std::uint32_t, 64> kSha256Constants{
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
    0x90befffaU, 0xa4506cebU, 0xbef9a3f7U, 0xc67178f2U,
};

constexpr std::array<std::uint32_t, 8> kSha256Initial{
    0x6a09e667U, 0xbb67ae85U, 0x3c6ef372U, 0xa54ff53aU,
    0x510e527fU, 0x9b05688cU, 0x1f83d9abU, 0x5be0cd19U,
};

[[nodiscard]] std::uint32_t rotate_right(
    std::uint32_t value,
    unsigned count) noexcept {
    return (value >> count) | (value << (32U - count));
}

void sha256_compress(
    std::array<std::uint32_t, 8>& state,
    const std::uint8_t* block) noexcept {
    std::array<std::uint32_t, 64> words{};
    for (std::size_t i = 0; i < 16; ++i) {
        const auto offset = i * 4;
        words[i] = (static_cast<std::uint32_t>(block[offset]) << 24U) |
                   (static_cast<std::uint32_t>(block[offset + 1]) << 16U) |
                   (static_cast<std::uint32_t>(block[offset + 2]) << 8U) |
                   static_cast<std::uint32_t>(block[offset + 3]);
    }

    for (std::size_t i = 16; i < 64; ++i) {
        const auto s0 = rotate_right(words[i - 15], 7U) ^
                        rotate_right(words[i - 15], 18U) ^
                        (words[i - 15] >> 3U);
        const auto s1 = rotate_right(words[i - 2], 17U) ^
                        rotate_right(words[i - 2], 19U) ^
                        (words[i - 2] >> 10U);
        words[i] = words[i - 16] + s0 + words[i - 7] + s1;
    }

    auto a = state[0];
    auto b = state[1];
    auto c = state[2];
    auto d = state[3];
    auto e = state[4];
    auto f = state[5];
    auto g = state[6];
    auto h = state[7];

    for (std::size_t i = 0; i < 64; ++i) {
        const auto sum1 = rotate_right(e, 6U) ^ rotate_right(e, 11U) ^
                          rotate_right(e, 25U);
        const auto choose = (e & f) ^ (~e & g);
        const auto temp1 = h + sum1 + choose + kSha256Constants[i] + words[i];
        const auto sum0 = rotate_right(a, 2U) ^ rotate_right(a, 13U) ^
                          rotate_right(a, 22U);
        const auto majority = (a & b) ^ (a & c) ^ (b & c);
        const auto temp2 = sum0 + majority;

        h = g;
        g = f;
        f = e;
        e = d + temp1;
        d = c;
        c = b;
        b = a;
        a = temp1 + temp2;
    }

    state[0] += a;
    state[1] += b;
    state[2] += c;
    state[3] += d;
    state[4] += e;
    state[5] += f;
    state[6] += g;
    state[7] += h;
}

[[nodiscard]] std::array<std::uint8_t, 32> sha256(
    std::span<const std::uint8_t> input) {
    std::vector<std::uint8_t> padded(input.begin(), input.end());
    padded.push_back(0x80U);

    while ((padded.size() % 64U) != 56U) {
        padded.push_back(0U);
    }

    const auto bit_length = static_cast<std::uint64_t>(input.size()) * 8U;
    for (int shift = 56; shift >= 0; shift -= 8) {
        padded.push_back(static_cast<std::uint8_t>(
            (bit_length >> static_cast<unsigned>(shift)) & 0xffU));
    }

    auto state = kSha256Initial;
    for (std::size_t offset = 0; offset < padded.size(); offset += 64U) {
        sha256_compress(state, padded.data() + offset);
    }

    std::array<std::uint8_t, 32> digest{};
    for (std::size_t i = 0; i < state.size(); ++i) {
        digest[i * 4] =
            static_cast<std::uint8_t>((state[i] >> 24U) & 0xffU);
        digest[i * 4 + 1] =
            static_cast<std::uint8_t>((state[i] >> 16U) & 0xffU);
        digest[i * 4 + 2] =
            static_cast<std::uint8_t>((state[i] >> 8U) & 0xffU);
        digest[i * 4 + 3] =
            static_cast<std::uint8_t>(state[i] & 0xffU);
    }
    return digest;
}

[[nodiscard]] std::string hex_encode(
    std::span<const std::uint8_t> bytes) {
    static constexpr char kDigits[] = "0123456789abcdef";
    std::string output;
    output.reserve(bytes.size() * 2U);
    for (const auto byte : bytes) {
        output.push_back(kDigits[byte >> 4U]);
        output.push_back(kDigits[byte & 0x0fU]);
    }
    return output;
}

[[nodiscard]] std::string ascii_upper(std::string_view input) {
    std::string output;
    output.reserve(input.size());
    for (const auto character : input) {
        if (character >= 'a' && character <= 'z') {
            output.push_back(static_cast<char>(character - ('a' - 'A')));
        } else {
            output.push_back(character);
        }
    }
    return output;
}

[[nodiscard]] std::string ascii_lower(std::string_view input) {
    std::string output;
    output.reserve(input.size());
    for (const auto character : input) {
        if (character >= 'A' && character <= 'Z') {
            output.push_back(static_cast<char>(character + ('a' - 'A')));
        } else {
            output.push_back(character);
        }
    }
    return output;
}

[[nodiscard]] bool is_identifier_character(char character) noexcept {
    return (character >= 'A' && character <= 'Z') ||
           (character >= 'a' && character <= 'z') ||
           (character >= '0' && character <= '9') ||
           character == '-' || character == '_';
}

[[nodiscard]] bool valid_identifier(
    std::string_view value,
    std::size_t maximum_length) noexcept {
    if (value.empty() || value.size() > maximum_length) {
        return false;
    }
    return std::all_of(
        value.begin(), value.end(), is_identifier_character);
}

[[nodiscard]] bool is_hex_digit(char character) noexcept {
    return (character >= '0' && character <= '9') ||
           (character >= 'a' && character <= 'f') ||
           (character >= 'A' && character <= 'F');
}

[[nodiscard]] bool valid_hex_signature(std::string_view value) noexcept {
    return value.size() == 64U &&
           std::all_of(value.begin(), value.end(), is_hex_digit);
}

[[nodiscard]] bool valid_confirmation_reference(
    std::string_view value,
    std::size_t maximum_length) noexcept {
    if (value.empty() || value.size() > maximum_length) {
        return false;
    }
    return std::all_of(value.begin(), value.end(), [](char character) {
        return character >= 0x20 && character <= 0x7e;
    });
}

[[nodiscard]] bool valid_payload_character(
    char character,
    bool allow_multiline) noexcept {
    if (character >= 0x20 && character <= 0x7e) {
        return true;
    }
    if (character == '\t') {
        return allow_multiline;
    }
    if (character == '\n') {
        return allow_multiline;
    }
    return false;
}

[[nodiscard]] bool valid_payload(
    std::string_view payload,
    std::size_t maximum_length,
    bool allow_multiline) noexcept {
    if (payload.size() > maximum_length) {
        return false;
    }
    return std::all_of(
        payload.begin(),
        payload.end(),
        [allow_multiline](char character) {
            return valid_payload_character(character, allow_multiline);
        });
}

[[nodiscard]] bool is_boundary(char character) noexcept {
    return !((character >= 'A' && character <= 'Z') ||
             (character >= 'a' && character <= 'z') ||
             (character >= '0' && character <= '9'));
}

[[nodiscard]] bool contains_phrase(
    std::string_view upper_payload,
    std::string_view phrase) noexcept {
    if (phrase.empty()) {
        return false;
    }

    std::size_t position = 0;
    while ((position = upper_payload.find(phrase, position)) !=
           std::string_view::npos) {
        const bool left_boundary =
            position == 0 || is_boundary(upper_payload[position - 1]);
        const auto end = position + phrase.size();
        const bool right_boundary =
            end >= upper_payload.size() || is_boundary(upper_payload[end]);
        if (left_boundary && right_boundary) {
            return true;
        }
        ++position;
    }
    return false;
}

[[nodiscard]] bool contains_critical_instruction(
    std::string_view payload) {
    static constexpr std::array<std::string_view, 19> kPhrases{
        "ALTITUDE", "CANCEL", "CLEAR", "CLEARANCE", "CLEARED",
        "CLIMB", "CLIMBING", "CONTACT", "DESCEND", "DESCENDING",
        "DIRECT", "DISREGARD", "FLIGHT LEVEL", "HEADING", "HOLD",
        "MAINTAIN", "ROUTE", "SQUAWK", "TURN",
    };
    static constexpr std::array<std::string_view, 4> kAdditionalPhrases{
        "PROCEED", "TURN LEFT", "TURN RIGHT", "WAYPOINT",
    };

    const auto upper_payload = ascii_upper(payload);
    for (const auto phrase : kPhrases) {
        if (contains_phrase(upper_payload, phrase)) {
            return true;
        }
    }
    for (const auto phrase : kAdditionalPhrases) {
        if (contains_phrase(upper_payload, phrase)) {
            return true;
        }
    }
    return false;
}

void append_field(
    std::string& output,
    std::string_view name,
    std::string_view value) {
    output.append(name);
    output.push_back('=');
    output.append(std::to_string(value.size()));
    output.push_back(':');
    output.append(value);
    output.push_back('\n');
}

void append_message_fields(
    std::string& output,
    const InboundMessage& message) {
    append_field(output, "type", ascii_upper(message.type));
    append_field(output, "source", ascii_upper(message.source));
    append_field(output, "destination", ascii_upper(message.destination));
    append_field(output, "timestamp", message.timestamp);
    append_field(output, "sequence", std::to_string(message.sequence));
    append_field(output, "payload", message.payload);
    append_field(output, "confirmation", message.confirmation);
}

[[nodiscard]] std::string canonical_core(const InboundMessage& message) {
    std::string output;
    output.reserve(
        message.type.size() + message.source.size() +
        message.destination.size() + message.timestamp.size() +
        message.payload.size() + message.confirmation.size() + 128U);

    output.append("NETSEC-AVIATION-CORE/V1\n");
    append_message_fields(output, message);
    return output;
}

[[nodiscard]] std::string canonical_full(const InboundMessage& message) {
    std::string output;
    output.reserve(
        message.type.size() + message.source.size() +
        message.destination.size() + message.timestamp.size() +
        message.payload.size() + message.confirmation.size() + 128U);

    output.append("NETSEC-AVIATION-MSG/V1\n");
    append_message_fields(output, message);
    return output;
}

[[nodiscard]] std::string confirmation_input(
    const InboundMessage& message) {
    std::string output;
    output.append("NETSEC-AVIATION-CONFIRM/V1\n");
    output.append(sha256_hex(canonical_core(message)));
    output.push_back('\n');
    return output;
}

[[nodiscard]] std::uint64_t days_from_civil(
    std::int64_t year,
    unsigned month,
    unsigned day) noexcept {
    year -= month <= 2U;
    const std::int64_t era = (year >= 0 ? year : year - 399) / 400;
    const auto year_of_era =
        static_cast<unsigned>(year - era * 400);
    const unsigned adjusted_month =
        month > 2U ? month - 3U : month + 9U;
    const unsigned day_of_year =
        (153U * adjusted_month + 2U) / 5U + day - 1U;
    const unsigned day_of_era =
        year_of_era * 365U + year_of_era / 4U -
        year_of_era / 100U + day_of_year;
    return static_cast<std::uint64_t>(
        era * 146097 + static_cast<std::int64_t>(day_of_era) - 719468);
}

[[nodiscard]] bool leap_year(std::int64_t year) noexcept {
    return (year % 4 == 0 && year % 100 != 0) || year % 400 == 0;
}

[[nodiscard]] unsigned days_in_month(
    std::int64_t year,
    unsigned month) noexcept {
    static constexpr std::array<unsigned, 12> kDays{
        31U, 28U, 31U, 30U, 31U, 30U, 31U, 31U, 30U, 31U, 30U, 31U,
    };
    if (month == 2U && leap_year(year)) {
        return 29U;
    }
    return kDays[month - 1U];
}

[[nodiscard]] bool parse_decimal(
    std::string_view value,
    std::uint32_t& output) noexcept {
    if (value.empty()) {
        return false;
    }
    std::uint32_t parsed = 0;
    for (const auto character : value) {
        if (character < '0' || character > '9') {
            return false;
        }
        parsed = parsed * 10U +
                 static_cast<std::uint32_t>(character - '0');
    }
    output = parsed;
    return true;
}

[[nodiscard]] std::optional<std::chrono::system_clock::time_point>
parse_timestamp(std::string_view timestamp) {
    if (timestamp.size() < 20U ||
        timestamp[4] != '-' || timestamp[7] != '-' ||
        timestamp[10] != 'T' || timestamp[13] != ':' ||
        timestamp[16] != ':' ||
        timestamp.back() != 'Z') {
        return std::nullopt;
    }

    std::uint32_t year = 0;
    std::uint32_t month = 0;
    std::uint32_t day = 0;
    std::uint32_t hour = 0;
    std::uint32_t minute = 0;
    std::uint32_t second = 0;
    if (!parse_decimal(timestamp.substr(0, 4), year) ||
        !parse_decimal(timestamp.substr(5, 2), month) ||
        !parse_decimal(timestamp.substr(8, 2), day) ||
        !parse_decimal(timestamp.substr(11, 2), hour) ||
        !parse_decimal(timestamp.substr(14, 2), minute) ||
        !parse_decimal(timestamp.substr(17, 2), second)) {
        return std::nullopt;
    }

    if (year < 1970U || year > 9999U ||
        month < 1U || month > 12U ||
        day < 1U || day > days_in_month(static_cast<std::int64_t>(year),
                                        month) ||
        hour > 23U || minute > 59U || second > 59U) {
        return std::nullopt;
    }

    std::uint64_t fractional_nanoseconds = 0;
    if (timestamp.size() > 20U) {
        if (timestamp[19] != '.' || timestamp.size() > 30U) {
            return std::nullopt;
        }
        const auto fraction =
            timestamp.substr(20, timestamp.size() - 21U);
        if (fraction.empty() || fraction.size() > 9U) {
            return std::nullopt;
        }

        std::uint32_t parsed_fraction = 0;
        if (!parse_decimal(fraction, parsed_fraction)) {
            return std::nullopt;
        }
        std::uint64_t scale = 1U;
        for (std::size_t i = fraction.size(); i < 9U; ++i) {
            scale *= 10U;
        }
        fractional_nanoseconds =
            static_cast<std::uint64_t>(parsed_fraction) * scale;
    }

    const auto days =
        days_from_civil(static_cast<std::int64_t>(year), month, day);
    const auto seconds =
        static_cast<std::int64_t>(days) * 86400 +
        static_cast<std::int64_t>(hour) * 3600 +
        static_cast<std::int64_t>(minute) * 60 +
        static_cast<std::int64_t>(second);
    const auto duration =
        std::chrono::duration_cast<std::chrono::system_clock::duration>(
            std::chrono::seconds{seconds} +
            std::chrono::nanoseconds{fractional_nanoseconds});
    return std::chrono::system_clock::time_point{duration};
}

[[nodiscard]] ValidationResult rejection(
    ValidationError error,
    std::string detail) {
    ValidationResult result;
    result.error = error;
    result.detail = std::move(detail);
    return result;
}

[[nodiscard]] std::string source_state_digest(
    const InboundMessage& message) {
    return sha256_hex(canonical_core(message));
}

}  // namespace

std::string_view to_string(ValidationError error) noexcept {
    switch (error) {
        case ValidationError::none:
            return "none";
        case ValidationError::malformed_type:
            return "malformed_type";
        case ValidationError::unsupported_type:
            return "unsupported_type";
        case ValidationError::malformed_source:
            return "malformed_source";
        case ValidationError::malformed_destination:
            return "malformed_destination";
        case ValidationError::address_mismatch:
            return "address_mismatch";
        case ValidationError::unknown_source:
            return "unknown_source";
        case ValidationError::source_type_not_allowed:
            return "source_type_not_allowed";
        case ValidationError::malformed_timestamp:
            return "malformed_timestamp";
        case ValidationError::stale_timestamp:
            return "stale_timestamp";
        case ValidationError::future_timestamp:
            return "future_timestamp";
        case ValidationError::invalid_sequence:
            return "invalid_sequence";
        case ValidationError::sequence_rollback:
            return "sequence_rollback";
        case ValidationError::duplicate_message:
            return "duplicate_message";
        case ValidationError::invalid_charset:
            return "invalid_charset";
        case ValidationError::message_too_long:
            return "message_too_long";
        case ValidationError::payload_too_long:
            return "payload_too_long";
        case ValidationError::instruction_injection:
            return "instruction_injection";
        case ValidationError::missing_signature:
            return "missing_signature";
        case ValidationError::invalid_signature:
            return "invalid_signature";
        case ValidationError::confirmation_required:
            return "confirmation_required";
        case ValidationError::invalid_confirmation:
            return "invalid_confirmation";
    }
    return "unknown";
}

std::string sha256_hex(std::string_view data) {
    const std::vector<std::uint8_t> bytes(data.begin(), data.end());
    return sha256_hex(std::span<const std::uint8_t>(bytes));
}

std::string sha256_hex(std::span<const std::uint8_t> data) {
    return hex_encode(sha256(data));
}

std::string hmac_sha256_hex(
    std::span<const std::uint8_t> key,
    std::string_view data) {
    constexpr std::size_t kBlockSize = 64U;
    std::vector<std::uint8_t> normalized_key(key.begin(), key.end());
    if (normalized_key.size() > kBlockSize) {
        const auto digest = sha256(normalized_key);
        normalized_key.assign(digest.begin(), digest.end());
    }
    normalized_key.resize(kBlockSize, 0U);

    std::vector<std::uint8_t> inner_pad(kBlockSize);
    std::vector<std::uint8_t> outer_pad(kBlockSize);
    for (std::size_t i = 0; i < kBlockSize; ++i) {
        inner_pad[i] = normalized_key[i] ^ 0x36U;
        outer_pad[i] = normalized_key[i] ^ 0x5cU;
    }

    std::vector<std::uint8_t> inner_buffer;
    inner_buffer.reserve(kBlockSize + data.size());
    inner_buffer.insert(
        inner_buffer.end(), inner_pad.begin(), inner_pad.end());
    inner_buffer.insert(
        inner_buffer.end(), data.begin(), data.end());
    const auto inner_digest = sha256(inner_buffer);

    std::vector<std::uint8_t> outer_buffer;
    outer_buffer.reserve(kBlockSize + inner_digest.size());
    outer_buffer.insert(
        outer_buffer.end(), outer_pad.begin(), outer_pad.end());
    outer_buffer.insert(
        outer_buffer.end(), inner_digest.begin(), inner_digest.end());
    return hex_encode(sha256(outer_buffer));
}

std::string hmac_sha256_hex(
    std::string_view key,
    std::string_view data) {
    const std::vector<std::uint8_t> bytes(key.begin(), key.end());
    return hmac_sha256_hex(
        std::span<const std::uint8_t>(bytes), data);
}

std::string canonical_signing_input(const InboundMessage& message) {
    return canonical_full(message);
}

std::string message_digest(const InboundMessage& message) {
    return sha256_hex(canonical_core(message));
}

std::string sign_message(
    const InboundMessage& message,
    std::span<const std::uint8_t> key) {
    return hmac_sha256_hex(key, canonical_full(message));
}

std::string sign_message(
    const InboundMessage& message,
    std::string_view key) {
    return hmac_sha256_hex(key, canonical_full(message));
}

std::string make_confirmation_signature(
    const InboundMessage& message,
    std::span<const std::uint8_t> key) {
    return hmac_sha256_hex(key, confirmation_input(message));
}

std::string make_confirmation_signature(
    const InboundMessage& message,
    std::string_view key) {
    return hmac_sha256_hex(key, confirmation_input(message));
}

bool verify_message_signature(
    const InboundMessage& message,
    std::string_view key) {
    const auto expected = sign_message(message, key);
    return constant_time_equal(expected, ascii_lower(message.signature));
}

bool constant_time_equal(
    std::string_view left,
    std::string_view right) noexcept {
    if (left.size() != right.size()) {
        return false;
    }

    std::uint8_t difference = 0U;
    for (std::size_t i = 0; i < left.size(); ++i) {
        difference |= static_cast<std::uint8_t>(
            static_cast<unsigned char>(left[i]) ^
            static_cast<unsigned char>(right[i]));
    }
    return difference == 0U;
}

MessageSource::MessageSource(SourcePolicy policy, Clock clock)
    : policy_(std::move(policy)), clock_(std::move(clock)) {
    if (!clock_) {
        clock_ = [] { return std::chrono::system_clock::now(); };
    }
    if (!policy_.local_address.empty()) {
        policy_.local_address = ascii_upper(policy_.local_address);
    }

    auto normalize_types = [](std::set<std::string> types) {
        std::set<std::string> normalized;
        for (auto& type : types) {
            normalized.insert(ascii_upper(type));
        }
        return normalized;
    };
    policy_.allowed_types = normalize_types(policy_.allowed_types);
    policy_.critical_types = normalize_types(policy_.critical_types);
    policy_.weather_types = normalize_types(policy_.weather_types);
}

MessageSource::MessageSource(MessageSource&& other) {
    std::lock_guard<std::mutex> lock(other.mutex_);
    policy_ = std::move(other.policy_);
    clock_ = std::move(other.clock_);
    sources_ = std::move(other.sources_);
}

MessageSource& MessageSource::operator=(MessageSource&& other) {
    if (this == &other) {
        return *this;
    }

    std::scoped_lock lock(mutex_, other.mutex_);
    policy_ = std::move(other.policy_);
    clock_ = std::move(other.clock_);
    sources_ = std::move(other.sources_);
    return *this;
}

bool MessageSource::add_source(TrustedSource source) {
    const auto address = ascii_upper(source.address);
    if (!valid_identifier(address, policy_.max_address_length) ||
        source.key.empty()) {
        return false;
    }

    std::set<std::string> allowed_types;
    for (const auto& type : source.allowed_types) {
        const auto normalized = ascii_upper(type);
        if (!valid_identifier(normalized, policy_.max_type_length)) {
            return false;
        }
        allowed_types.insert(normalized);
    }

    std::lock_guard<std::mutex> lock(mutex_);
    if (sources_.find(address) != sources_.end()) {
        return false;
    }

    SourceState state;
    state.key = std::move(source.key);
    state.allowed_types = std::move(allowed_types);
    state.last_sequence = source.initial_sequence;
    sources_.emplace(address, std::move(state));
    return true;
}

bool MessageSource::add_source(
    std::string_view address,
    std::string_view key) {
    TrustedSource source{
        std::string(address),
        key,
    };
    return add_source(std::move(source));
}

bool MessageSource::register_source(TrustedSource source) {
    return add_source(std::move(source));
}

bool MessageSource::register_source(
    std::string_view address,
    std::string_view key) {
    return add_source(address, key);
}

bool MessageSource::upsert_source(TrustedSource source) {
    const auto address = ascii_upper(source.address);
    if (!valid_identifier(address, policy_.max_address_length) ||
        source.key.empty()) {
        return false;
    }

    std::set<std::string> allowed_types;
    for (const auto& type : source.allowed_types) {
        const auto normalized = ascii_upper(type);
        if (!valid_identifier(normalized, policy_.max_type_length)) {
            return false;
        }
        allowed_types.insert(normalized);
    }

    std::lock_guard<std::mutex> lock(mutex_);
    auto position = sources_.find(address);
    if (position == sources_.end()) {
        SourceState state;
        state.key = std::move(source.key);
        state.allowed_types = std::move(allowed_types);
        state.last_sequence = source.initial_sequence;
        sources_.emplace(address, std::move(state));
        return true;
    }

    position->second.key = std::move(source.key);
    position->second.allowed_types = std::move(allowed_types);
    return true;
}

bool MessageSource::remove_source(std::string_view address) {
    const auto normalized = ascii_upper(address);
    std::lock_guard<std::mutex> lock(mutex_);
    return sources_.erase(normalized) != 0U;
}

bool MessageSource::has_source(std::string_view address) const {
    const auto normalized = ascii_upper(address);
    std::lock_guard<std::mutex> lock(mutex_);
    return sources_.find(normalized) != sources_.end();
}

std::size_t MessageSource::source_count() const {
    std::lock_guard<std::mutex> lock(mutex_);
    return sources_.size();
}

ValidationResult MessageSource::accept(const InboundMessage& message) {
    std::lock_guard<std::mutex> lock(mutex_);
    return accept_impl(message, nullptr);
}

ValidationResult MessageSource::accept(
    const InboundMessage& message,
    const ConfirmationProof& proof) {
    std::lock_guard<std::mutex> lock(mutex_);
    return accept_impl(message, &proof);
}

ValidationResult MessageSource::accept_impl(
    InboundMessage message,
    const ConfirmationProof* proof) {
    message.type = ascii_upper(message.type);
    message.source = ascii_upper(message.source);
    message.destination = ascii_upper(message.destination);
    message.confirmation_signature =
        ascii_lower(message.confirmation_signature);
    message.signature = ascii_lower(message.signature);

    if (proof != nullptr) {
        if (!proof->reference.empty()) {
            if (!message.confirmation.empty() &&
                message.confirmation != proof->reference) {
                return rejection(
                    ValidationError::invalid_confirmation,
                    "confirmation reference does not match the message");
            }
            message.confirmation = proof->reference;
        }
        if (!proof->signature.empty()) {
            const auto normalized_proof_signature =
                ascii_lower(proof->signature);
            if (!message.confirmation_signature.empty() &&
                message.confirmation_signature != normalized_proof_signature) {
                return rejection(
                    ValidationError::invalid_confirmation,
                    "conflicting confirmation signatures");
            }
            message.confirmation_signature = normalized_proof_signature;
        }
    }

    if (!valid_identifier(message.type, policy_.max_type_length)) {
        return rejection(
            ValidationError::malformed_type,
            "type must be 1-16 characters from [A-Z0-9_-]");
    }

    if (policy_.allowed_types.find(message.type) ==
        policy_.allowed_types.end()) {
        return rejection(
            ValidationError::unsupported_type,
            "message type is not enabled by policy: " + message.type);
    }

    if (!valid_identifier(message.source, policy_.max_address_length)) {
        return rejection(
            ValidationError::malformed_source,
            "source address has an invalid format");
    }

    if (!valid_identifier(
            message.destination,
            policy_.max_address_length)) {
        return rejection(
            ValidationError::malformed_destination,
            "destination address has an invalid format");
    }

    if (!policy_.local_address.empty() &&
        message.destination != policy_.local_address) {
        return rejection(
            ValidationError::address_mismatch,
            "message is not addressed to this operational control");
    }

    if (message.sequence == 0U) {
        return rejection(
            ValidationError::invalid_sequence,
            "sequence number zero is not valid");
    }

    if (!valid_payload(
            message.payload,
            policy_.max_payload_length,
            policy_.allow_multiline_payload)) {
        if (message.payload.size() > policy_.max_payload_length) {
            return rejection(
                ValidationError::payload_too_long,
                "payload exceeds the configured length limit");
        }
        return rejection(
            ValidationError::invalid_charset,
            "payload contains a non-printable or non-ASCII character");
    }

    if (!message.confirmation.empty() &&
        !valid_confirmation_reference(
            message.confirmation,
            policy_.max_confirmation_length)) {
        return rejection(
            ValidationError::invalid_confirmation,
            "confirmation reference is empty, too long, or non-ASCII");
    }

    const std::size_t total_length =
        message.type.size() + message.source.size() +
        message.destination.size() + message.timestamp.size() +
        message.payload.size() + message.confirmation.size() +
        message.confirmation_signature.size() + message.signature.size();
    if (total_length > policy_.max_message_length) {
        return rejection(
            ValidationError::message_too_long,
            "message exceeds the configured total length limit");
    }

    if (!message.signature.empty() &&
        !valid_hex_signature(message.signature)) {
        return rejection(
            ValidationError::invalid_signature,
            "signature must be a 64-digit hexadecimal HMAC-SHA-256");
    }
    if (!message.confirmation_signature.empty() &&
        !valid_hex_signature(message.confirmation_signature)) {
        return rejection(
            ValidationError::invalid_confirmation,
            "confirmation signature must be 64 hexadecimal digits");
    }

    const auto timestamp = parse_timestamp(message.timestamp);
    if (!timestamp.has_value()) {
        return rejection(
            ValidationError::malformed_timestamp,
            "timestamp must be an ISO-8601 UTC YYYY-MM-DDTHH:MM:SSZ value");
    }

    const auto now = clock_();
    if (*timestamp > now + policy_.max_future_skew) {
        return rejection(
            ValidationError::future_timestamp,
            "message timestamp is too far in the future");
    }
    if (now - *timestamp > policy_.max_age) {
        return rejection(
            ValidationError::stale_timestamp,
            "message timestamp is outside the freshness window");
    }

    auto source = sources_.find(message.source);
    if (source == sources_.end()) {
        return rejection(
            ValidationError::unknown_source,
            "source address is not present in the trusted registry");
    }

    const auto& state = source->second;
    if (!state.allowed_types.empty() &&
        state.allowed_types.find(message.type) ==
            state.allowed_types.end()) {
        return rejection(
            ValidationError::source_type_not_allowed,
            "trusted source is not authorized for this message type");
    }

    const auto digest = source_state_digest(message);
    if (std::find(
            state.recent_digests.begin(),
            state.recent_digests.end(),
            digest) != state.recent_digests.end()) {
        return rejection(
            ValidationError::duplicate_message,
            "this message content was already accepted");
    }
    if (message.sequence <= state.last_sequence) {
        return rejection(
            ValidationError::sequence_rollback,
            "sequence is not strictly newer than the last accepted value");
    }

    const bool critical =
        policy_.critical_types.find(message.type) !=
        policy_.critical_types.end();

    const bool instruction_sensitive =
        !critical ||
        policy_.weather_types.find(message.type) !=
            policy_.weather_types.end();
    if (policy_.reject_instruction_in_noncritical && instruction_sensitive &&
        contains_critical_instruction(message.payload)) {
        return rejection(
            ValidationError::instruction_injection,
            "non-critical message contains an operational instruction");
    }

    if (policy_.require_signature || critical) {
        if (message.signature.empty()) {
            return rejection(
                ValidationError::missing_signature,
                "a ground message signature is required");
        }
    }

    if (!message.signature.empty() &&
        !constant_time_equal(
            sign_message(message, state.key),
            message.signature)) {
        return rejection(
            ValidationError::invalid_signature,
            "message signature verification failed");
    }

    if (critical && policy_.require_critical_confirmation) {
        if (message.confirmation.empty()) {
            return rejection(
                ValidationError::confirmation_required,
                "critical instruction requires a second confirmation");
        }
        if (policy_.require_critical_confirmation_signature &&
            message.confirmation_signature.empty()) {
            return rejection(
                ValidationError::confirmation_required,
                "critical instruction requires a signed second confirmation");
        }
        if (!message.confirmation_signature.empty() &&
            !constant_time_equal(
                make_confirmation_signature(message, state.key),
                message.confirmation_signature)) {
            return rejection(
                ValidationError::invalid_confirmation,
                "confirmation signature verification failed");
        }
    }

    auto& mutable_source = sources_.at(message.source);
    mutable_source.last_sequence = message.sequence;
    if (policy_.replay_window > 0U) {
        mutable_source.recent_digests.push_back(digest);
        while (mutable_source.recent_digests.size() >
               policy_.replay_window) {
            mutable_source.recent_digests.pop_front();
        }
    }

    TrustedMessage trusted;
    trusted.type = std::move(message.type);
    trusted.source = std::move(message.source);
    trusted.destination = std::move(message.destination);
    trusted.timestamp = std::move(message.timestamp);
    trusted.sequence = message.sequence;
    trusted.payload = std::move(message.payload);
    trusted.issued_at = *timestamp;
    trusted.received_at = now;
    trusted.critical = critical;

    ValidationResult result;
    result.accepted = true;
    result.message = std::move(trusted);
    return result;
}

}  // namespace netsec
