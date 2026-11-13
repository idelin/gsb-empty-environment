#include "netsec.hpp"

#include <cctype>
#include <cstdio>
#include <stdexcept>

namespace netsec {
namespace {

constexpr std::uint32_t kHalfCycle = kSmpCntModulus / 2;

std::uint8_t hexValue(char value) {
    if (value >= '0' && value <= '9') {
        return static_cast<std::uint8_t>(value - '0');
    }
    if (value >= 'a' && value <= 'f') {
        return static_cast<std::uint8_t>(value - 'a' + 10);
    }
    if (value >= 'A' && value <= 'F') {
        return static_cast<std::uint8_t>(value - 'A' + 10);
    }
    throw std::invalid_argument("MAC address contains a non-hexadecimal digit");
}

std::uint32_t nextCounter(std::uint32_t counter) noexcept {
    return counter + 1u == kSmpCntModulus ? 0u : counter + 1u;
}

std::uint32_t counterDistance(std::uint32_t from, std::uint32_t to) noexcept {
    return (to + kSmpCntModulus - from) % kSmpCntModulus;
}

}  // namespace

MacAddress::MacAddress(const char* text) : MacAddress(std::string_view(text)) {}

MacAddress::MacAddress(std::string_view text) { *this = parse(text); }

MacAddress MacAddress::parse(std::string_view text) {
    while (!text.empty() &&
           std::isspace(static_cast<unsigned char>(text.front())) != 0) {
        text.remove_prefix(1);
    }
    while (!text.empty() &&
           std::isspace(static_cast<unsigned char>(text.back())) != 0) {
        text.remove_suffix(1);
    }

    MacAddress address;
    if (text.size() == 12) {
        for (std::size_t index = 0; index < 6; ++index) {
            address.octets_[index] =
                static_cast<std::uint8_t>(hexValue(text[index * 2]) << 4 |
                                          hexValue(text[index * 2 + 1]));
        }
        return address;
    }

    char separator = 0;
    std::size_t group = 0;
    std::size_t offset = 0;
    while (offset < text.size()) {
        if (group >= 6) {
            throw std::invalid_argument("MAC address has too many groups");
        }

        std::size_t digits = 0;
        std::uint8_t value = 0;
        while (offset < text.size() &&
               std::isxdigit(static_cast<unsigned char>(text[offset])) != 0 &&
               digits < 2) {
            value = static_cast<std::uint8_t>(value << 4 |
                                              hexValue(text[offset]));
            ++offset;
            ++digits;
        }
        if (digits == 0) {
            throw std::invalid_argument("MAC address group is empty");
        }
        address.octets_[group++] = value;

        if (offset == text.size()) {
            break;
        }
        const char current = text[offset];
        if (current != ':' && current != '-') {
            throw std::invalid_argument("MAC address has an invalid separator");
        }
        if (separator == 0) {
            separator = current;
        } else if (separator != current) {
            throw std::invalid_argument("MAC address mixes separators");
        }
        ++offset;
        if (offset == text.size()) {
            throw std::invalid_argument("MAC address has a trailing separator");
        }
    }

    if (group != 6) {
        throw std::invalid_argument("MAC address must contain six groups");
    }
    return address;
}

std::optional<MacAddress> MacAddress::tryParse(std::string_view text) noexcept {
    try {
        return parse(text);
    } catch (...) {
        return std::nullopt;
    }
}

std::string MacAddress::toString() const {
    char buffer[18];
    std::snprintf(buffer, sizeof(buffer), "%02x:%02x:%02x:%02x:%02x:%02x",
                  static_cast<unsigned int>(octets_[0]),
                  static_cast<unsigned int>(octets_[1]),
                  static_cast<unsigned int>(octets_[2]),
                  static_cast<unsigned int>(octets_[3]),
                  static_cast<unsigned int>(octets_[4]),
                  static_cast<unsigned int>(octets_[5]));
    return std::string(buffer);
}

std::ostream& operator<<(std::ostream& stream, const MacAddress& address) {
    return stream << address.toString();
}

const char* toString(SvStatus status) noexcept {
    switch (status) {
        case SvStatus::Accepted:
            return "accepted";
        case SvStatus::Duplicate:
            return "duplicate";
        case SvStatus::Replay:
            return "replay";
        case SvStatus::RejectedSourceMac:
            return "rejected-source-mac";
        case SvStatus::RejectedConfRev:
            return "rejected-conf-rev";
        case SvStatus::RejectedSourceMacAndConfRev:
            return "rejected-source-mac-and-conf-rev";
        case SvStatus::RejectedAwaitingReconfirmation:
            return "rejected-awaiting-reconfirmation";
        case SvStatus::RejectedMalformed:
            return "rejected-malformed";
    }
    return "unknown";
}

std::ostream& operator<<(std::ostream& stream, SvStatus status) {
    return stream << toString(status);
}

SvStatus SvSubscriber::identityStatus(bool macMismatch,
                                      bool confRevMismatch) noexcept {
    if (macMismatch && confRevMismatch) {
        return SvStatus::RejectedSourceMacAndConfRev;
    }
    if (macMismatch) {
        return SvStatus::RejectedSourceMac;
    }
    return SvStatus::RejectedConfRev;
}

SvDecision SvSubscriber::reject(const SvFrame& frame, StreamState& state,
                                SvStatus status) {
    SvDecision decision;
    decision.status = status;
    decision.receivedSmpCnt = frame.smpCnt;
    decision.expectedSmpCnt =
        state.hasLastSmpCnt ? nextCounter(state.lastSmpCnt) : frame.smpCnt;
    decision.totalLostSamples = state.stats.lostSamples;
    return decision;
}

SvDecision SvSubscriber::acceptCounter(const SvFrame& frame,
                                       StreamState& state) {
    SvDecision decision;
    decision.status = SvStatus::Accepted;
    decision.receivedSmpCnt = frame.smpCnt;

    if (!state.hasLastSmpCnt) {
        state.hasLastSmpCnt = true;
        state.lastSmpCnt = frame.smpCnt;
        decision.expectedSmpCnt = frame.smpCnt;
        decision.totalLostSamples = state.stats.lostSamples;
        state.stats.hasLastSmpCnt = true;
        state.stats.lastSmpCnt = frame.smpCnt;
        ++state.stats.acceptedFrames;
        return decision;
    }

    const std::uint32_t expected = nextCounter(state.lastSmpCnt);
    decision.expectedSmpCnt = expected;
    const std::uint32_t distance =
        counterDistance(state.lastSmpCnt, frame.smpCnt);

    if (distance == 0) {
        decision.status = SvStatus::Duplicate;
        ++state.stats.duplicateFrames;
        decision.totalLostSamples = state.stats.lostSamples;
        return decision;
    }

    if (distance > kHalfCycle) {
        decision.status = SvStatus::Replay;
        ++state.stats.replayFrames;
        decision.totalLostSamples = state.stats.lostSamples;
        return decision;
    }

    if (distance > 1) {
        decision.lostSamples = distance - 1u;
        decision.gapStart = expected;
        decision.gapEnd =
            (frame.smpCnt + kSmpCntModulus - 1u) % kSmpCntModulus;
        decision.invalidSmpCnts.reserve(decision.lostSamples);
        for (std::uint32_t offset = 1; offset < distance; ++offset) {
            decision.invalidSmpCnts.push_back(
                (state.lastSmpCnt + offset) % kSmpCntModulus);
        }
        state.stats.lostSamples += decision.lostSamples;
        state.stats.invalidSamples += decision.lostSamples;
        ++state.stats.gapEvents;
    }

    state.lastSmpCnt = frame.smpCnt;
    state.stats.hasLastSmpCnt = true;
    state.stats.lastSmpCnt = frame.smpCnt;
    ++state.stats.acceptedFrames;
    decision.totalLostSamples = state.stats.lostSamples;
    return decision;
}

SvDecision SvSubscriber::receive(const SvFrame& frame) {
    if (frame.svID.empty()) {
        SvDecision decision;
        decision.status = SvStatus::RejectedMalformed;
        decision.receivedSmpCnt = frame.smpCnt;
        decision.expectedSmpCnt = frame.smpCnt;
        return decision;
    }

    StreamState& state = streams_[frame.svID];
    ++state.stats.receivedFrames;

    if (frame.smpCnt >= kSmpCntModulus) {
        ++state.stats.malformedFrames;
        return reject(frame, state, SvStatus::RejectedMalformed);
    }

    if (!state.bound) {
        state.bound = true;
        state.locked = false;
        state.sourceMac = frame.sourceMac;
        state.confRev = frame.confRev;
    }

    if (state.locked) {
        ++state.stats.awaitingReconfirmationFrames;
        return reject(frame, state,
                      SvStatus::RejectedAwaitingReconfirmation);
    }

    const bool macMismatch = frame.sourceMac != state.sourceMac;
    const bool confRevMismatch = frame.confRev != state.confRev;
    if (macMismatch || confRevMismatch) {
        state.locked = true;
        ++state.stats.identityRejectedFrames;
        return reject(frame, state, identityStatus(macMismatch, confRevMismatch));
    }

    return acceptCounter(frame, state);
}

bool SvSubscriber::confirm(const std::string& svID, const MacAddress& sourceMac,
                           std::uint32_t confRev) {
    if (svID.empty()) {
        return false;
    }
    StreamState& state = streams_[svID];
    state.bound = true;
    state.locked = false;
    state.sourceMac = sourceMac;
    state.confRev = confRev;
    state.hasLastSmpCnt = false;
    state.lastSmpCnt = 0;
    state.stats.hasLastSmpCnt = false;
    state.stats.lastSmpCnt = 0;
    return true;
}

bool SvSubscriber::resetSequence(const std::string& svID) {
    auto iterator = streams_.find(svID);
    if (iterator == streams_.end() || !iterator->second.bound) {
        return false;
    }
    iterator->second.hasLastSmpCnt = false;
    iterator->second.lastSmpCnt = 0;
    iterator->second.stats.hasLastSmpCnt = false;
    iterator->second.stats.lastSmpCnt = 0;
    return true;
}

bool SvSubscriber::unbind(const std::string& svID) {
    return streams_.erase(svID) != 0u;
}

void SvSubscriber::clear() { streams_.clear(); }

bool SvSubscriber::isBound(const std::string& svID) const {
    auto iterator = streams_.find(svID);
    return iterator != streams_.end() && iterator->second.bound;
}

bool SvSubscriber::isLocked(const std::string& svID) const {
    auto iterator = streams_.find(svID);
    return iterator != streams_.end() && iterator->second.locked;
}

std::optional<SvBinding> SvSubscriber::binding(
    const std::string& svID) const {
    auto iterator = streams_.find(svID);
    if (iterator == streams_.end() || !iterator->second.bound) {
        return std::nullopt;
    }
    SvBinding result;
    result.sourceMac = iterator->second.sourceMac;
    result.confRev = iterator->second.confRev;
    result.awaitingReconfirmation = iterator->second.locked;
    return result;
}

SvStreamStats SvSubscriber::stats(const std::string& svID) const {
    auto iterator = streams_.find(svID);
    if (iterator == streams_.end()) {
        return SvStreamStats{};
    }
    return iterator->second.stats;
}

}  // namespace netsec
