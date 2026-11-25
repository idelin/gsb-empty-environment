#ifndef TLV_READER_H
#define TLV_READER_H

#include <cstddef>
#include <cstdint>
#include <functional>
#include <vector>

namespace tlv {

// Error that put the reader into its terminal state.
enum class Error {
    None,
    NegativeLength,     // signed length field had its sign bit set
    ArithmeticOverflow, // size_t accumulation would wrap around
    TruncatedRecord,    // a top-level record ends before its declared length
    ChildTruncated,     // parent value ends in the middle of a child record
    ChildOverrun,       // child record extends past the parent value region
    ChildLeftover,      // parent value is not exactly filled by child records
    DepthExceeded,      // nesting went deeper than kMaxDepth
    QuotaExceeded       // cumulative stream bytes exceeded the caller's budget
};

// Pre-order record callback. The value view is valid only for the duration of
// the call and points into the reader's internal reassembly buffer.
using RecordCallback =
    std::function<void(uint16_t type, const uint8_t* value, size_t length)>;

// Incremental reader for an untrusted big-endian TLV byte stream.
//
// Wire format of one record:
//   uint16 BE type   (bit 0x8000 marks a nested record)
//   int32  BE length (value length; negative values are rejected)
//   uint8  value[length]
//
// A nested record's value must be filled exactly by zero or more complete
// child records. A whole top-level subtree is validated before any callback
// fires; callbacks are delivered in pre-order once validation succeeds.
// Any structural failure makes the reader terminal: no callback is emitted for
// the offending top-level record, committed() stays at its start offset and
// every later feed() is a no-op that returns false.
class Reader {
public:
    static constexpr size_t kHeaderSize = 6;
    static constexpr int kMaxDepth = 8;

    explicit Reader(RecordCallback callback, size_t budget = SIZE_MAX)
        : budget_(budget), callback_(std::move(callback)) {}

    Reader(const Reader&) = delete;
    Reader& operator=(const Reader&) = delete;

    // Feeds one arbitrary network fragment. Returns false once the reader is
    // in its terminal state; fragment boundaries never affect parsing.
    bool feed(const uint8_t* data, size_t size) {
        if (failed()) {
            return false;
        }
        if (size != 0) {
            buffer_.insert(buffer_.end(), data, data + size);
            pump();
        }
        return !failed();
    }

    bool feed(const std::vector<uint8_t>& fragment) {
        return feed(fragment.data(), fragment.size());
    }

    // Stream offset of the last fully validated top-level record boundary.
    // Frozen at the start offset of the record that caused a terminal error.
    size_t committed() const { return committed_; }

    // Cumulative number of stream bytes already checked against the budget.
    size_t charged() const { return charged_; }

    size_t budget() const { return budget_; }
    Error error() const { return error_; }
    bool failed() const { return error_ != Error::None; }

    // Restores the reader to its initial state, optionally with a new budget.
    void reset(size_t budget) {
        buffer_.clear();
        committed_ = 0;
        charged_ = 0;
        budget_ = budget;
        error_ = Error::None;
    }

    void reset() { reset(budget_); }

private:
    // Validates the record beginning at buffer-relative position pos.
    // The root record is depth 1. For a child, value_end is the enclosing
    // value's exclusive buffer-relative end. On success record_end receives
    // the exclusive end of this record.
    bool validateRecord(size_t pos, int depth, bool is_root, size_t value_end,
                        size_t& record_end) {
        size_t region_end = is_root ? buffer_.size() : value_end;
        if (pos > region_end || region_end - pos < kHeaderSize) {
            return fail(is_root ? Error::TruncatedRecord
                                : Error::ChildTruncated);
        }

        // The header bytes are charged against the cumulative budget first.
        if (!chargeBufferRelative(pos + kHeaderSize)) {
            return false;
        }

        uint16_t type = readBe16(pos);
        if (isNegativeLength(pos)) {
            // Covers -1 (0xFFFFFFFF) and INT32_MIN (0x80000000): the field
            // is interpreted as a signed integer before it is ever used.
            return fail(Error::NegativeLength);
        }
        uint32_t length = readBe32(pos + 2);

        size_t total_size = 0;
        if (!addChecked(kHeaderSize, length, total_size)) {
            return fail(Error::ArithmeticOverflow);
        }
        size_t end = 0;
        if (!addChecked(pos, total_size, end)) {
            return fail(Error::ArithmeticOverflow);
        }

        if (end > region_end) {
            // A child declares more bytes than the parent value still owns;
            // the root is bounded by the whole reassembly buffer.
            return fail(is_root ? Error::TruncatedRecord
                                : Error::ChildOverrun);
        }

        // Charging the record end reserves header plus value without wrapping.
        if (!chargeBufferRelative(end)) {
            return false;
        }

        if ((type & 0x8000u) != 0) {
            size_t child_pos = pos + kHeaderSize;
            while (child_pos < end) {
                size_t child_end = 0;
                if (depth + 1 > kMaxDepth) {
                    return fail(Error::DepthExceeded);
                }
                if (!validateRecord(child_pos, depth + 1, false, end,
                                    child_end)) {
                    return false;
                }
                child_pos = child_end;
            }
            if (child_pos != end) {
                return fail(Error::ChildLeftover);
            }
        }

        record_end = end;
        return true;
    }

