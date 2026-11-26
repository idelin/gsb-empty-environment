// SPDX-License-Identifier: MIT

#include "dtls13/reassembly.hpp"

#include <algorithm>
#include <limits>

namespace dtls13 {

namespace {

// Guard against 32-bit overflow when adding fragment bounds.
bool rangeOverflows(std::uint32_t offset, std::uint32_t length) noexcept {
    return length > std::numeric_limits<std::uint32_t>::max() - offset;
}

}  // namespace

bool HandshakeReassembler::Slot::isBitSet(std::uint32_t index) const
    noexcept {
    const std::size_t byte_index = index / 8;
    const unsigned bit_index = static_cast<unsigned>(index % 8);
    return (present[byte_index] &
            static_cast<std::uint8_t>(1u << bit_index)) != 0;
}

void HandshakeReassembler::Slot::setBit(std::uint32_t index) noexcept {
    const std::size_t byte_index = index / 8;
    const unsigned bit_index = static_cast<unsigned>(index % 8);
    present[byte_index] |= static_cast<std::uint8_t>(1u << bit_index);
}

Result<HandshakeFragment> parseHandshakeFragment(ByteView record_payload) {
    if (record_payload.size() < kHandshakeHeaderSize) {
        return ErrorCode::MalformedRecord;
    }
    const std::uint8_t* p = record_payload.data();
    HandshakeFragment fragment;
    fragment.msg_type = p[0];
    fragment.total_length = readBe24(p + 1);
    fragment.message_seq = readBe16(p + 4);
    fragment.fragment_offset = readBe24(p + 6);
    fragment.fragment_length = readBe24(p + 9);
    fragment.body = record_payload.subview(kHandshakeHeaderSize);

    if (fragment.body.size() != fragment.fragment_length) {
        return ErrorCode::MalformedRecord;
    }
    return fragment;
}

HandshakeReassembler::AddResult HandshakeReassembler::reject(
    Slot& slot, ErrorCode code) {
    slot.poisoned = true;
    AddResult result;
    result.code = code;
    return result;
}

HandshakeReassembler::AddResult HandshakeReassembler::addFragment(
    const HandshakeFragment& fragment) {
    AddResult result;

    // 1. Structural bounds: any fragment that claims past total_length is
    // an immediate injection signal.
    if (rangeOverflows(fragment.fragment_offset,
                       fragment.fragment_length) ||
        fragment.fragment_offset + fragment.fragment_length >
            fragment.total_length) {
        result.code = ErrorCode::FragmentOverflow;
        return result;
    }
    if (fragment.total_length > limits_.max_message_bytes) {
        result.code = ErrorCode::MessageTooLarge;
        return result;
    }
    if (fragment.body.size() != fragment.fragment_length) {
        result.code = ErrorCode::BadArgument;
        return result;
    }

    auto it = slots_.find(fragment.message_seq);
    if (it == slots_.end()) {
        // 2. Enforce slot / total-memory budgets before allocating.
        if (slots_.size() >= limits_.max_open_messages) {
            result.code = ErrorCode::TooManyMessages;
            return result;
        }
        const std::size_t need =
            static_cast<std::size_t>(fragment.total_length) +
            (static_cast<std::size_t>(fragment.total_length) + 7) / 8;
        if (buffered_bytes_ + need > limits_.max_total_buffered_bytes) {
            result.code = ErrorCode::MessageTooLarge;
            return result;
        }

        Slot slot;
        slot.msg_type = fragment.msg_type;
        slot.total_length = fragment.total_length;
        slot.data.assign(fragment.total_length, 0);
        const std::size_t bitmap_bytes =
            fragment.total_length == 0
                ? 1
                : (static_cast<std::size_t>(fragment.total_length) + 7) /
                      8;
        slot.present.assign(bitmap_bytes, 0);
        it = slots_.emplace(fragment.message_seq, std::move(slot)).first;
        buffered_bytes_ += need;
    }

    Slot& slot = it->second;
    if (slot.poisoned) {
        result.code = ErrorCode::ReassemblyPoisoned;
        return result;
    }

    // 3. Metadata consistency: type and total length are immutable for a
    // given message_seq. Any change is an injection attempt.
    if (slot.msg_type != fragment.msg_type ||
        slot.total_length != fragment.total_length) {
        return reject(slot, ErrorCode::InconsistentHeader);
    }

    // 4. Byte-exact overlap validation, then merge.
    for (std::uint32_t i = 0; i < fragment.fragment_length; ++i) {
        const std::uint32_t absolute = fragment.fragment_offset + i;
        const std::uint8_t incoming = fragment.body[i];
        if (slot.isBitSet(absolute)) {
            if (slot.data[absolute] != incoming) {
                return reject(slot, ErrorCode::OverlapConflict);
            }
            continue;  // Identical retransmission: dedupe, no state change.
        }
        slot.data[absolute] = incoming;
        slot.setBit(absolute);
        ++slot.received_bytes;
    }

    // 5. Completion check.
    if (slot.received_bytes == slot.total_length) {
        result.completed = true;
        result.message.msg_type = slot.msg_type;
        result.message.message_seq = fragment.message_seq;
        result.message.total_length = slot.total_length;
        result.message.body = slot.data;

        const std::size_t freed =
            slot.data.size() + slot.present.size();
        buffered_bytes_ -= std::min(buffered_bytes_, freed);
        slots_.erase(it);
        return result;
    }

    result.code = ErrorCode::Ok;
    return result;
}

HandshakeReassembler::AddResult HandshakeReassembler::addRecordPayload(
    ByteView record_payload) {
    auto parsed = parseHandshakeFragment(record_payload);
    if (!parsed.ok()) {
        AddResult result;
        result.code = parsed.code;
        return result;
    }
    return addFragment(parsed.value);
}

void HandshakeReassembler::discard(std::uint16_t message_seq) {
    auto it = slots_.find(message_seq);
    if (it == slots_.end()) {
        return;
    }
    const std::size_t freed =
        it->second.data.size() + it->second.present.size();
    buffered_bytes_ -= std::min(buffered_bytes_, freed);
    slots_.erase(it);
}

void HandshakeReassembler::reset() {
    slots_.clear();
    buffered_bytes_ = 0;
}

}  // namespace dtls13
