#include "netsec.hpp"

#include <cstring>

namespace netsec {

namespace {

// Returns a full-width mask: all bits zero when v == 0, all bits set
// otherwise, without branching on v.
constexpr std::size_t nonzero_mask(std::uint8_t v) noexcept {
    const std::size_t widened = static_cast<std::size_t>(v);
    const std::size_t folded = widened | (0u - widened);
    const std::size_t bit = folded >> (sizeof(std::size_t) * 8u - 1u);
    return 0u - bit;
}

}  // namespace

const char* to_string(PadError error) noexcept {
    switch (error) {
        case PadError::kOk:
            return "ok";
        case PadError::kNullInput:
            return "null input content";
        case PadError::kNullOutput:
            return "null output buffer";
        case PadError::kZeroType:
            return "content type must be non-zero";
        case PadError::kContentTooLong:
            return "inner plaintext would exceed 2^14+1 octets";
        case PadError::kOutputTooSmall:
            return "output buffer too small";
    }
    return "unknown padding error";
}

const char* to_string(UnpadError error) noexcept {
    switch (error) {
        case UnpadError::kOk:
            return "ok";
        case UnpadError::kNullOrEmpty:
            return "null or empty inner plaintext";
        case UnpadError::kAllZeroInner:
            return "all-zero inner plaintext (unexpected_message)";
        case UnpadError::kUnknownContentType:
            return "unknown content type (unexpected_message)";
    }
    return "unknown unpadding error";
}

std::optional<std::uint8_t>
recommended_alert(UnpadError error) noexcept {
    if (error == UnpadError::kOk) {
        return std::nullopt;
    }
    // RFC 8446 section 5.4: zero-length / unrecognized inner plaintext is a
    // fatal unexpected_message alert (alert description 10).
    return 10;
}

bool is_known_content_type(std::uint8_t content_type) noexcept {
    // change_cipher_spec(20), alert(21), handshake(22),
    // application_data(23), heartbeat(24).
    return content_type >= 20 && content_type <= 24;
}

std::size_t padded_length(std::size_t content_length) noexcept {
    if (content_length > kMaxContentLength) {
        return 0;
    }
    const std::size_t total = content_length + 1u;
    const std::size_t padded =
            (total + (kBlockSize - 1u)) & ~(kBlockSize - 1u);
    if (padded > kMaxPaddedLength) {
        return 0;
    }
    return padded;
}

PadError pad(const std::uint8_t* content,
             std::size_t content_length,
             std::uint8_t content_type,
             std::uint8_t* out,
             std::size_t out_capacity,
             std::size_t* out_length) noexcept {
    if (content == nullptr && content_length > 0u) {
        return PadError::kNullInput;
    }
    if (out == nullptr || out_length == nullptr) {
        return PadError::kNullOutput;
    }
    if (content_type == 0u) {
        return PadError::kZeroType;
    }
    const std::size_t total = padded_length(content_length);
    if (total == 0u) {
        return PadError::kContentTooLong;
    }
    if (out_capacity < total) {
        return PadError::kOutputTooSmall;
    }

    if (content_length > 0u) {
        std::memcpy(out, content, content_length);
    }
    out[content_length] = content_type;
    // The fill length is derived solely from the total record size, never
    // from the content payload.
    std::memset(out + content_length + 1u, 0, total - content_length - 1u);
    *out_length = total;
    return PadError::kOk;
}

PadError pad_in_place(std::uint8_t* buffer,
                      std::size_t content_length,
                      std::size_t buffer_capacity,
                      std::uint8_t content_type,
                      std::size_t* out_length) noexcept {
    if (buffer == nullptr) {
        return PadError::kNullInput;
    }
    if (out_length == nullptr) {
        return PadError::kNullOutput;
    }
    if (content_type == 0u) {
        return PadError::kZeroType;
    }
    const std::size_t total = padded_length(content_length);
    if (total == 0u) {
        return PadError::kContentTooLong;
    }
    if (buffer_capacity < total) {
        return PadError::kOutputTooSmall;
    }

    buffer[content_length] = content_type;
    std::memset(buffer + content_length + 1u, 0,
                total - content_length - 1u);
    *out_length = total;
    return PadError::kOk;
}

std::optional<std::vector<std::uint8_t>>
pad(const std::uint8_t* content,
    std::size_t content_length,
    std::uint8_t content_type) {
    if (content == nullptr && content_length > 0u) {
        return std::nullopt;
    }
    if (content_type == 0u) {
        return std::nullopt;
    }
    const std::size_t total = padded_length(content_length);
    if (total == 0u) {
        return std::nullopt;
    }

    std::vector<std::uint8_t> record(total, 0u);
    if (content_length > 0u) {
        std::memcpy(record.data(), content, content_length);
    }
    record[content_length] = content_type;
    return record;
}

UnpadError unpad(const std::uint8_t* record,
                 std::size_t record_length,
                 UnpadView* out,
                 bool strict_content_type) noexcept {
    if (record == nullptr || record_length == 0u || out == nullptr) {
        return UnpadError::kNullOrEmpty;
    }

    // Branchless, single-pass scan over the whole record. Both the number of
    // iterations and the per-octet operations are independent of the data,
    // so wall time varies with record_length but not with where the type
    // byte sits or how much padding is present.
    std::size_t last_nonzero = 0u;
    std::uint8_t type_candidate = 0u;
    for (std::size_t i = 0u; i < record_length; ++i) {
        const std::uint8_t octet = record[i];
        const std::size_t select = nonzero_mask(octet);
        const std::size_t keep = ~select;

        // last_nonzero = nz ? i : last_nonzero
        last_nonzero = (last_nonzero & keep) | (i & select);
        // type_candidate = nz ? octet : type_candidate
        type_candidate = static_cast<std::uint8_t>(
                (type_candidate & static_cast<std::uint8_t>(keep)) |
                (octet & static_cast<std::uint8_t>(select)));
    }

    // type_candidate stays zero only when every octet was zero. Such inner
    // plaintexts carry no content type and must be rejected.
    if (type_candidate == 0u) {
        return UnpadError::kAllZeroInner;
    }
    if (strict_content_type &&
        !is_known_content_type(type_candidate)) {
        return UnpadError::kUnknownContentType;
    }

    out->content = record;
    out->content_length = last_nonzero;
    out->content_type = type_candidate;
    return UnpadError::kOk;
}

}  // namespace netsec
