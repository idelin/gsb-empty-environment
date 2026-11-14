#include "netsec.hpp"

#include <algorithm>
#include <bit>
#include <cmath>
#include <limits>
#include <mutex>
#include <stdexcept>
#include <utility>

namespace netsec {

struct CandidateQualityView {
    const ClockQuality* quality{};
    ClockIdentity grandmaster{};
    std::uint16_t steps_removed{};
    PortIdentity source{};
};

namespace {

void appendU8(std::vector<std::uint8_t>& out, std::uint8_t value) {
    out.push_back(value);
}

void appendU16(std::vector<std::uint8_t>& out, std::uint16_t value) {
    out.push_back(static_cast<std::uint8_t>(value >> 8));
    out.push_back(static_cast<std::uint8_t>(value));
}

void appendU32(std::vector<std::uint8_t>& out, std::uint32_t value) {
    for (int shift = 24; shift >= 0; shift -= 8) {
        out.push_back(static_cast<std::uint8_t>(value >> shift));
    }
}

void appendU64(std::vector<std::uint8_t>& out, std::uint64_t value) {
    for (int shift = 56; shift >= 0; shift -= 8) {
        out.push_back(static_cast<std::uint8_t>(value >> shift));
    }
}

void appendBytes(
    std::vector<std::uint8_t>& out,
    std::span<const std::uint8_t> bytes) {
    out.insert(out.end(), bytes.begin(), bytes.end());
}

std::int64_t saturatingAdd(std::int64_t lhs, std::int64_t rhs) {
    if (rhs > 0 && lhs > std::numeric_limits<std::int64_t>::max() - rhs) {
        return std::numeric_limits<std::int64_t>::max();
    }
    if (rhs < 0 && lhs < std::numeric_limits<std::int64_t>::min() - rhs) {
        return std::numeric_limits<std::int64_t>::min();
    }
    return lhs + rhs;
}

std::int64_t saturatingSub(std::int64_t lhs, std::int64_t rhs) {
    if (rhs == std::numeric_limits<std::int64_t>::min()) {
        if (lhs >= 0) {
            return std::numeric_limits<std::int64_t>::max();
        }
        return static_cast<std::int64_t>(
            static_cast<std::uint64_t>(lhs) +
            (static_cast<std::uint64_t>(1) << 63U));
    }
    return saturatingAdd(lhs, -rhs);
}

Timestamp saturatingAdd(Timestamp lhs, Timestamp rhs) {
    return Timestamp{saturatingAdd(lhs.count(), rhs.count())};
}

Timestamp saturatingSub(Timestamp lhs, Timestamp rhs) {
    return Timestamp{saturatingSub(lhs.count(), rhs.count())};
}

std::uint64_t magnitude(std::int64_t value) {
    if (value >= 0) {
        return static_cast<std::uint64_t>(value);
    }
    if (value == std::numeric_limits<std::int64_t>::min()) {
        return static_cast<std::uint64_t>(std::numeric_limits<std::int64_t>::max()) + 1U;
    }
    return static_cast<std::uint64_t>(-value);
}

std::string hexByte(std::uint8_t value) {
    static constexpr char digits[] = "0123456789abcdef";
    std::string out;
    out.push_back(digits[(value >> 4) & 0x0FU]);
    out.push_back(digits[value & 0x0FU]);
    return out;
}

int hexValue(char value) {
    if (value >= '0' && value <= '9') {
        return value - '0';
    }
    if (value >= 'a' && value <= 'f') {
        return value - 'a' + 10;
    }
    if (value >= 'A' && value <= 'F') {
        return value - 'A' + 10;
    }
    return -1;
}

bool constantTimeEqual(const AuthTag& lhs, const AuthTag& rhs) {
    std::uint8_t difference = 0;
    for (std::size_t index = 0; index < lhs.size(); ++index) {
        difference = static_cast<std::uint8_t>(difference | (lhs[index] ^ rhs[index]));
    }
    return difference == 0;
}

std::array<std::uint8_t, 32> sha256(std::span<const std::uint8_t> input) {
    static constexpr std::array<std::uint32_t, 64> k{
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

    std::vector<std::uint8_t> message(input.begin(), input.end());
    const std::uint64_t bit_length =
        static_cast<std::uint64_t>(message.size()) * 8U;
    message.push_back(0x80U);
    while ((message.size() % 64U) != 56U) {
        message.push_back(0U);
    }
    for (int shift = 56; shift >= 0; shift -= 8) {
        message.push_back(static_cast<std::uint8_t>(bit_length >> shift));
    }

    std::array<std::uint32_t, 8> hash{
        0x6a09e667U, 0xbb67ae85U, 0x3c6ef372U, 0xa54ff53aU,
        0x510e527fU, 0x9b05688cU, 0x1f83d9abU, 0x5be0cd19U,
    };

    for (std::size_t offset = 0; offset < message.size(); offset += 64U) {
        std::array<std::uint32_t, 64> schedule{};
        for (std::size_t index = 0; index < 16U; ++index) {
            const std::size_t base = offset + index * 4U;
            schedule[index] =
                (static_cast<std::uint32_t>(message[base]) << 24U) |
                (static_cast<std::uint32_t>(message[base + 1U]) << 16U) |
                (static_cast<std::uint32_t>(message[base + 2U]) << 8U) |
                static_cast<std::uint32_t>(message[base + 3U]);
        }
        for (std::size_t index = 16U; index < 64U; ++index) {
            const std::uint32_t sigma0 =
                std::rotr(schedule[index - 15U], 7) ^
                std::rotr(schedule[index - 15U], 18) ^
                (schedule[index - 15U] >> 3U);
            const std::uint32_t sigma1 =
                std::rotr(schedule[index - 2U], 17) ^
                std::rotr(schedule[index - 2U], 19) ^
                (schedule[index - 2U] >> 10U);
            schedule[index] =
                schedule[index - 16U] + sigma0 +
                schedule[index - 7U] + sigma1;
        }

        std::uint32_t a = hash[0];
        std::uint32_t b = hash[1];
        std::uint32_t c = hash[2];
        std::uint32_t d = hash[3];
        std::uint32_t e = hash[4];
        std::uint32_t f = hash[5];
        std::uint32_t g = hash[6];
        std::uint32_t h = hash[7];

        for (std::size_t index = 0; index < 64U; ++index) {
            const std::uint32_t sum1 =
                std::rotr(e, 6) ^ std::rotr(e, 11) ^ std::rotr(e, 25);
            const std::uint32_t choose = (e & f) ^ (~e & g);
            const std::uint32_t temp1 =
                h + sum1 + choose + k[index] + schedule[index];
            const std::uint32_t sum0 =
                std::rotr(a, 2) ^ std::rotr(a, 13) ^ std::rotr(a, 22);
            const std::uint32_t majority = (a & b) ^ (a & c) ^ (b & c);
            const std::uint32_t temp2 = sum0 + majority;

            h = g;
            g = f;
            f = e;
            e = d + temp1;
            d = c;
            c = b;
            b = a;
            a = temp1 + temp2;
        }

        hash[0] += a;
        hash[1] += b;
        hash[2] += c;
        hash[3] += d;
        hash[4] += e;
        hash[5] += f;
        hash[6] += g;
        hash[7] += h;
    }

    std::array<std::uint8_t, 32> digest{};
    for (std::size_t index = 0; index < hash.size(); ++index) {
        digest[index * 4U] = static_cast<std::uint8_t>(hash[index] >> 24U);
        digest[index * 4U + 1U] = static_cast<std::uint8_t>(hash[index] >> 16U);
        digest[index * 4U + 2U] = static_cast<std::uint8_t>(hash[index] >> 8U);
        digest[index * 4U + 3U] = static_cast<std::uint8_t>(hash[index]);
    }
    return digest;
}

bool sequenceIsNewer(std::uint16_t candidate, std::uint16_t current) {
    return static_cast<std::int16_t>(
               static_cast<std::uint16_t>(candidate - current)) > 0;
}

std::string describeSource(const PortIdentity& source) {
    return source.clock_identity.toString() + "/" + std::to_string(source.port_number);
}

AlarmCode alarmCodeFor(RejectionReason reason) {
    switch (reason) {
        case RejectionReason::UntrustedSource:
        case RejectionReason::TrustedSourceDisabled:
            return AlarmCode::UntrustedSource;
        case RejectionReason::AuthenticationRequired:
            return AlarmCode::AuthenticationMissing;
        case RejectionReason::AuthenticationFailed:
        case RejectionReason::KeyIdMismatch:
            return AlarmCode::AuthenticationFailed;
        case RejectionReason::ReplayDetected:
            return AlarmCode::ReplayDetected;
        case RejectionReason::StaleAnnouncement:
        case RejectionReason::FutureAnnouncement:
            return AlarmCode::StaleAnnouncement;
        case RejectionReason::GrandmasterMismatch:
            return AlarmCode::GrandmasterMismatch;
        case RejectionReason::PriorityOutOfPolicy:
        case RejectionReason::StratumOutOfPolicy:
        case RejectionReason::ClockClassOutOfPolicy:
        case RejectionReason::ClockAccuracyOutOfPolicy:
        case RejectionReason::VarianceOutOfPolicy:
            return AlarmCode::PolicyViolation;
        case RejectionReason::NoSelectedMaster:
        case RejectionReason::SyncSourceMismatch:
            return AlarmCode::SyncSourceMismatch;
        case RejectionReason::MalformedAnnouncement:
        case RejectionReason::VersionUnsupported:
        case RejectionReason::DomainMismatch:
            return AlarmCode::InvalidAnnouncement;
        case RejectionReason::None:
            return AlarmCode::None;
    }
    return AlarmCode::None;
}

Severity severityFor(RejectionReason reason) {
    switch (reason) {
        case RejectionReason::UntrustedSource:
        case RejectionReason::TrustedSourceDisabled:
        case RejectionReason::AuthenticationRequired:
        case RejectionReason::AuthenticationFailed:
        case RejectionReason::KeyIdMismatch:
        case RejectionReason::GrandmasterMismatch:
        case RejectionReason::SyncSourceMismatch:
            return Severity::Critical;
        default:
            return Severity::Warning;
    }
}

}  // namespace

namespace {

bool candidateIsBetter(
    const CandidateQualityView& lhs,
    const CandidateQualityView& rhs,
    BmcaMode mode) {
    const ClockQuality& a = *lhs.quality;
    const ClockQuality& b = *rhs.quality;
    if (a.priority1 != b.priority1) {
        return a.priority1 < b.priority1;
    }
    if (mode == BmcaMode::PriorityStratumIdentity) {
        if (a.stratum != b.stratum) {
            return a.stratum < b.stratum;
        }
        if (a.priority2 != b.priority2) {
            return a.priority2 < b.priority2;
        }
        if (a.clock_class != b.clock_class) {
            return a.clock_class < b.clock_class;
        }
    } else {
        if (a.clock_class != b.clock_class) {
            return a.clock_class < b.clock_class;
        }
        if (a.clock_accuracy != b.clock_accuracy) {
            return a.clock_accuracy < b.clock_accuracy;
        }
        if (a.offset_scaled_log_variance != b.offset_scaled_log_variance) {
            return a.offset_scaled_log_variance < b.offset_scaled_log_variance;
        }
        if (a.priority2 != b.priority2) {
            return a.priority2 < b.priority2;
        }
        // Stratum is a local policy extension rather than a native IEEE 1588
        // field. Use it only as a tie-break after the canonical quality fields.
        if (a.stratum != b.stratum) {
            return a.stratum < b.stratum;
        }
    }

    if (lhs.grandmaster != rhs.grandmaster) {
        return lhs.grandmaster < rhs.grandmaster;
    }
    if (lhs.steps_removed != rhs.steps_removed) {
        return lhs.steps_removed < rhs.steps_removed;
    }
    return lhs.source < rhs.source;
}

double finiteOr(double value, double fallback) {
    return std::isfinite(value) ? value : fallback;
}

std::int64_t clampToInt64(double value) {
    constexpr double lower =
        static_cast<double>(std::numeric_limits<std::int64_t>::min());
    constexpr double upper =
        static_cast<double>(std::numeric_limits<std::int64_t>::max());
    if (!std::isfinite(value)) {
        return value < 0 ? std::numeric_limits<std::int64_t>::min()
                         : std::numeric_limits<std::int64_t>::max();
    }
    if (value <= lower) {
        return std::numeric_limits<std::int64_t>::min();
    }
    if (value >= upper) {
        return std::numeric_limits<std::int64_t>::max();
    }
    return static_cast<std::int64_t>(std::llround(value));
}

}  // namespace

// ---------------------------------------------------------------------------
// Identities
// ---------------------------------------------------------------------------

bool ClockIdentity::isZero() const noexcept {
    return std::all_of(bytes.begin(), bytes.end(), [](std::uint8_t value) {
        return value == 0;
    });
}

std::string ClockIdentity::toString() const {
    std::string out;
    out.reserve(23);
    for (std::size_t index = 0; index < bytes.size(); ++index) {
        if (index != 0) {
            out.push_back(':');
        }
        out += hexByte(bytes[index]);
    }
    return out;
}

std::optional<ClockIdentity> ClockIdentity::fromString(std::string_view text) {
    if (text.size() > 2 && text[0] == '0' && (text[1] == 'x' || text[1] == 'X')) {
        text.remove_prefix(2);
    }

    std::vector<std::uint8_t> digits;
    digits.reserve(16);
    for (char character : text) {
        if (character == ':' || character == '-' || character == '.' ||
            character == ' ') {
            continue;
        }
        const int value = hexValue(character);
        if (value < 0) {
            return std::nullopt;
        }
        digits.push_back(static_cast<std::uint8_t>(value));
    }
    if (digits.size() != 16) {
        return std::nullopt;
    }

    ClockIdentity identity;
    for (std::size_t index = 0; index < identity.bytes.size(); ++index) {
        identity.bytes[index] = static_cast<std::uint8_t>(
            (digits[index * 2U] << 4U) | digits[index * 2U + 1U]);
    }
    return identity;
}

bool PortIdentity::isZero() const noexcept {
    return clock_identity.isZero() && port_number == 0;
}

bool Authentication::isEmpty() const noexcept {
    return std::all_of(tag.begin(), tag.end(), [](std::uint8_t value) {
        return value == 0;
    });
}

bool Announcement::hasAuthentication() const noexcept {
    return authentication.has_value() && !authentication->isEmpty();
}

ClockIdentity Announcement::effectiveGrandmaster() const noexcept {
    return grandmaster.isZero() ? source.clock_identity : grandmaster;
}

bool PriorityRange::contains(std::uint8_t value) const noexcept {
    return value >= minimum && value <= maximum;
}

bool AdmissionResult::hasAlarm(AlarmCode code) const noexcept {
    return std::any_of(alarms.begin(), alarms.end(), [code](const Alarm& alarm) {
        return alarm.code == code;
    });
}

bool TimeUpdateResult::hasAlarm(AlarmCode code) const noexcept {
    return std::any_of(alarms.begin(), alarms.end(), [code](const Alarm& alarm) {
        return alarm.code == code;
    });
}

std::string_view toString(RejectionReason reason) noexcept {
    switch (reason) {
        case RejectionReason::None:
            return "none";
        case RejectionReason::MalformedAnnouncement:
            return "malformed-announcement";
        case RejectionReason::VersionUnsupported:
            return "version-unsupported";
        case RejectionReason::DomainMismatch:
            return "domain-mismatch";
        case RejectionReason::UntrustedSource:
            return "untrusted-source";
        case RejectionReason::TrustedSourceDisabled:
            return "trusted-source-disabled";
        case RejectionReason::AuthenticationRequired:
            return "authentication-required";
        case RejectionReason::AuthenticationFailed:
            return "authentication-failed";
        case RejectionReason::KeyIdMismatch:
            return "key-id-mismatch";
        case RejectionReason::ReplayDetected:
            return "replay-detected";
        case RejectionReason::StaleAnnouncement:
            return "stale-announcement";
        case RejectionReason::FutureAnnouncement:
            return "future-announcement";
        case RejectionReason::GrandmasterMismatch:
            return "grandmaster-mismatch";
        case RejectionReason::PriorityOutOfPolicy:
            return "priority-out-of-policy";
        case RejectionReason::StratumOutOfPolicy:
            return "stratum-out-of-policy";
        case RejectionReason::ClockClassOutOfPolicy:
            return "clock-class-out-of-policy";
        case RejectionReason::ClockAccuracyOutOfPolicy:
            return "clock-accuracy-out-of-policy";
        case RejectionReason::VarianceOutOfPolicy:
            return "variance-out-of-policy";
        case RejectionReason::NoSelectedMaster:
            return "no-selected-master";
        case RejectionReason::SyncSourceMismatch:
            return "sync-source-mismatch";
    }
    return "unknown";
}

std::string_view toString(AlarmCode code) noexcept {
    switch (code) {
        case AlarmCode::None:
            return "none";
        case AlarmCode::InvalidAnnouncement:
            return "invalid-announcement";
        case AlarmCode::UntrustedSource:
            return "untrusted-source";
        case AlarmCode::AuthenticationMissing:
            return "authentication-missing";
        case AlarmCode::AuthenticationFailed:
            return "authentication-failed";
        case AlarmCode::ReplayDetected:
            return "replay-detected";
        case AlarmCode::StaleAnnouncement:
            return "stale-announcement";
        case AlarmCode::PolicyViolation:
            return "policy-violation";
        case AlarmCode::GrandmasterMismatch:
            return "grandmaster-mismatch";
        case AlarmCode::MasterChanged:
            return "master-changed";
        case AlarmCode::NoTrustedSource:
            return "no-trusted-source";
        case AlarmCode::TimeJumpExceedsLimit:
            return "time-jump-exceeds-limit";
        case AlarmCode::BackwardCorrectionSuppressed:
            return "backward-correction-suppressed";
        case AlarmCode::LeapSecond:
            return "leap-second";
        case AlarmCode::LeapSecondOutOfBounds:
            return "leap-second-out-of-bounds";
        case AlarmCode::DriftExceedsLimit:
            return "drift-exceeds-limit";
        case AlarmCode::UncertaintyExceedsLimit:
            return "uncertainty-exceeds-limit";
        case AlarmCode::InvalidTimeSample:
            return "invalid-time-sample";
        case AlarmCode::SyncSourceMismatch:
            return "sync-source-mismatch";
    }
    return "unknown";
}

std::string_view toString(Severity severity) noexcept {
    switch (severity) {
        case Severity::Info:
            return "info";
        case Severity::Warning:
            return "warning";
        case Severity::Critical:
            return "critical";
    }
    return "unknown";
}

// ---------------------------------------------------------------------------
// Authentication
// ---------------------------------------------------------------------------

AuthTag hmacSha256(
    std::span<const std::uint8_t> key,
    std::span<const std::uint8_t> message) {
    std::array<std::uint8_t, 64> block{};
    if (key.size() > block.size()) {
        const auto digest = sha256(key);
        std::copy(digest.begin(), digest.end(), block.begin());
    } else {
        std::copy(key.begin(), key.end(), block.begin());
    }

    std::array<std::uint8_t, 64> innerPad{};
    std::array<std::uint8_t, 64> outerPad{};
    for (std::size_t index = 0; index < block.size(); ++index) {
        innerPad[index] = static_cast<std::uint8_t>(block[index] ^ 0x36U);
        outerPad[index] = static_cast<std::uint8_t>(block[index] ^ 0x5cU);
    }

    std::vector<std::uint8_t> inner(innerPad.begin(), innerPad.end());
    appendBytes(inner, message);
    const auto innerDigest = sha256(inner);

    std::vector<std::uint8_t> outer(outerPad.begin(), outerPad.end());
    appendBytes(outer, innerDigest);
    return sha256(outer);
}

std::vector<std::uint8_t> canonicalAnnouncementPayload(
    const Announcement& announcement,
    std::uint32_t key_id) {
    std::vector<std::uint8_t> payload;
    payload.reserve(96);
    static constexpr std::string_view domainTag = "netsec.ptp.announce.v1";
    appendBytes(
        payload,
        std::span<const std::uint8_t>(
            reinterpret_cast<const std::uint8_t*>(domainTag.data()),
            domainTag.size()));
    appendU8(payload, 0U);
    appendU32(payload, key_id);
    appendU8(payload, announcement.version);
    appendU8(payload, announcement.domain);
    appendBytes(payload, announcement.source.clock_identity.bytes);
    appendU16(payload, announcement.source.port_number);
    appendBytes(payload, announcement.effectiveGrandmaster().bytes);
    appendU8(payload, announcement.quality.priority1);
    appendU8(payload, announcement.quality.clock_class);
    appendU8(payload, announcement.quality.clock_accuracy);
    appendU16(payload, announcement.quality.offset_scaled_log_variance);
    appendU8(payload, announcement.quality.priority2);
    appendU8(payload, announcement.quality.stratum);
    appendU16(payload, announcement.sequence_id);
    appendU16(payload, announcement.steps_removed);

    if (announcement.transmit_time) {
        appendU8(payload, 1U);
        appendU64(
            payload,
            static_cast<std::uint64_t>(announcement.transmit_time->count()));
    } else {
        appendU8(payload, 0U);
    }
    if (announcement.utc_offset_seconds) {
        appendU8(payload, 1U);
        appendU32(
            payload,
            static_cast<std::uint32_t>(*announcement.utc_offset_seconds));
    } else {
        appendU8(payload, 0U);
    }
    if (announcement.leap_seconds) {
        appendU8(payload, 1U);
        appendU8(payload, static_cast<std::uint8_t>(*announcement.leap_seconds));
    } else {
        appendU8(payload, 0U);
    }
    return payload;
}

Announcement signAnnouncement(
    Announcement announcement,
    std::span<const std::uint8_t> key,
    std::uint32_t key_id) {
    if (key.empty()) {
        throw std::invalid_argument("announcement authentication key must not be empty");
    }
    Authentication authentication;
    authentication.key_id = key_id;
    const auto payload = canonicalAnnouncementPayload(announcement, key_id);
    authentication.tag = hmacSha256(key, payload);
    announcement.authentication = authentication;
    return announcement;
}

Announcement signAnnouncement(
    Announcement announcement,
    std::string_view key,
    std::uint32_t key_id) {
    const auto bytes = keyFromText(key);
    return signAnnouncement(std::move(announcement), bytes, key_id);
}

bool verifyAuthentication(
    const Announcement& announcement,
    std::span<const std::uint8_t> key,
    std::optional<std::uint32_t> expected_key_id) {
    if (!announcement.authentication || key.empty() ||
        announcement.authentication->isEmpty()) {
        return false;
    }
    if (expected_key_id &&
        *expected_key_id != announcement.authentication->key_id) {
        return false;
    }
    const auto payload = canonicalAnnouncementPayload(
        announcement, announcement.authentication->key_id);
    const auto expected = hmacSha256(key, payload);
    return constantTimeEqual(expected, announcement.authentication->tag);
}

bool verifyAuthentication(
    const Announcement& announcement,
    std::string_view key,
    std::optional<std::uint32_t> expected_key_id) {
    const auto bytes = keyFromText(key);
    return verifyAuthentication(announcement, bytes, expected_key_id);
}

std::vector<std::uint8_t> keyFromHex(std::string_view hex) {
    if (hex.size() > 2 && hex[0] == '0' && (hex[1] == 'x' || hex[1] == 'X')) {
        hex.remove_prefix(2);
    }
    std::vector<std::uint8_t> output;
    int high = -1;
    for (char character : hex) {
        if (character == ':' || character == '-' || character == '.' ||
            character == ' ') {
            continue;
        }
        const int value = hexValue(character);
        if (value < 0) {
            throw std::invalid_argument("invalid hexadecimal key");
        }
        if (high < 0) {
            high = value;
        } else {
            output.push_back(static_cast<std::uint8_t>((high << 4) | value));
            high = -1;
        }
    }
    if (high >= 0) {
        throw std::invalid_argument("hexadecimal key has an odd number of digits");
    }
    return output;
}

std::vector<std::uint8_t> keyFromText(std::string_view text) {
    return std::vector<std::uint8_t>(text.begin(), text.end());
}

TrustedMaster makeTrustedMaster(
    ClockIdentity identity,
    std::string_view shared_secret,
    std::optional<std::uint16_t> port_number,
    std::optional<std::uint32_t> key_id) {
    TrustedMaster master;
    master.clock_identity = identity;
    master.shared_secret.assign(shared_secret.begin(), shared_secret.end());
    master.port_number = port_number;
    master.key_id = key_id;
    return master;
}

Announcement makeAnnouncement(
    ClockIdentity source,
    std::uint8_t priority1,
    std::uint8_t stratum,
    std::uint16_t sequence_id,
    std::uint8_t clock_class) {
    Announcement announcement;
    announcement.source.clock_identity = source;
    announcement.source.port_number = 1;
    announcement.grandmaster = source;
    announcement.quality.priority1 = priority1;
    announcement.quality.stratum = stratum;
    announcement.quality.clock_class = clock_class;
    announcement.sequence_id = sequence_id;
    return announcement;
}

Announcement makeAnnouncement(
    PortIdentity source,
    ClockQuality quality,
    std::uint16_t sequence_id,
    ClockIdentity grandmaster,
    std::uint16_t steps_removed) {
    Announcement announcement;
    announcement.source = source;
    announcement.quality = quality;
    announcement.sequence_id = sequence_id;
    announcement.grandmaster = grandmaster.isZero()
                                   ? source.clock_identity
                                   : grandmaster;
    announcement.steps_removed = steps_removed;
    return announcement;
}

// ---------------------------------------------------------------------------
// Admission and BMCA
// ---------------------------------------------------------------------------

struct MasterClockAdmission::Impl {
    struct ReplayState {
        PortIdentity source{};
        bool initialized{false};
        std::uint16_t last_sequence{0};
        Timestamp last_received{};
    };