    // Emits an already validated record subtree in pre-order.
    void emitRecord(size_t pos, size_t record_end) {
        uint16_t type = readBe16(pos);
        uint32_t length = readBe32(pos + 2);
        const uint8_t* value = buffer_.data() + pos + kHeaderSize;
        callback_(type, value, static_cast<size_t>(length));

        if ((type & 0x8000u) != 0) {
            size_t child_pos = pos + kHeaderSize;
            while (child_pos < record_end) {
                uint32_t child_length = readBe32(child_pos + 2);
                size_t child_total =
                    kHeaderSize + static_cast<size_t>(child_length);
                size_t child_end = child_pos + child_total;
                emitRecord(child_pos, child_end);
                child_pos = child_end;
            }
        }
    }

    // Drains as many complete, fully validated top-level records as possible.
    void pump() {
        while (!failed()) {
            if (buffer_.size() < kHeaderSize) {
                return; // wait for the rest of a split header
            }

            if (isNegativeLength(0)) {
                fail(Error::NegativeLength);
                return;
            }

            uint32_t length = readBe32(2);
            size_t total_size = 0;
            if (!addChecked(kHeaderSize, length, total_size)) {
                fail(Error::ArithmeticOverflow);
                return;
            }

            if (total_size > buffer_.size()) {
                // Wait for the remaining value bytes. All budget accounting
                // happens in validateRecord() once the record is complete, so
                // a still-in-flight record simply stays pending.
                return;
            }

            size_t record_end = 0;
            if (!validateRecord(0, 1, true, 0, record_end)) {
                return;
            }

            emitRecord(0, record_end);

            committed_ += record_end;
            buffer_.erase(buffer_.begin(),
                          buffer_.begin() +
                              static_cast<std::ptrdiff_t>(record_end));
        }
    }

    // Marks the absolute stream offset absolute_end as consumed. Offsets
    // inside the record currently being validated are reached repeatedly (a
    // parent region contains its children), so only forward progress charges
    // additional cumulative budget.
    bool chargeTo(size_t absolute_end) {
        if (absolute_end < committed_) {
            // Before the last committed boundary means a position wrapped.
            return fail(Error::ArithmeticOverflow);
        }
        if (absolute_end <= charged_) {
            return true;
        }
        if (absolute_end > budget_) {
            return fail(Error::QuotaExceeded);
        }
        charged_ = absolute_end;
        return true;
    }

    bool chargeBufferRelative(size_t relative_end) {
        size_t absolute_end = 0;
        if (!addChecked(committed_, relative_end, absolute_end)) {
            return fail(Error::ArithmeticOverflow);
        }
        return chargeTo(absolute_end);
    }

    static bool addChecked(size_t a, size_t b, size_t& result) {
        if (a > SIZE_MAX - b) {
            return false;
        }
        result = a + b;
        return true;
    }

    bool isNegativeLength(size_t pos) const {
        return (buffer_[pos + 2] & 0x80u) != 0;
    }

    uint16_t readBe16(size_t pos) const {
        return static_cast<uint16_t>(
            (static_cast<uint16_t>(buffer_[pos]) << 8) |
            static_cast<uint16_t>(buffer_[pos + 1]));
    }

    uint32_t readBe32(size_t pos) const {
        return (static_cast<uint32_t>(buffer_[pos]) << 24) |
               (static_cast<uint32_t>(buffer_[pos + 1]) << 16) |
               (static_cast<uint32_t>(buffer_[pos + 2]) << 8) |
               static_cast<uint32_t>(buffer_[pos + 3]);
    }

    bool fail(Error error) {
        error_ = error;
        return false;
    }

    std::vector<uint8_t> buffer_;
    size_t committed_ = 0;
    size_t charged_ = 0;
    size_t budget_;
    Error error_ = Error::None;
    RecordCallback callback_;
};

} // namespace tlv

#endif // TLV_READER_H
