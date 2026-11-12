#pragma once

#include <cstddef>
#include <cstdint>
#include <optional>
#include <vector>

// Embeddable TLS 1.3 inner-plaintext padding / unpadding library.
//
// Inner plaintext layout (RFC 8446, section 5.2):
//
//   struct {
//       opaque content[TLSPlaintext.length];
//       ContentType type;
//       uint8 zeros[padding_length];
//   } TLSInnerPlaintext;
//
// The sender appends the content type and zero padding so that the total
// length is a multiple of 256 bytes. Records carrying inner plaintexts of
// the same padded size are indistinguishable by record length.
namespace netsec {

// Padding block size: every padded inner plaintext is a multiple of this.
inline constexpr std::size_t kBlockSize = 256;

// RFC 8446 section 5.4: the inner plaintext (including the type byte and
// padding) MUST NOT exceed 2^14 + 1 octets.
inline constexpr std::size_t kMaxInnerPlaintextLength = (1u << 14) + 1;

// Largest multiple of kBlockSize that satisfies the limit above: 16384.
// This is the maximum length ever produced by the sender.
inline constexpr std::size_t kMaxPaddedLength = 1u << 14;

// Maximum caller-supplied content length (one octet is reserved for type).
inline constexpr std::size_t kMaxContentLength = kMaxPaddedLength - 1;

enum class PadError {
    kOk = 0,
    kNullInput,
    kNullOutput,
    kZeroType,
    kContentTooLong,
    kOutputTooSmall,
};

enum class UnpadError {
    kOk = 0,
    // Null pointer or zero-length record buffer.
    kNullOrEmpty,
    // Every octet is zero: no content type exists. RFC 8446 requires the
    // connection to be terminated with an unexpected_message alert.
    kAllZeroInner,
    // Only reported in strict content-type mode.
    kUnknownContentType,
};

struct UnpadView {
    // Points into the record buffer supplied to unpad(); zero-copy, valid
    // for the lifetime of that buffer.
    const std::uint8_t* content;
    std::size_t content_length;
    std::uint8_t content_type;
};

const char* to_string(PadError error) noexcept;
const char* to_string(UnpadError error) noexcept;

// TLS alert number (RFC 8446 section 6) that should be sent for an unpad
// failure. Returns std::nullopt for kOk; every failure maps to
// unexpected_message (10).
std::optional<std::uint8_t> recommended_alert(UnpadError error) noexcept;

// True for the ContentType values defined for TLS 1.3 / Heartbeat:
// change_cipher_spec(20), alert(21), handshake(22), application_data(23),
// heartbeat(24).
bool is_known_content_type(std::uint8_t content_type) noexcept;

// Returns the padded total length for content_length bytes of content, or 0
// when the result would exceed kMaxPaddedLength (including overflow).
std::size_t padded_length(std::size_t content_length) noexcept;

// Writes content || content_type || zero padding into out. The output is a
// multiple of kBlockSize and never longer than kMaxPaddedLength.
// On success *out_length holds the total written length. The content and out
// buffers must not overlap; use pad_in_place() for that layout.
PadError pad(const std::uint8_t* content,
             std::size_t content_length,
             std::uint8_t content_type,
             std::uint8_t* out,
             std::size_t out_capacity,
             std::size_t* out_length) noexcept;

// Padding variant for the common send-side layout: buffer already contains
// content_length bytes of content and has room for the type byte plus
// padding. The type byte and zeros are appended inside buffer.
PadError pad_in_place(std::uint8_t* buffer,
                      std::size_t content_length,
                      std::size_t buffer_capacity,
                      std::uint8_t content_type,
                      std::size_t* out_length) noexcept;

// Convenience wrapper that allocates the padded record. Returns std::nullopt
// on invalid arguments (null content with non-zero length, zero type,
// oversized content).
std::optional<std::vector<std::uint8_t>>
pad(const std::uint8_t* content,
    std::size_t content_length,
    std::uint8_t content_type);

// Locates the content type as the last non-zero octet of record and exposes
// everything before it as content. The scan touches every octet exactly once
// with data-independent control flow, so the running time depends solely on
// record_length, not on where the type byte sits or whether padding exists.
// An all-zero record is rejected as kAllZeroInner. In strict mode, unknown
// ContentType values are rejected as kUnknownContentType.
UnpadError unpad(const std::uint8_t* record,
                 std::size_t record_length,
                 UnpadView* out,
                 bool strict_content_type = false) noexcept;

}  // namespace netsec