    struct Candidate {
        Announcement announcement{};
        Timestamp received_at{};
        Timestamp expires_at{};
    };

    explicit Impl(
        AdmissionPolicy initial_policy,
        std::vector<TrustedMaster> initial_trusted)
        : policy(std::move(initial_policy)),
          trusted_masters(std::move(initial_trusted)) {
        std::vector<TrustedMaster> unique;
        unique.reserve(trusted_masters.size());
        for (auto& master : trusted_masters) {
            if (master.clock_identity.isZero()) {
                continue;
            }
            const bool duplicate = std::any_of(
                unique.begin(), unique.end(), [&master](const TrustedMaster& other) {
                    return other.clock_identity == master.clock_identity;
                });
            if (!duplicate) {
                unique.push_back(std::move(master));
            }
        }
        trusted_masters = std::move(unique);
    }

    AdmissionPolicy policy;
    std::vector<TrustedMaster> trusted_masters;
    std::vector<Candidate> candidates;
    std::vector<ReplayState> replay_states;
    std::optional<SelectedMaster> current;
    std::optional<PortIdentity> selected_source;
    std::function<void(const Alarm&)> alarm_sink;
    mutable std::mutex mutex;
    Timestamp logical_now{0};
    bool has_received{false};

    const TrustedMaster* findTrusted(const ClockIdentity& identity) const {
        const auto iterator = std::find_if(
            trusted_masters.begin(), trusted_masters.end(),
            [&identity](const TrustedMaster& master) {
                return master.clock_identity == identity;
            });
        return iterator == trusted_masters.end() ? nullptr : &*iterator;
    }

