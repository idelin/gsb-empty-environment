// SPDX-License-Identifier: MIT
// Embeddable DTLS 1.3 (RFC 9147) record-layer security primitives.
//
// Common value types, error codes and big-endian codec helpers.
// Everything in this library is self-contained and depends only on the
// C++17 standard library; AEAD / nonce masking are injected through
// abstract interfaces defined in crypto.hpp.

#ifndef DTLS13_TYPES_HPP
#define DTLS13_TYPES_HPP

#include <cstddef>
#include <cstdint>
#include <string>
#include <vector>

namespace dtls13 {

// Non-owning view over a contiguous byte range.
class ByteView {
public:
    constexpr ByteView() noexcept = default;
    constexpr ByteView(const std::uint8_t* data, std::size_t size) noexcept
        : data_(data), size_(size) {}
    ByteView(const std::vector<std::uint8_t>& bytes) noexcept
        : data_(bytes.data()), size_(bytes.size()) {}

    constexpr const std::uint8_t* data() const noexcept { return data_; }
    constexpr std::size_t size() const noexcept { return size_; }
    constexpr bool empty() const noexcept { return size_ == 0; }
    constexpr std::uint8_t operator[](std::size_t index) const noexcept {
        return data_[index];
    }
    constexpr ByteView subview(std::size_t offset) const noexcept {
        return offset >= size_ ? ByteView()
                               : ByteView(data_ + offset, size_ - offset);
    }
    constexpr ByteView subview(std::size_t offset,
                               std::size_t count) const noexcept {
        if (offset >= size_) {
            return ByteView();
        }
        const std::size_t available = size_ - offset;
        return ByteView(data_ + offset,
                        count < available ? count : available);
    }

private:
    const std::uint8_t* data_ = nullptr;
    std::size_t size_ = 0;
};

using Bytes = std::vector<std::uint8_t>;

// TLS/DTLS content types (RFC 8446 section 5.1, RFC 9147).
enum class ContentType : std::uint8_t {
    Invalid = 0,
    ChangeCipherSpec = 20,
    Alert = 21,
    Handshake = 22,
    ApplicationData = 23,
    Ack = 25,  // DTLS 1.3 ACK message is carried in a Handshake record.
};

// Operational error categories. Every public entry point returns one of
// these codes; none of them throws across the library boundary.
enum class ErrorCode {
    Ok = 0,
    MalformedRecord,       // Truncated or syntactically invalid record.
    UnsupportedHeader,     // Reserved/pattern bits that must be zero.
    TruncatedDatagram,     // Datagram ends before the declared record ends.
    AeadDecryptFailed,     // Authentication tag verification failed.
    Replayed,              // Record is a duplicate or behind the window.
    StaleEpoch,            // Plaintext record from an older epoch.
    UnknownEpoch,          // No keying material exists for this epoch.
    EpochLimitReached,     // Too many distinct epochs tracked.
    FragmentOverflow,      // offset + length exceeds total_length.
    MessageTooLarge,       // Reassembled message exceeds the configured cap.
    TooManyMessages,       // Open reassembly slot budget exhausted.
    OverlapConflict,       // Overlapping bytes disagree (injection attempt).
    InconsistentHeader,    // msg_type / total_length changed for message_seq.
    ReassemblyPoisoned,    // Slot already flagged as attacked.
    BadCookie,             // Cookie missing, malformed, expired or replayed.
    BadArgument,
};

const char* toString(ErrorCode code) noexcept;

// Result wrapper used by fallible operations that also produce a value.
template <typename T>
struct Result {
    ErrorCode code = ErrorCode::Ok;
    T value{};

    Result() = default;
    Result(ErrorCode error_code) : code(error_code) {}  // NOLINT(google-explicit-constructor)
    Result(T result_value)  // NOLINT(google-explicit-constructor)
        : value(std::move(result_value)) {}

    explicit operator bool() const noexcept { return code == ErrorCode::Ok; }
    bool ok() const noexcept { return code == ErrorCode::Ok; }
};

// ---------------------------------------------------------------------------
// Big-endian codec helpers. Every write checks capacity explicitly so the
// library never performs out-of-bounds accesses on hostile input.
// ---------------------------------------------------------------------------

inline std::uint16_t readBe16(const std::uint8_t* p) noexcept {
    return static_cast<std::uint16_t>(
        static_cast<std::uint16_t>(p[0] << 8) |
        static_cast<std::uint16_t>(p[1]));
}

inline std::uint32_t readBe24(const std::uint8_t* p) noexcept {
    return static_cast<std::uint32_t>(
        static_cast<std::uint32_t>(p[0] << 16) |
        static_cast<std::uint32_t>(p[1] << 8) |
        static_cast<std::uint32_t>(p[2]));
}

inline std::uint32_t readBe32(const std::uint8_t* p) noexcept {
    return static_cast<std::uint32_t>(
        static_cast<std::uint32_t>(p[0] << 24) |
        static_cast<std::uint32_t>(p[1] << 16) |
        static_cast<std::uint32_t>(p[2] << 8) |
        static_cast<std::uint32_t>(p[3]));
}

// 48-bit DTLS sequence number.
inline std::uint64_t readBe48(const std::uint8_t* p) noexcept {
    std::uint64_t value = 0;
    for (int i = 0; i < 6; ++i) {
        value = (value << 8) | static_cast<std::uint64_t>(p[i]);
    }
    return value;
}

inline void writeBe16(std::uint8_t* p, std::uint16_t value) noexcept {
    p[0] = static_cast<std::uint8_t>(value >> 8);
    p[1] = static_cast<std::uint8_t>(value);
}

inline void writeBe24(std::uint8_t* p, std::uint32_t value) noexcept {
    p[0] = static_cast<std::uint8_t>(value >> 16);
    p[1] = static_cast<std::uint8_t>(value >> 8);
    p[2] = static_cast<std::uint8_t>(value);
}

inline void writeBe32(std::uint8_t* p, std::uint32_t value) noexcept {
    p[0] = static_cast<std::uint8_t>(value >> 24);
    p[1] = static_cast<std::uint8_t>(value >> 16);
    p[2] = static_cast<std::uint8_t>(value >> 8);
    p[3] = static_cast<std::uint8_t>(value);
}

inline void writeBe48(std::uint8_t* p, std::uint64_t value) noexcept {
    for (int i = 5; i >= 0; --i) {
        p[i] = static_cast<std::uint8_t>(value);
        value >>= 8;
    }
}

inline void writeBe64(std::uint8_t* p, std::uint64_t value) noexcept {
    for (int i = 7; i >= 0; --i) {
        p[i] = static_cast<std::uint8_t>(value);
        value >>= 8;
    }
}

// Constant-time comparison for authentication tags and cookies.
// Returns true only when both buffers are equal and the same length.
bool constantTimeEqual(ByteView lhs, ByteView rhs) noexcept;

}  // namespace dtls13

#endif  // DTLS13_TYPES_HPP
