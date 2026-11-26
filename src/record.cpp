// SPDX-License-Identifier: MIT

#include "dtls13/record.hpp"

#include <limits>

namespace dtls13 {

namespace {

constexpr std::size_t kMaxRecordBodyDefault = 1u << 16;

bool isKnownContentType(std::uint8_t raw) noexcept {
    // TLS 1.3 reserves 20-25 for real traffic; anything else is rejected
    // by the embedding layer, but syntactically we accept the range.
    return raw >= static_cast<std::uint8_t>(ContentType::ChangeCipherSpec) &&
           raw <= static_cast<std::uint8_t>(ContentType::Ack);
}

}  // namespace

std::uint64_t reconstructSequence(std::uint64_t truncated,
                                  unsigned bits,
                                  std::uint64_t window_end) noexcept {
    // RFC 9147 section 4.5.1, expressed modulo 2^48 so wrap-around cannot
    // trigger unsigned underflow. `mod` is 256 (8-bit) or 65536 (16-bit).
    const std::uint64_t mod = 1ULL << bits;
    const std::uint64_t half = mod / 2;
    const std::uint64_t t = truncated & (mod - 1);

    // Largest value congruent to t modulo mod that is <= window_end.
    const std::uint64_t delta = window_end % mod;
    std::uint64_t candidate = window_end - ((delta - t + mod) % mod);

    // Signed circular distance from window_end, in (-half, half].
    // Positive means candidate is older than window_end.
    std::uint64_t forward_distance = window_end - candidate;  // no underflow
    if (forward_distance >= half) {
        // Tie (== half) resolves to the higher sequence number, matching
        // the RFC pseudocode branch "candidate <= window_end - half".
        candidate = (candidate + mod) & kSeqSpaceMask;
    }
    return candidate & kSeqSpaceMask;
}

ReplayWindow::Verdict ReplayWindow::inspect(std::uint64_t seq48) const
    noexcept {
    if (!initialized_) {
        return Verdict::Fresh;
    }
    // Circular distance from the current high-water mark on the 48-bit
    // ring. `behind == 0` means seq48 == window_end_; a distance of >= 2^47
    // means seq48 is actually *ahead* of window_end_ (forward jump).
    const std::uint64_t behind = (window_end_ - seq48) & kSeqSpaceMask;
    if (behind == 0) {
        return (bitmap_ & 1ULL) ? Verdict::Replayed : Verdict::Fresh;
    }
    if (behind >= kSeqSpaceSize - kReplayWindowBits + 1) {
        // Equivalent to seq48 being 1..63 positions ahead of window_end_.
        return Verdict::Fresh;
    }
    if (behind >= kReplayWindowBits) {
        return Verdict::TooOld;
    }
    return (bitmap_ & (1ULL << static_cast<unsigned>(behind)))
               ? Verdict::Replayed
               : Verdict::Fresh;
}

void ReplayWindow::commit(std::uint64_t seq48) noexcept {
    if (!initialized_) {
        initialized_ = true;
        window_end_ = seq48;
        bitmap_ = 1ULL;
        return;
    }

    const std::uint64_t ahead = (seq48 - window_end_) & kSeqSpaceMask;
    if (ahead == 0) {
        bitmap_ |= 1ULL;
        return;
    }
    if (ahead < kSeqSpaceSize / 2) {
        // New high-water mark: shift the bitmap. A shift of >= 64 would be
        // undefined behavior, so handle it explicitly.
        if (ahead >= kReplayWindowBits) {
            bitmap_ = 0;
        } else {
            bitmap_ <<= static_cast<unsigned>(ahead);
        }
        bitmap_ |= 1ULL;
        window_end_ = seq48;
        return;
    }

    // Older sequence inside the window: mark the corresponding bit.
    const std::uint64_t behind = (window_end_ - seq48) & kSeqSpaceMask;
    if (behind < kReplayWindowBits) {
        bitmap_ |= (1ULL << static_cast<unsigned>(behind));
    }
}

ErrorCode parseOneRecord(ByteView datagram,
                         std::size_t& offset,
                         std::uint16_t current_epoch,
                         ParsedRecord& out) {
    if (offset >= datagram.size()) {
        return ErrorCode::TruncatedDatagram;
    }
    const std::size_t record_start = offset;
    const std::uint8_t type_octet = datagram[offset];
    if (!isKnownContentType(type_octet)) {
        return ErrorCode::MalformedRecord;
    }
    ++offset;
    if (offset >= datagram.size()) {
        return ErrorCode::TruncatedDatagram;
    }

    const std::uint8_t flags = datagram[offset];
    if ((flags & kUhFixedPatternMask) != kUhFixedPattern) {
        return ErrorCode::UnsupportedHeader;
    }
    if ((flags & kUhReservedFlag) != 0) {
        return ErrorCode::UnsupportedHeader;
    }

    UnifiedHeader uh;
    uh.cid_present = (flags & kUhCidFlag) != 0;
    uh.epoch_present = (flags & kUhEpochFlag) != 0;
    uh.seq_16_bits = (flags & kUhSeq16Flag) != 0;
    uh.length_present = (flags & kUhLengthFlag) != 0;
    uh.seq_bits = uh.seq_16_bits ? 16u : 8u;
    ++offset;

    // Connection ID: length comes from the negotiated association. The
    // wire format carries no length, so without a configured CID any
    // record setting B is rejected by the caller's policy. Here we expose
    // the flag and skip no bytes; the record layer rejects CID records.
    if (uh.cid_present) {
        return ErrorCode::UnsupportedHeader;
    }

    if (uh.epoch_present) {
        if (offset + 2 > datagram.size()) {
            return ErrorCode::TruncatedDatagram;
        }
        uh.epoch = readBe16(datagram.data() + offset);
        offset += 2;
    } else {
        // Epoch elision: the record belongs to the current epoch.
        uh.epoch = current_epoch;
    }

    if (uh.seq_16_bits) {
        if (offset + 2 > datagram.size()) {
            return ErrorCode::TruncatedDatagram;
        }
        uh.truncated_seq = readBe16(datagram.data() + offset);
        offset += 2;
    } else {
        if (offset >= datagram.size()) {
            return ErrorCode::TruncatedDatagram;
        }
        uh.truncated_seq = datagram[offset];
        ++offset;
    }

    if (uh.length_present) {
        if (offset + 2 > datagram.size()) {
            return ErrorCode::TruncatedDatagram;
        }
        uh.length = readBe16(datagram.data() + offset);
        uh.has_length = true;
        offset += 2;
    } else {
        uh.has_length = false;
    }

    uh.header_size = offset - record_start;

    std::size_t body_size = 0;
    if (uh.has_length) {
        body_size = uh.length;
        if (offset + body_size > datagram.size()) {
            return ErrorCode::TruncatedDatagram;
        }
        if (body_size > kMaxRecordBodyDefault) {
            return ErrorCode::MalformedRecord;
        }
    } else {
        // No L field: the record body is the remainder of the datagram.
        body_size = datagram.size() - offset;
        if (body_size > kMaxRecordBodyDefault) {
            return ErrorCode::MalformedRecord;
        }
        uh.length = static_cast<std::uint16_t>(
            body_size > std::numeric_limits<std::uint16_t>::max()
                ? std::numeric_limits<std::uint16_t>::max()
                : body_size);
    }

    out.outer_type = static_cast<ContentType>(type_octet);
    out.header = uh;
    out.body = ByteView(datagram.data() + offset, body_size);
    out.raw_header = ByteView(datagram.data() + record_start,
                              uh.header_size);
    offset += body_size;
    return ErrorCode::Ok;
}

Result<std::vector<ParsedRecord>> parseDatagram(ByteView datagram,
                                                std::uint16_t initial_epoch) {
    std::vector<ParsedRecord> records;
    RecordScanner scanner(initial_epoch);
    std::size_t offset = 0;
    while (offset < datagram.size()) {
        ParsedRecord record;
        ErrorCode error = scanner.next(datagram, offset, record);
        if (error != ErrorCode::Ok) {
            return error;
        }
        records.push_back(record);
    }
    return records;
}

ErrorCode RecordScanner::next(ByteView datagram,
                              std::size_t& offset,
                              ParsedRecord& out) {
    ErrorCode error = parseOneRecord(datagram, offset, current_epoch_, out);
    if (error != ErrorCode::Ok) {
        return error;
    }
    current_epoch_ = out.header.epoch;
    return ErrorCode::Ok;
}

ErrorCode EpochReplayTable::getOrCreate(std::uint16_t epoch,
                                        ReplayWindow*& out) {
    auto it = windows_.find(epoch);
    if (it != windows_.end()) {
        out = &it->second;
        return ErrorCode::Ok;
    }
    if (windows_.size() >= max_epochs_) {
        return ErrorCode::EpochLimitReached;
    }
    it = windows_.emplace(epoch, ReplayWindow()).first;
    out = &it->second;
    return ErrorCode::Ok;
}

ReplayWindow* EpochReplayTable::find(std::uint16_t epoch) {
    auto it = windows_.find(epoch);
    return it == windows_.end() ? nullptr : &it->second;
}

const ReplayWindow* EpochReplayTable::find(std::uint16_t epoch) const {
    auto it = windows_.find(epoch);
    return it == windows_.end() ? nullptr : &it->second;
}

}  // namespace dtls13
