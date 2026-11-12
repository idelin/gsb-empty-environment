#include "netsec.hpp"

namespace netsec {
namespace {

constexpr std::uint32_t kMaxStandardId = 0x7FFu;
constexpr std::uint32_t kMaxExtendedId = 0x1FFFFFFFu;

bool isValidIdentity(std::uint32_t id, CanFdIde ide) noexcept {
    switch (ide) {
    case CanFdIde::Standard:
        return id <= kMaxStandardId;
    case CanFdIde::Extended:
        return id <= kMaxExtendedId;
    }
    return false;
}

}  // namespace

std::size_t canFdPayloadLength(std::uint8_t dlc) noexcept {
    switch (dlc) {
    case 0:
    case 1:
    case 2:
    case 3:
    case 4:
    case 5:
    case 6:
    case 7:
    case 8:
        return dlc;
    case 9:
        return 12;
    case 10:
        return 16;
    case 11:
        return 20;
    case 12:
        return 24;
    case 13:
        return 32;
    case 14:
        return 48;
    case 15:
        return 64;
    default:
        return 0;
    }
}

bool isValidCanFdDlc(std::uint8_t dlc) noexcept {
    return dlc <= 15;
}

CanFdFilter::CanFdFilter(CanFdMacVerifier verifier, void* context) noexcept
    : verifier_(verifier), verifierContext_(context) {}

bool CanFdFilter::addEntry(const CanFdWhitelistEntry& entry) noexcept {
    if (size_ >= kCanFdMaxWhitelistEntries ||
        !isValidIdentity(entry.id, entry.ide) ||
        !isValidCanFdDlc(entry.dlc)) {
        return false;
    }

    for (std::size_t i = 0; i < size_; ++i) {
        if (entries_[i].entry.id == entry.id && entries_[i].entry.ide == entry.ide) {
            return false;
        }
    }

    entries_[size_].entry = entry;
    ++size_;
    return true;
}

void CanFdFilter::setMacVerifier(CanFdMacVerifier verifier, void* context) noexcept {
    verifier_ = verifier;
    verifierContext_ = context;
}

CanFdFilterStatus CanFdFilter::evaluate(const CanFdFrame& frame) noexcept {
    return evaluateImpl(frame, false);
}

CanFdFilterStatus CanFdFilter::evaluate(const CanFdFrame& frame,
                                        bool macVerified) noexcept {
    return evaluateImpl(frame, macVerified);
}

bool CanFdFilter::accept(const CanFdFrame& frame) noexcept {
    return isAccepted(evaluateImpl(frame, false));
}

bool CanFdFilter::accept(const CanFdFrame& frame, bool macVerified) noexcept {
    return isAccepted(evaluateImpl(frame, macVerified));
}

CanFdFilterStatus CanFdFilter::evaluateImpl(const CanFdFrame& frame,
                                            bool macPreverified) noexcept {
    if (!isValidIdentity(frame.id, frame.ide)) {
        return CanFdFilterStatus::InvalidIdentifier;
    }

    bool idFound = false;
    EntryState* match = nullptr;
    for (std::size_t i = 0; i < size_; ++i) {
        if (entries_[i].entry.id == frame.id) {
            idFound = true;
            if (entries_[i].entry.ide == frame.ide) {
                match = &entries_[i];
                break;
            }
        }
    }
    if (match == nullptr) {
        return idFound ? CanFdFilterStatus::IdeMismatch
                       : CanFdFilterStatus::NotWhitelisted;
    }

    const CanFdWhitelistEntry& entry = match->entry;
    if (!isValidCanFdDlc(frame.dlc)) {
        return CanFdFilterStatus::InvalidDlc;
    }
    if (frame.dlc != entry.dlc) {
        return CanFdFilterStatus::DlcMismatch;
    }

    const std::size_t expectedLength = canFdPayloadLength(frame.dlc);
    if (frame.payloadLength != expectedLength) {
        return CanFdFilterStatus::PayloadLengthMismatch;
    }
    if (expectedLength != 0 && frame.payload == nullptr) {
        return CanFdFilterStatus::PayloadUnavailable;
    }

    if (match->hasTimestamp) {
        if (frame.timestampUs < match->lastAcceptedTimestampUs) {
            return CanFdFilterStatus::TimestampRegressed;
        }
        if (frame.timestampUs - match->lastAcceptedTimestampUs <
            entry.minPeriodUs) {
            return CanFdFilterStatus::TooSoon;
        }
    }

    if (entry.requiresFreshCounter && match->hasCounter &&
        frame.counter <= match->lastAcceptedCounter) {
        return CanFdFilterStatus::ReplayedCounter;
    }

    if (entry.requiresMac && !macPreverified) {
        if (verifier_ == nullptr) {
            return CanFdFilterStatus::MacVerifierMissing;
        }
        if (!verifier_(frame, entry, verifierContext_)) {
            return CanFdFilterStatus::MacRejected;
        }
    }

    match->hasTimestamp = true;
    match->lastAcceptedTimestampUs = frame.timestampUs;
    if (entry.requiresFreshCounter) {
        match->hasCounter = true;
        match->lastAcceptedCounter = frame.counter;
    }

    return CanFdFilterStatus::Accepted;
}

void CanFdFilter::resetTracking() noexcept {
    for (std::size_t i = 0; i < size_; ++i) {
        entries_[i].hasTimestamp = false;
        entries_[i].lastAcceptedTimestampUs = 0;
        entries_[i].hasCounter = false;
        entries_[i].lastAcceptedCounter = 0;
    }
}

std::size_t CanFdFilter::size() const noexcept {
    return size_;
}

std::size_t CanFdFilter::capacity() const noexcept {
    return kCanFdMaxWhitelistEntries;
}

}  // namespace netsec