    ReplayState* findReplay(const PortIdentity& source) {
        const auto iterator = std::find_if(
            replay_states.begin(), replay_states.end(),
            [&source](const ReplayState& state) {
                return state.source == source;
            });
        return iterator == replay_states.end() ? nullptr : &*iterator;
    }

    bool qualityAllowed(
        const TrustedMaster& trusted,
        const ClockQuality& quality,
        RejectionReason& reason) const {
        if (!policy.priority1_range.contains(quality.priority1)) {
            reason = RejectionReason::PriorityOutOfPolicy;
            return false;
        }
        if (!policy.priority2_range.contains(quality.priority2)) {
            reason = RejectionReason::PriorityOutOfPolicy;
            return false;
        }
        if (quality.stratum < policy.min_stratum ||
            quality.stratum > policy.max_stratum) {
            reason = RejectionReason::StratumOutOfPolicy;
            return false;
        }
        if (quality.clock_class > policy.max_clock_class) {
            reason = RejectionReason::ClockClassOutOfPolicy;
            return false;
        }
        if (quality.clock_accuracy > policy.max_clock_accuracy) {
            reason = RejectionReason::ClockAccuracyOutOfPolicy;
            return false;
        }
        if (quality.offset_scaled_log_variance >
            policy.max_offset_scaled_log_variance) {
            reason = RejectionReason::VarianceOutOfPolicy;
            return false;
        }

        if (trusted.allowed_priority1 &&
            !trusted.allowed_priority1->contains(quality.priority1)) {
            reason = RejectionReason::PriorityOutOfPolicy;
            return false;
        }
        if (trusted.allowed_priority2 &&
            !trusted.allowed_priority2->contains(quality.priority2)) {
            reason = RejectionReason::PriorityOutOfPolicy;
            return false;
        }
        if (trusted.min_stratum && quality.stratum < *trusted.min_stratum) {
            reason = RejectionReason::StratumOutOfPolicy;
            return false;
        }
        if (trusted.max_stratum && quality.stratum > *trusted.max_stratum) {
            reason = RejectionReason::StratumOutOfPolicy;
            return false;
        }
        if (trusted.max_clock_class &&
            quality.clock_class > *trusted.max_clock_class) {
            reason = RejectionReason::ClockClassOutOfPolicy;
            return false;
        }

        if (trusted.expected_priority1 &&
            quality.priority1 != *trusted.expected_priority1) {
            reason = RejectionReason::PriorityOutOfPolicy;
            return false;
        }
        if (trusted.expected_priority2 &&
            quality.priority2 != *trusted.expected_priority2) {
            reason = RejectionReason::PriorityOutOfPolicy;
            return false;
        }
        if (trusted.expected_stratum &&
            quality.stratum != *trusted.expected_stratum) {
            reason = RejectionReason::StratumOutOfPolicy;
            return false;
        }
        if (trusted.expected_clock_class &&
            quality.clock_class != *trusted.expected_clock_class) {
            reason = RejectionReason::ClockClassOutOfPolicy;
            return false;
        }
        return true;
    }

