#pragma once

#include <cstddef>
#include <cstdint>
#include <functional>
#include <utility>
#include <vector>

namespace netsec {

// Incremental reader for an untrusted nested TLV stream.
//
// Record layout: 2-byte big-endian type, 4-byte big-endian *signed* length,
// then `length` value bytes. A type with the most significant bit set is
// nested: its value must be exactly filled by zero or more complete child
// records. Nesting depth is capped (top-level record is depth 0).
//
// A top-level record's callbacks (pre-order) fire only after its whole
// subtree has been validated. On any malformed record the reader enters a
// terminal state: the offending top-level record produces zero callbacks,
// committed() stays at that record's start, and later feed() calls are
// no-ops. The callback sequence and final state do not depend on how the
// stream is split into feed() chunks.
class tlv_reader {
public:
    static constexpr std::size_t kHeaderSize = 6;  // 2-byte type + 4-byte length
    static constexpr unsigned kMaxDepth = 8;
    static constexpr std::uint16_t kNestedFlag = 0x8000;

    // `value`/`length` describe the record payload; `depth` is 0 for the
    // top-level record. `value` points into an internal buffer and is only
    // valid for the duration of the call.
    using Callback = std::function<void(std::uint16_t type,
                                        const std::uint8_t* value,
                                        std::uint32_t length,
                                        unsigned depth)>;

    // `budget` is the total byte allowance (headers + values) for the whole
    // stream; it may be as large as SIZE_MAX.
    tlv_reader(std::size_t budget, Callback cb) : budget_(budget), cb_(std::move(cb)) {}

    tlv_reader(const tlv_reader&) = delete;
    tlv_reader& operator=(const tlv_reader&) = delete;

    // Feeds one chunk of the stream; any chunking yields the same behavior.
    void feed(const void* data, std::size_t size) {
        if (terminated_) return;
        if (size != 0) {
            const auto* p = static_cast<const std::uint8_t*>(data);
            buf_.insert(buf_.end(), p, p + size);
        }
        drain();
    }

    // Stream offset of the first uncommitted byte: the total size of all
    // fully validated top-level records. Frozen once a record fails.
    std::size_t committed() const { return committed_; }

    // True once a record was rejected; feed() becomes a no-op.
    bool terminated() const { return terminated_; }

private:
    static std::uint16_t type_at(const std::uint8_t* p) {
        return static_cast<std::uint16_t>((static_cast<std::uint16_t>(p[0]) << 8) |
                                          static_cast<std::uint16_t>(p[1]));
    }

    static std::uint32_t len_at(const std::uint8_t* p) {
        return (static_cast<std::uint32_t>(p[0]) << 24) |
               (static_cast<std::uint32_t>(p[1]) << 16) |
               (static_cast<std::uint32_t>(p[2]) << 8) |
               static_cast<std::uint32_t>(p[3]);
    }

    void drain() {
        std::size_t off = 0;
        while (buf_.size() - off >= kHeaderSize) {
            const std::uint8_t* header = buf_.data() + off;
            const std::uint16_t type = type_at(header);
            const std::uint32_t raw = len_at(header + 2);
            // Length is a signed 32-bit value: reject negatives first.
            if (raw & 0x80000000u) {
                terminated_ = true;
                break;
            }
            const std::uint32_t len = raw;

            // Budget check without overflow: committed_ <= budget_ holds and
            // every record before `off` already fit, so the subtractions are
            // safe and the running total can never wrap.
            const std::size_t remaining = budget_ - committed_ - off;
            if (remaining < kHeaderSize ||
                static_cast<std::size_t>(len) > remaining - kHeaderSize) {
                terminated_ = true;
                break;
            }

            // Not enough buffered bytes yet: wait for the next feed().
            if (buf_.size() - off - kHeaderSize < len) break;

            const std::uint8_t* value = header + kHeaderSize;
            if (!validate(value, type, len, 0)) {
                terminated_ = true;
                break;
            }
            emit(value, type, len, 0);
            off += kHeaderSize + len;
        }
        if (off != 0) {
            committed_ += off;
            buf_.erase(buf_.begin(), buf_.begin() + static_cast<std::ptrdiff_t>(off));
        }
    }

    // Validates a record subtree. A nested value must be exactly tiled by
    // complete child records.
    static bool validate(const std::uint8_t* p, std::uint16_t type,
                         std::uint32_t len, unsigned depth) {
        if (depth > kMaxDepth) return false;
        if (!(type & kNestedFlag)) return true;
        std::uint32_t pos = 0;
        while (pos < len) {
            if (len - pos < kHeaderSize) return false;
            const std::uint8_t* child = p + pos;
            const std::uint16_t child_type = type_at(child);
            const std::uint32_t child_raw = len_at(child + 2);
            if (child_raw & 0x80000000u) return false;
            if (child_raw > len - pos - kHeaderSize) return false;
            if (!validate(child + kHeaderSize, child_type, child_raw, depth + 1)) {
                return false;
            }
            pos += kHeaderSize + child_raw;
        }
        return true;
    }

    // Pre-order callback walk; only called on a fully validated subtree.
    void emit(const std::uint8_t* p, std::uint16_t type, std::uint32_t len,
              unsigned depth) {
        cb_(type, p, len, depth);
        if (!(type & kNestedFlag)) return;
        std::uint32_t pos = 0;
        while (pos < len) {
            const std::uint8_t* child = p + pos;
            const std::uint16_t child_type = type_at(child);
            const std::uint32_t child_len = len_at(child + 2);
            emit(child + kHeaderSize, child_type, child_len, depth + 1);
            pos += kHeaderSize + child_len;
        }
    }

    std::size_t budget_;
    Callback cb_;
    std::size_t committed_ = 0;
    bool terminated_ = false;
    std::vector<std::uint8_t> buf_;  // uncommitted bytes only
};

}  // namespace netsec