    static void appendAlarm(
        std::vector<Alarm>& alarms,
        AlarmCode code,
        Severity severity,
        std::string detail,
        Timestamp at) {
        alarms.push_back(Alarm{code, severity, std::move(detail), at});
    }

    void expireLocked(Timestamp now) {
        candidates.erase(
            std::remove_if(
                candidates.begin(), candidates.end(),
                [now](const Candidate& candidate) {
                    return now >= candidate.expires_at;
                }),
            candidates.end());
    }

    std::optional<SelectedMaster> selectBestLocked() const {
        const Candidate* best = nullptr;
        for (const Candidate& candidate : candidates) {
            if (best == nullptr) {
                best = &candidate;
                continue;
            }
            CandidateQualityView candidate_view{
                &candidate.announcement.quality,
                candidate.announcement.effectiveGrandmaster(),
                candidate.announcement.steps_removed,
                candidate.announcement.source,
            };
            CandidateQualityView best_view{
                &best->announcement.quality,
                best->announcement.effectiveGrandmaster(),
                best->announcement.steps_removed,
                best->announcement.source,
            };
            if (candidateIsBetter(candidate_view, best_view, policy.bmca_mode)) {
                best = &candidate;
            }
        }
        if (best == nullptr) {
            return std::nullopt;
        }
        return SelectedMaster{
            best->announcement.source,
            best->announcement.effectiveGrandmaster(),
            best->announcement.quality,
            best->announcement.sequence_id,
            best->announcement.steps_removed,
            best->received_at,
            best->expires_at,
        };
    }

    std::optional<SelectedMaster> refreshSelectionLocked(
        Timestamp now,
        std::vector<Alarm>& alarms) {
        auto best = selectBestLocked();
        if (best) {
            if (selected_source && *selected_source != best->source) {
                appendAlarm(
                    alarms, AlarmCode::MasterChanged, Severity::Info,
                    "selected master " + describeSource(best->source),
                    now);
            }
            selected_source = best->source;
            current = best;
            return best;
        }

        if (selected_source) {
            appendAlarm(
                alarms, AlarmCode::NoTrustedSource, Severity::Warning,
                "no admitted master remains within holdover",
                now);
            selected_source.reset();
        }
        current.reset();
        return std::nullopt;
    }

    static bool checkTimestamp(
        const Announcement& announcement,
        Timestamp received_at,
        const AdmissionPolicy& policy,
        RejectionReason& reason) {
        if (!policy.enforce_announcement_timestamps ||
            !announcement.transmit_time) {
            return true;
        }
        const Timestamp transmit = *announcement.transmit_time;
        if (transmit > received_at) {
            if (saturatingSub(transmit, received_at) > policy.max_future_skew) {
                reason = RejectionReason::FutureAnnouncement;
                return false;
            }
        } else if (saturatingSub(received_at, transmit) >
                   policy.max_announcement_age) {
            reason = RejectionReason::StaleAnnouncement;
            return false;
        }
        return true;
    }

    AdmissionResult submitLocked(
        const Announcement& announcement,
        Timestamp received_at) {
        AdmissionResult result;
        result.evaluated_at = received_at;
        logical_now = std::max(logical_now, received_at);
        has_received = true;

        RejectionReason reason = RejectionReason::None;
        std::string detail;

        auto reject = [&](RejectionReason rejection, std::string message) {
            reason = rejection;
            detail = std::move(message);
        };

        if (announcement.source.clock_identity.isZero() ||
            announcement.source.port_number == 0) {
            reject(
                RejectionReason::MalformedAnnouncement,
                "announcement has an invalid source port identity");
        } else if (announcement.version < policy.minimum_version ||
                   announcement.version > policy.maximum_version) {
            reject(
                RejectionReason::VersionUnsupported,
                "unsupported PTP version " +
                    std::to_string(announcement.version));
        } else if (announcement.domain != policy.domain) {
            reject(
                RejectionReason::DomainMismatch,
                "unexpected PTP domain " +
                    std::to_string(announcement.domain));
        }

        const TrustedMaster* trusted = nullptr;
        if (reason == RejectionReason::None) {
            trusted = findTrusted(announcement.source.clock_identity);
            if (trusted == nullptr) {
                reject(
                    RejectionReason::UntrustedSource,
                    "source " + describeSource(announcement.source) +
                        " is not configured as a trusted master");
            } else if (!trusted->enabled) {
                reject(
                    RejectionReason::TrustedSourceDisabled,
                    "trusted source " + describeSource(announcement.source) +
                        " is disabled");
            } else if (trusted->port_number &&
                       *trusted->port_number != announcement.source.port_number) {
                reject(
                    RejectionReason::UntrustedSource,
                    "source port is not the configured trusted port");
            }
        }

        if (reason == RejectionReason::None) {
            // A configured key always implies authentication. The policy can
            // only relax authentication for a trusted identity that has no key
            // material configured at all.
            const bool authenticationRequired =
                policy.require_authentication || !trusted->shared_secret.empty();
            if (!announcement.authentication) {
                if (authenticationRequired) {
                    reject(
                        RejectionReason::AuthenticationRequired,
                        "announcement is missing message authentication");
                }
            } else if (announcement.authentication->isEmpty()) {
                reject(
                    RejectionReason::AuthenticationRequired,
                    "announcement carries an empty authentication tag");
            } else {
                if (trusted->key_id &&
                    trusted->key_id != announcement.authentication->key_id) {
                    reject(
                        RejectionReason::KeyIdMismatch,
                        "announcement key id does not match the trusted key id");
                } else if (trusted->shared_secret.empty()) {
                    reject(
                        RejectionReason::AuthenticationFailed,
                        "trusted master has no configured authentication key");
                } else if (!verifyAuthentication(
                               announcement,
                               std::span<const std::uint8_t>(
                                   trusted->shared_secret.data(),
                                   trusted->shared_secret.size()),
                               trusted->key_id)) {
                    reject(
                        RejectionReason::AuthenticationFailed,
                        "announcement HMAC verification failed");
                }
            }
        }

        if (reason == RejectionReason::None &&
            !checkTimestamp(announcement, received_at, policy, reason)) {
            detail = reason == RejectionReason::FutureAnnouncement
                         ? "announcement transmit time is in the future"
                         : "announcement transmit time is stale";
        }

        if (reason == RejectionReason::None) {
            const ClockIdentity grandmaster =
                announcement.effectiveGrandmaster();
            if (trusted->expected_grandmaster_identity &&
                grandmaster != *trusted->expected_grandmaster_identity) {
                reject(
                    RejectionReason::GrandmasterMismatch,
                    "announced grandmaster does not match the pinned grandmaster");
            } else if (policy.require_grandmaster_in_trust_list) {
                const TrustedMaster* grandmaster_trust =
                    findTrusted(grandmaster);
                if (grandmaster_trust == nullptr ||
                    !grandmaster_trust->enabled) {
                    reject(
                        RejectionReason::GrandmasterMismatch,
                        "announced grandmaster is not in the trust list");
                }
            }
        }

        if (reason == RejectionReason::None &&
            !qualityAllowed(*trusted, announcement.quality, reason)) {
            detail = "announcement quality violates admission policy: " +
                     std::string(toString(reason));
        }

        ReplayState* replay = nullptr;
        if (reason == RejectionReason::None) {
            replay = findReplay(announcement.source);
            if (policy.enforce_sequence_monotonicity && replay != nullptr &&
                replay->initialized &&
                !sequenceIsNewer(announcement.sequence_id, replay->last_sequence)) {
                reject(
                    RejectionReason::ReplayDetected,
                    "announcement sequence " +
                        std::to_string(announcement.sequence_id) +
                        " reuses or rolls back the last accepted sequence");
            }
        }

        if (reason == RejectionReason::None) {
            if (replay == nullptr) {
                replay_states.push_back(ReplayState{
                    announcement.source, true, announcement.sequence_id,
                    received_at});
            } else {
                replay->initialized = true;
                replay->last_sequence = announcement.sequence_id;
                replay->last_received = received_at;
            }

            const Timestamp expires_at =
                saturatingAdd(received_at, policy.candidate_holdover);
            const auto existing = std::find_if(
                candidates.begin(), candidates.end(),
                [&announcement](const Candidate& candidate) {
                    return candidate.announcement.source ==
                           announcement.source;
                });
            if (existing == candidates.end()) {
                candidates.push_back(
                    Candidate{announcement, received_at, expires_at});
            } else {
                existing->announcement = announcement;
                existing->received_at = received_at;
                existing->expires_at =
                    std::max(existing->expires_at, expires_at);
            }

            result.accepted = true;
            result.reason = RejectionReason::None;
        } else {
            result.accepted = false;
            result.reason = reason;
            const AlarmCode code = alarmCodeFor(reason);
            if (code != AlarmCode::None) {
                appendAlarm(
                    result.alarms, code, severityFor(reason),
                    detail.empty()
                        ? "announcement rejected: " + std::string(toString(reason))
                        : std::move(detail),
                    received_at);
            }
        }

        expireLocked(received_at);
        result.selected = refreshSelectionLocked(received_at, result.alarms);
        return result;
    }

    AdmissionResult dispatch(AdmissionResult result) {
        std::function<void(const Alarm&)> sink;
        {
            std::lock_guard<std::mutex> lock(mutex);
            sink = alarm_sink;
        }
        if (sink) {
            for (const Alarm& alarm : result.alarms) {
                sink(alarm);
            }
        }
        return result;
    }

    AdmissionResult submit(
        const Announcement& announcement,
        Timestamp received_at) {
        AdmissionResult result;
        {
            std::lock_guard<std::mutex> lock(mutex);
            result = submitLocked(announcement, received_at);
        }
        return dispatch(std::move(result));
    }

    AdmissionResult submitAuto(const Announcement& announcement) {
        AdmissionResult result;
        {
            std::lock_guard<std::mutex> lock(mutex);
            const Timestamp received_at = has_received ? logical_now : Timestamp{0};
            result = submitLocked(announcement, received_at);
        }
        return dispatch(std::move(result));
    }

    std::optional<SelectedMaster> selectedAt(Timestamp now) {
        std::vector<Alarm> alarms;
        std::optional<SelectedMaster> selected;
        {
            std::lock_guard<std::mutex> lock(mutex);
            logical_now = std::max(logical_now, now);
            has_received = true;
            expireLocked(now);
            selected = refreshSelectionLocked(now, alarms);
        }
        std::function<void(const Alarm&)> sink;
        if (!alarms.empty()) {
            std::lock_guard<std::mutex> lock(mutex);
            sink = alarm_sink;
        }
        if (sink) {
            for (const Alarm& alarm : alarms) {
                sink(alarm);
            }
        }
        return selected;
    }

    void expireAt(Timestamp now) {
        std::vector<Alarm> alarms;
        {
            std::lock_guard<std::mutex> lock(mutex);
            logical_now = std::max(logical_now, now);
            has_received = true;
            expireLocked(now);
            refreshSelectionLocked(now, alarms);
        }
        std::function<void(const Alarm&)> sink;
        if (!alarms.empty()) {
            std::lock_guard<std::mutex> lock(mutex);
            sink = alarm_sink;
        }
        if (sink) {
            for (const Alarm& alarm : alarms) {
                sink(alarm);
            }
        }
    }
};

MasterClockAdmission::MasterClockAdmission(
    AdmissionPolicy policy,
    std::vector<TrustedMaster> trusted_masters)
    : impl_(std::make_unique<Impl>(std::move(policy), std::move(trusted_masters))) {}

MasterClockAdmission::MasterClockAdmission(
    std::vector<TrustedMaster> trusted_masters,
    AdmissionPolicy policy)
    : MasterClockAdmission(std::move(policy), std::move(trusted_masters)) {}

MasterClockAdmission::MasterClockAdmission(MasterClockAdmission&&) noexcept =
    default;
MasterClockAdmission& MasterClockAdmission::operator=(
    MasterClockAdmission&&) noexcept = default;
MasterClockAdmission::~MasterClockAdmission() = default;

bool MasterClockAdmission::addTrustedMaster(TrustedMaster master) {
    if (master.clock_identity.isZero()) {
        return false;
    }
    std::lock_guard<std::mutex> lock(impl_->mutex);
    if (impl_->findTrusted(master.clock_identity) != nullptr) {
        return false;
    }
    impl_->trusted_masters.push_back(std::move(master));
    return true;
}

bool MasterClockAdmission::upsertTrustedMaster(TrustedMaster master) {
    if (master.clock_identity.isZero()) {
        return false;
    }
    std::lock_guard<std::mutex> lock(impl_->mutex);
    const auto iterator = std::find_if(
        impl_->trusted_masters.begin(), impl_->trusted_masters.end(),
        [&master](const TrustedMaster& current) {
            return current.clock_identity == master.clock_identity;
        });
    if (iterator == impl_->trusted_masters.end()) {
        impl_->trusted_masters.push_back(std::move(master));
        return true;
    }
    const ClockIdentity identity = master.clock_identity;
    *iterator = std::move(master);

    impl_->candidates.erase(
        std::remove_if(
            impl_->candidates.begin(), impl_->candidates.end(),
            [&identity](const Impl::Candidate& candidate) {
                return candidate.announcement.source.clock_identity == identity;
            }),
        impl_->candidates.end());
    if (impl_->selected_source &&
        impl_->selected_source->clock_identity == identity) {
        impl_->selected_source.reset();
        impl_->current.reset();
    }
    return true;
}

bool MasterClockAdmission::removeTrustedMaster(const ClockIdentity& identity) {
    std::lock_guard<std::mutex> lock(impl_->mutex);
    const auto before = impl_->trusted_masters.size();
    impl_->trusted_masters.erase(
        std::remove_if(
            impl_->trusted_masters.begin(), impl_->trusted_masters.end(),
            [&identity](const TrustedMaster& master) {
                return master.clock_identity == identity;
            }),
        impl_->trusted_masters.end());
    if (impl_->trusted_masters.size() == before) {
        return false;
    }
    impl_->candidates.erase(
        std::remove_if(
            impl_->candidates.begin(), impl_->candidates.end(),
            [&identity](const Impl::Candidate& candidate) {
                return candidate.announcement.source.clock_identity == identity;
            }),
        impl_->candidates.end());
    impl_->replay_states.erase(
        std::remove_if(
            impl_->replay_states.begin(), impl_->replay_states.end(),
            [&identity](const Impl::ReplayState& state) {
                return state.source.clock_identity == identity;
            }),
        impl_->replay_states.end());
    if (impl_->selected_source &&
        impl_->selected_source->clock_identity == identity) {
        impl_->selected_source.reset();
        impl_->current.reset();
    }
    return true;
}

void MasterClockAdmission::clearTrustedMasters() {
    std::lock_guard<std::mutex> lock(impl_->mutex);
    impl_->trusted_masters.clear();
    impl_->candidates.clear();
    impl_->replay_states.clear();
    impl_->selected_source.reset();
    impl_->current.reset();
}

void MasterClockAdmission::resetSourceState(const ClockIdentity& identity) {
    std::lock_guard<std::mutex> lock(impl_->mutex);
    impl_->candidates.erase(
        std::remove_if(
            impl_->candidates.begin(), impl_->candidates.end(),
            [&identity](const Impl::Candidate& candidate) {
                return candidate.announcement.source.clock_identity == identity;
            }),
        impl_->candidates.end());
    impl_->replay_states.erase(
        std::remove_if(
            impl_->replay_states.begin(), impl_->replay_states.end(),
            [&identity](const Impl::ReplayState& state) {
                return state.source.clock_identity == identity;
            }),
        impl_->replay_states.end());
    if (impl_->selected_source &&
        impl_->selected_source->clock_identity == identity) {
        impl_->selected_source.reset();
        impl_->current.reset();
    }
}

std::vector<TrustedMaster> MasterClockAdmission::trustedMasters() const {
    std::lock_guard<std::mutex> lock(impl_->mutex);
    return impl_->trusted_masters;
}

AdmissionPolicy MasterClockAdmission::policy() const {
    std::lock_guard<std::mutex> lock(impl_->mutex);
    return impl_->policy;
}

void MasterClockAdmission::setPolicy(AdmissionPolicy policy) {
    std::lock_guard<std::mutex> lock(impl_->mutex);
    impl_->policy = std::move(policy);
    // Existing candidates were admitted under the previous policy. Force a
    // clean re-admission rather than grandfathering weaker announcements.
    impl_->candidates.clear();
    impl_->selected_source.reset();
    impl_->current.reset();
}

AdmissionResult MasterClockAdmission::submit(
    const Announcement& announcement,
    Timestamp received_at) {
    return impl_->submit(announcement, received_at);
}

AdmissionResult MasterClockAdmission::submit(const Announcement& announcement) {
    return impl_->submitAuto(announcement);
}

AdmissionResult MasterClockAdmission::submitAnnouncement(
    const Announcement& announcement,
    Timestamp received_at) {
    return impl_->submit(announcement, received_at);
}

AdmissionResult MasterClockAdmission::submitAnnouncement(
    const Announcement& announcement) {
    return impl_->submitAuto(announcement);
}

AdmissionResult MasterClockAdmission::admit(
    const Announcement& announcement,
    Timestamp received_at) {
    return impl_->submit(announcement, received_at);
}

AdmissionResult MasterClockAdmission::admit(const Announcement& announcement) {
    return impl_->submitAuto(announcement);
}

std::optional<SelectedMaster> MasterClockAdmission::selected(Timestamp now) {
    return impl_->selectedAt(now);
}

std::optional<SelectedMaster> MasterClockAdmission::bestMaster(Timestamp now) {
    return impl_->selectedAt(now);
}

std::optional<SelectedMaster> MasterClockAdmission::bestMaster() const {
    std::lock_guard<std::mutex> lock(impl_->mutex);
    return impl_->current;
}

std::optional<SelectedMaster> MasterClockAdmission::selectedMaster() const {
    std::lock_guard<std::mutex> lock(impl_->mutex);
    return impl_->current;
}

std::optional<SelectedMaster> MasterClockAdmission::currentMaster() const {
    std::lock_guard<std::mutex> lock(impl_->mutex);
    return impl_->current;
}

void MasterClockAdmission::expire(Timestamp now) {
    impl_->expireAt(now);
}

std::size_t MasterClockAdmission::admittedCandidateCount() const {
    std::lock_guard<std::mutex> lock(impl_->mutex);
    return impl_->candidates.size();
}

void MasterClockAdmission::setAlarmSink(std::function<void(const Alarm&)> sink) {
    std::lock_guard<std::mutex> lock(impl_->mutex);
    impl_->alarm_sink = std::move(sink);
}

// ---------------------------------------------------------------------------
// Bounded monotonic time correction
// ---------------------------------------------------------------------------

struct BoundedMonotonicClock::Impl {
    Impl(ClockSafetyPolicy initial_policy, Timestamp initial)
        : policy(std::move(initial_policy)), initial_time(initial) {}

    ClockSafetyPolicy policy;
    Timestamp initial_time{0};
    bool anchored{false};
    Timestamp base_local{0};
    Timestamp base_time{0};
    Timestamp last_local{0};
    Timestamp last_output{0};
    double measured_ppm{0.0};
    double rate_ppm{0.0};
    std::optional<std::int32_t> last_utc_offset;
    std::function<void(const Alarm&)> alarm_sink;
    mutable std::mutex mutex;

    void appendAlarm(
        std::vector<Alarm>& alarms,
        AlarmCode code,
        Severity severity,
        std::string detail,
        Timestamp at) const {
        alarms.push_back(Alarm{code, severity, std::move(detail), at});
    }

    Timestamp nowLocked(Timestamp local_now) {
        if (anchored && local_now < last_local) {
            local_now = last_local;
        }
        if (!anchored) {
            anchored = true;
            base_local = local_now;
            base_time = initial_time;
            last_local = local_now;
            last_output = initial_time;
            return last_output;
        }

        const double elapsed_seconds =
            std::chrono::duration<double>(saturatingSub(local_now, base_local))
                .count();
        const double delta_ns =
            elapsed_seconds * 1.0e9 * (1.0 + rate_ppm * 1.0e-6);
        Timestamp predicted{
            saturatingAdd(base_time.count(), clampToInt64(delta_ns))};
        if (predicted < last_output) {
            predicted = last_output;
        }
        last_local = local_now;
        last_output = predicted;
        return predicted;
    }

    double clampedSlew(double value) const {
        const double maximum = std::max(0.0, finiteOr(policy.max_slew_ppm, 0.0));
        value = std::max(-maximum, std::min(maximum, value));
        // Keep the affine rate strictly positive so time never runs backward.
        return std::max(value, -999000.0);
    }

    double clampedDrift(double value) const {
        const double maximum = std::max(0.0, finiteOr(policy.max_drift_ppm, 0.0));
        return std::max(-maximum, std::min(maximum, value));
    }

    TimeUpdateResult synchronizeLocked(const SyncSample& sample) {
        TimeUpdateResult result;
        result.target = sample.master_time;
        result.offset = Timestamp{0};
        result.applied_step = Timestamp{0};

        if (sample.uncertainty < Timestamp{0}) {
            result.accepted = false;
            appendAlarm(
                result.alarms, AlarmCode::InvalidTimeSample, Severity::Critical,
                "negative synchronization uncertainty", sample.local_time);
            return result;
        }

        Timestamp local = sample.local_time;
        if (anchored && local < last_local) {
            local = last_local;
        }
        if (!anchored) {
            anchored = true;
            base_local = local;
            base_time = initial_time;
            last_local = local;
            last_output = initial_time;
        } else {
            nowLocked(local);
        }

        const Timestamp before = last_output;
        result.before = before;
        const Timestamp raw_offset = saturatingSub(sample.master_time, before);
        result.offset = raw_offset;

        if (sample.uncertainty > policy.max_uncertainty) {
            appendAlarm(
                result.alarms, AlarmCode::UncertaintyExceedsLimit,
                Severity::Warning,
                "synchronization uncertainty exceeds the configured bound",
                local);
        }

        if (sample.frequency_ppm) {
            if (!std::isfinite(*sample.frequency_ppm)) {
                appendAlarm(
                    result.alarms, AlarmCode::InvalidTimeSample,
                    Severity::Warning,
                    "non-finite frequency correction in synchronization sample",
                    local);
            } else {
                measured_ppm = *sample.frequency_ppm;
            }
            if (std::isfinite(measured_ppm) &&
                std::abs(measured_ppm) > policy.max_drift_ppm) {
                appendAlarm(
                    result.alarms, AlarmCode::DriftExceedsLimit,
                    Severity::Warning,
                    "measured frequency drift exceeds the configured bound",
                    local);
            }
            measured_ppm = clampedDrift(measured_ppm);
        }

        bool leapPositive = false;
        if (sample.leap_seconds) {
            std::int8_t leap = *sample.leap_seconds;
            if (leap < -1 || leap > 1) {
                appendAlarm(
                    result.alarms, AlarmCode::LeapSecondOutOfBounds,
                    Severity::Critical,
                    "leap-second adjustment is outside [-1, 1]",
                    local);
                leap = leap < 0 ? -1 : 1;
            }
            leapPositive = leap > 0;
            appendAlarm(
                result.alarms, AlarmCode::LeapSecond, Severity::Info,
                leap > 0 ? "leap second insertion announced"
                         : (leap < 0 ? "leap second deletion announced"
                                     : "leap second state cleared"),
                local);
        }

        if (sample.utc_offset_seconds) {
            if (last_utc_offset &&
                *last_utc_offset != *sample.utc_offset_seconds) {
                const std::int64_t delta =
                    static_cast<std::int64_t>(*sample.utc_offset_seconds) -
                    static_cast<std::int64_t>(*last_utc_offset);
                if (delta < -1 || delta > 1) {
                    appendAlarm(
                        result.alarms, AlarmCode::LeapSecondOutOfBounds,
                        Severity::Critical,
                        "UTC offset changed by more than one second",
                        local);
                } else {
                    appendAlarm(
                        result.alarms, AlarmCode::LeapSecond, Severity::Info,
                        "UTC offset changed by one second", local);
                    leapPositive = leapPositive || delta > 0;
                }
            }
            last_utc_offset = *sample.utc_offset_seconds;
        }

        const std::int64_t raw = raw_offset.count();
        std::int64_t step = 0;
        std::int64_t remaining = 0;
        bool reject_correction = false;

        const auto nonNegative = [](Timestamp value) {
            return Timestamp{std::max<std::int64_t>(0, value.count())};
        };
        const std::int64_t step_limit =
            policy.allow_forward_step
                ? nonNegative(policy.max_step).count()
                : 0;
        const std::int64_t leap_allowance =
            leapPositive ? nonNegative(policy.max_leap_adjustment).count() : 0;
        const std::int64_t forward_limit =
            saturatingAdd(step_limit, leap_allowance);

        if (raw > 0) {
            if (raw <= forward_limit) {
                step = raw;
            } else {
                switch (policy.jump_policy) {
                    case JumpPolicy::Reject:
                        reject_correction = true;
                        appendAlarm(
                            result.alarms, AlarmCode::TimeJumpExceedsLimit,
                            Severity::Critical,
                            "forward time jump exceeds the configured step bound",
                            local);
                        break;
                    case JumpPolicy::Clamp:
                        step = forward_limit;
                        remaining = raw - step;
                        appendAlarm(
                            result.alarms, AlarmCode::TimeJumpExceedsLimit,
                            Severity::Warning,
                            "forward time jump clamped to the configured step bound",
                            local);
                        break;
                    case JumpPolicy::Slew:
                        remaining = raw;
                        appendAlarm(
                            result.alarms, AlarmCode::TimeJumpExceedsLimit,
                            Severity::Warning,
                            "forward time jump will be slewed within the rate bound",
                            local);
                        break;
                }
            }
        } else if (raw < 0) {
            const std::uint64_t backwards = magnitude(raw);
            appendAlarm(
                result.alarms, AlarmCode::BackwardCorrectionSuppressed,
                Severity::Info,
                "backward correction can never move output time backwards",
                local);
            if (backwards > static_cast<std::uint64_t>(forward_limit)) {
                appendAlarm(
                    result.alarms, AlarmCode::TimeJumpExceedsLimit,
                    Severity::Warning,
                    "backward time jump exceeds the configured bound",
                    local);
                if (policy.jump_policy == JumpPolicy::Reject) {
                    reject_correction = true;
                }
            }
            if (!reject_correction) {
                remaining = raw;
            }
        }

        const Timestamp after = step == 0
                                    ? before
                                    : saturatingAdd(before, Timestamp{step});
        result.after = after;
        result.applied_step = Timestamp{step};
        result.stepped = step != 0;
        result.correction_applied = step != 0 || remaining != 0;
        result.jump_rejected = reject_correction;
        result.monotonic = after >= before;

        double correction_ppm = 0.0;
        if (remaining != 0) {
            const double horizon =
                std::max(1.0, finiteOr(policy.correction_horizon_seconds, 60.0));
            correction_ppm =
                (static_cast<double>(remaining) / (horizon * 1.0e9)) * 1.0e6;
        }
        const double total_ppm =
            clampedSlew(measured_ppm + correction_ppm);

        base_local = local;
        base_time = after;
        last_local = local;
        last_output = after;
        rate_ppm = total_ppm;

        result.applied_rate_ppm = rate_ppm;
        result.accepted = true;
        return result;
    }

    TimeUpdateResult synchronize(const SyncSample& sample) {
        TimeUpdateResult result;
        {
            std::lock_guard<std::mutex> lock(mutex);
            result = synchronizeLocked(sample);
        }
        std::function<void(const Alarm&)> sink;
        {
            std::lock_guard<std::mutex> lock(mutex);
            sink = alarm_sink;
        }
        if (sink) {
            for (const Alarm& alarm : result.alarms) {
                sink(alarm);
            }
        }
        return result;
    }
};

BoundedMonotonicClock::BoundedMonotonicClock(
    ClockSafetyPolicy policy,
    Timestamp initial_time)
    : impl_(std::make_unique<Impl>(std::move(policy), initial_time)) {}

BoundedMonotonicClock::BoundedMonotonicClock(
    Timestamp initial_time,
    ClockSafetyPolicy policy)
    : BoundedMonotonicClock(std::move(policy), initial_time) {}

BoundedMonotonicClock::~BoundedMonotonicClock() = default;
BoundedMonotonicClock::BoundedMonotonicClock(BoundedMonotonicClock&&) noexcept =
    default;
BoundedMonotonicClock& BoundedMonotonicClock::operator=(
    BoundedMonotonicClock&&) noexcept = default;

Timestamp BoundedMonotonicClock::now(Timestamp local_now) const {
    std::lock_guard<std::mutex> lock(impl_->mutex);
    return impl_->nowLocked(local_now);
}

TimeUpdateResult BoundedMonotonicClock::synchronize(const SyncSample& sample) {
    return impl_->synchronize(sample);
}

TimeUpdateResult BoundedMonotonicClock::applyTimeUpdate(
    const SyncSample& sample) {
    return impl_->synchronize(sample);
}

ClockSafetyPolicy BoundedMonotonicClock::policy() const {
    std::lock_guard<std::mutex> lock(impl_->mutex);
    return impl_->policy;
}

void BoundedMonotonicClock::setPolicy(ClockSafetyPolicy policy) {
    std::lock_guard<std::mutex> lock(impl_->mutex);
    impl_->policy = std::move(policy);
    impl_->rate_ppm = impl_->clampedSlew(impl_->rate_ppm);
}

void BoundedMonotonicClock::setAlarmSink(
    std::function<void(const Alarm&)> sink) {
    std::lock_guard<std::mutex> lock(impl_->mutex);
    impl_->alarm_sink = std::move(sink);
}

// ---------------------------------------------------------------------------
// TimeSyncGuard
// ---------------------------------------------------------------------------

struct TimeSyncGuard::Impl {
    struct AlarmHub {
        std::mutex mutex;
        std::function<void(const Alarm&)> sink;

        void emit(const Alarm& alarm) {
            std::function<void(const Alarm&)> copy;
            {
                std::lock_guard<std::mutex> lock(mutex);
                copy = sink;
            }
            if (copy) {
                copy(alarm);
            }
        }
    };

    Impl(
        AdmissionPolicy admission_policy,
        std::vector<TrustedMaster> trusted_masters,
        ClockSafetyPolicy clock_policy,
        Timestamp initial_time)
        : hub(std::make_shared<AlarmHub>()),
          admission(std::make_unique<MasterClockAdmission>(
              std::move(admission_policy), std::move(trusted_masters))),
          clock(std::make_unique<BoundedMonotonicClock>(
              std::move(clock_policy), initial_time)) {
        const auto shared_hub = hub;
        admission->setAlarmSink([shared_hub](const Alarm& alarm) {
            shared_hub->emit(alarm);
        });
        clock->setAlarmSink([shared_hub](const Alarm& alarm) {
            shared_hub->emit(alarm);
        });
    }

    std::shared_ptr<AlarmHub> hub;
    std::unique_ptr<MasterClockAdmission> admission;
    std::unique_ptr<BoundedMonotonicClock> clock;

    TimeUpdateResult rejectSync(
        const SyncSample& sample,
        RejectionReason reason,
        AlarmCode code,
        std::string detail) {
        TimeUpdateResult result;
        result.accepted = false;
        result.before = clock->now(sample.local_time);
        result.after = result.before;
        result.target = sample.master_time;
        result.offset = saturatingSub(sample.master_time, result.before);
        result.monotonic = true;
        result.alarms.push_back(
            Alarm{code, Severity::Critical, std::move(detail), sample.local_time});
        (void)reason;
        return result;
    }
};

TimeSyncGuard::TimeSyncGuard(
    AdmissionPolicy admission_policy,
    std::vector<TrustedMaster> trusted_masters,
    ClockSafetyPolicy clock_policy,
    Timestamp initial_time)
    : impl_(std::make_unique<Impl>(
          std::move(admission_policy),
          std::move(trusted_masters),
          std::move(clock_policy),
          initial_time)) {}

TimeSyncGuard::TimeSyncGuard(
    std::vector<TrustedMaster> trusted_masters,
    ClockSafetyPolicy clock_policy,
    Timestamp initial_time)
    : TimeSyncGuard(
          AdmissionPolicy{},
          std::move(trusted_masters),
          std::move(clock_policy),
          initial_time) {}

TimeSyncGuard::~TimeSyncGuard() = default;
TimeSyncGuard::TimeSyncGuard(TimeSyncGuard&&) noexcept = default;
TimeSyncGuard& TimeSyncGuard::operator=(TimeSyncGuard&&) noexcept = default;

AdmissionResult TimeSyncGuard::submitAnnouncement(
    const Announcement& announcement,
    Timestamp received_at) {
    return impl_->admission->submit(announcement, received_at);
}

AdmissionResult TimeSyncGuard::submitAnnouncement(
    const Announcement& announcement) {
    return impl_->admission->submit(announcement);
}

TimeUpdateResult TimeSyncGuard::submitSync(const SyncSample& sample) {
    const auto selected = impl_->admission->selected(sample.local_time);
    if (!selected) {
        auto result = impl_->rejectSync(
            sample, RejectionReason::NoSelectedMaster,
            AlarmCode::SyncSourceMismatch,
            "time synchronization rejected because no master is currently selected");
        // Ensure alarms also reach the guard's sink.
        for (const Alarm& alarm : result.alarms) {
            impl_->hub->emit(alarm);
        }
        return result;
    }
    if (sample.source.clock_identity != selected->source.clock_identity ||
        sample.source.port_number != selected->source.port_number) {
        auto result = impl_->rejectSync(
            sample, RejectionReason::SyncSourceMismatch,
            AlarmCode::SyncSourceMismatch,
            "time synchronization source does not match the selected master");
        for (const Alarm& alarm : result.alarms) {
            impl_->hub->emit(alarm);
        }
        return result;
    }
    return impl_->clock->synchronize(sample);
}

TimeUpdateResult TimeSyncGuard::submitSync(
    const ClockIdentity& source,
    const SyncSample& sample) {
    SyncSample bound = sample;
    const auto selected = impl_->admission->selected(sample.local_time);
    if (selected && selected->source.clock_identity == source) {
        bound.source = selected->source;
    } else {
        bound.source.clock_identity = source;
        if (bound.source.port_number == 0) {
            bound.source.port_number = 1;
        }
    }
    return submitSync(bound);
}

Timestamp TimeSyncGuard::now(Timestamp local_now) const {
    return impl_->clock->now(local_now);
}

std::optional<SelectedMaster> TimeSyncGuard::selectedMaster(Timestamp now) {
    return impl_->admission->selected(now);
}

MasterClockAdmission& TimeSyncGuard::admission() {
    return *impl_->admission;
}

const MasterClockAdmission& TimeSyncGuard::admission() const {
    return *impl_->admission;
}

BoundedMonotonicClock& TimeSyncGuard::clock() {
    return *impl_->clock;
}

const BoundedMonotonicClock& TimeSyncGuard::clock() const {
    return *impl_->clock;
}

void TimeSyncGuard::setAlarmSink(std::function<void(const Alarm&)> sink) {
    std::lock_guard<std::mutex> lock(impl_->hub->mutex);
    impl_->hub->sink = std::move(sink);
}

}  // namespace netsec
