#include "session_rules.hpp"

#include <array>
#include <cstdint>
#include <cstring>
#include <sstream>
#include <iomanip>

namespace netsec::session_rule::detail {
namespace {

// SHA-256 (FIPS 180-4), implemented locally so the component has no external
// cryptographic dependency. HMAC-SHA256 is built on top of it.

constexpr std::array<std::uint32_t, 64> kSha256K = {
    0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1,
    0x923f82a4, 0xab1c5ed5, 0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3,
    0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174, 0xe49b69c1, 0xefbe4786,
    0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
    0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147,
    0x06ca6351, 0x14292967, 0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13,
    0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85, 0xa2bfe8a1, 0xa81a664b,
    0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
    0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a,
    0x5b9cca4f, 0x682e6ff3, 0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208,
    0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2};

constexpr std::uint32_t rotr(std::uint32_t value, std::uint32_t shift) noexcept {
    return (value >> shift) | (value << (32U - shift));
}

class Sha256 {
public:
    Sha256() { reset(); }

    void update(const std::uint8_t* data, std::size_t length) noexcept {
        for (std::size_t i = 0; i < length; ++i) {
            buffer_[buffer_length_++] = data[i];
            if (buffer_length_ == 64U) {
                transform(buffer_.data());
                bit_length_ += 512ULL;
                buffer_length_ = 0;
            }
        }
    }

    std::array<std::uint8_t, 32> finalize() noexcept {
        const std::uint64_t bit_length =
            bit_length_ + (static_cast<std::uint64_t>(buffer_length_) * 8ULL);
        buffer_[buffer_length_++] = 0x80U;
        if (buffer_length_ > 56U) {
            while (buffer_length_ < 64U) {
                buffer_[buffer_length_++] = 0U;
            }
            transform(buffer_.data());
            buffer_length_ = 0;
        }
        while (buffer_length_ < 56U) {
            buffer_[buffer_length_++] = 0U;
        }
        for (int i = 7; i >= 0; --i) {
            buffer_[buffer_length_++] =
                static_cast<std::uint8_t>((bit_length >> (i * 8)) & 0xFFU);
        }
        transform(buffer_.data());

        std::array<std::uint8_t, 32> digest{};
        for (std::size_t i = 0; i < 8U; ++i) {
            digest[i * 4U] = static_cast<std::uint8_t>((state_[i] >> 24) & 0xFFU);
            digest[i * 4U + 1U] = static_cast<std::uint8_t>((state_[i] >> 16) & 0xFFU);
            digest[i * 4U + 2U] = static_cast<std::uint8_t>((state_[i] >> 8) & 0xFFU);
            digest[i * 4U + 3U] = static_cast<std::uint8_t>(state_[i] & 0xFFU);
        }
        return digest;
    }

private:
    void reset() noexcept {
        state_ = {0x6a09e667U, 0xbb67ae85U, 0x3c6ef372U, 0xa54ff53aU,
                  0x510e527fU, 0x9b05688cU, 0x1f83d9abU, 0x5be0cd19U};
        bit_length_ = 0;
        buffer_length_ = 0;
    }

    void transform(const std::uint8_t block[64]) noexcept {
        std::array<std::uint32_t, 64> words{};
        for (std::size_t i = 0; i < 16U; ++i) {
            words[i] = (static_cast<std::uint32_t>(block[i * 4U]) << 24) |
                       (static_cast<std::uint32_t>(block[i * 4U + 1U]) << 16) |
                       (static_cast<std::uint32_t>(block[i * 4U + 2U]) << 8) |
                       static_cast<std::uint32_t>(block[i * 4U + 3U]);
        }
        for (std::size_t i = 16U; i < 64U; ++i) {
            const std::uint32_t s0 =
                rotr(words[i - 15U], 7) ^ rotr(words[i - 15U], 18) ^ (words[i - 15U] >> 3);
            const std::uint32_t s1 =
                rotr(words[i - 2U], 17) ^ rotr(words[i - 2U], 19) ^ (words[i - 2U] >> 10);
            words[i] = words[i - 16U] + s0 + words[i - 7U] + s1;
        }

        std::uint32_t a = state_[0];
        std::uint32_t b = state_[1];
        std::uint32_t c = state_[2];
        std::uint32_t d = state_[3];
        std::uint32_t e = state_[4];
        std::uint32_t f = state_[5];
        std::uint32_t g = state_[6];
        std::uint32_t h = state_[7];

        for (std::size_t i = 0; i < 64U; ++i) {
            const std::uint32_t s1 = rotr(e, 6) ^ rotr(e, 11) ^ rotr(e, 25);
            const std::uint32_t ch = (e & f) ^ ((~e) & g);
            const std::uint32_t temp1 = h + s1 + ch + kSha256K[i] + words[i];
            const std::uint32_t s0 = rotr(a, 2) ^ rotr(a, 13) ^ rotr(a, 22);
            const std::uint32_t maj = (a & b) ^ (a & c) ^ (b & c);
            const std::uint32_t temp2 = s0 + maj;
            h = g;
            g = f;
            f = e;
            e = d + temp1;
            d = c;
            c = b;
            b = a;
            a = temp1 + temp2;
        }

        state_[0] += a;
        state_[1] += b;
        state_[2] += c;
        state_[3] += d;
        state_[4] += e;
        state_[5] += f;
        state_[6] += g;
        state_[7] += h;
    }

    std::array<std::uint32_t, 8> state_{};
    std::array<std::uint8_t, 64> buffer_{};
    std::size_t buffer_length_ = 0;
    std::uint64_t bit_length_ = 0;
};

std::array<std::uint8_t, 32> sha256(const std::uint8_t* data, std::size_t length) {
    Sha256 hash;
    hash.update(data, length);
    return hash.finalize();
}

// Constant-time comparison so MAC verification does not leak a timing signal.
bool constantTimeEqual(const std::array<std::uint8_t, 32>& lhs,
                       const std::array<std::uint8_t, 32>& rhs) noexcept {
    std::uint8_t difference = 0;
    for (std::size_t i = 0; i < 32U; ++i) {
        difference = static_cast<std::uint8_t>(difference | (lhs[i] ^ rhs[i]));
    }
    return difference == 0U;
}

std::array<std::uint8_t, 32> hmacSha256(std::string_view key,
                                        const std::vector<std::uint8_t>& message) {
    std::array<std::uint8_t, 64> block{};
    if (key.size() > 64U) {
        const auto digest =
            sha256(reinterpret_cast<const std::uint8_t*>(key.data()), key.size());
        std::copy(digest.begin(), digest.end(), block.begin());
    } else {
        std::copy(key.begin(), key.end(), block.begin());
    }

    std::array<std::uint8_t, 64> ipad{};
    std::array<std::uint8_t, 64> opad{};
    for (std::size_t i = 0; i < 64U; ++i) {
        ipad[i] = static_cast<std::uint8_t>(block[i] ^ 0x36U);
        opad[i] = static_cast<std::uint8_t>(block[i] ^ 0x5cU);
    }

    Sha256 inner;
    inner.update(ipad.data(), ipad.size());
    inner.update(message.data(), message.size());
    const auto inner_digest = inner.finalize();

    Sha256 outer;
    outer.update(opad.data(), opad.size());
    outer.update(inner_digest.data(), inner_digest.size());
    return outer.finalize();
}

// Deterministic canonical encoding. Every authenticated field is prefixed by
// an explicit tag and length so field injection / confusion attacks cannot
// produce a colliding representation.
class CanonicalEncoder {
public:
    void text(std::uint8_t tag, std::string_view value) {
        putByte(tag);
        putU64(value.size());
        bytes_.insert(bytes_.end(), value.begin(), value.end());
    }

    void u8(std::uint8_t tag, std::uint8_t value) {
        putByte(tag);
        putByte(value);
    }

    void u64(std::uint8_t tag, std::uint64_t value) {
        putByte(tag);
        putU64(value);
    }

    const std::vector<std::uint8_t>& bytes() const noexcept { return bytes_; }

private:
    void putByte(std::uint8_t value) { bytes_.push_back(value); }

    void putU64(std::uint64_t value) {
        for (int shift = 56; shift >= 0; shift -= 8) {
            bytes_.push_back(static_cast<std::uint8_t>((value >> shift) & 0xFFU));
        }
    }

    std::vector<std::uint8_t> bytes_;
};

constexpr std::uint8_t kTagDomain = 0x01;
constexpr std::uint8_t kTagSession = 0x10;
constexpr std::uint8_t kTagUser = 0x11;
constexpr std::uint8_t kTagNode = 0x12;
constexpr std::uint8_t kTagSeq = 0x13;
constexpr std::uint8_t kTagRuleId = 0x20;
constexpr std::uint8_t kTagCharging = 0x21;
constexpr std::uint8_t kTagForwarding = 0x22;
constexpr std::uint8_t kTagRedirect = 0x23;
constexpr std::uint8_t kTagFilter = 0x24;
constexpr std::uint8_t kTagState = 0x30;

void appendRuleBody(CanonicalEncoder& encoder, const RuleBody& rule) {
    encoder.text(kTagRuleId, rule.rule_id);
    encoder.u8(kTagCharging, static_cast<std::uint8_t>(rule.charging));
    encoder.u8(kTagForwarding, static_cast<std::uint8_t>(rule.forwarding));
    encoder.text(kTagRedirect, rule.redirect_target);
    encoder.text(kTagFilter, rule.traffic_filter);
}

std::vector<std::uint8_t> ruleEncoding(const RuleMessage& message) {
    CanonicalEncoder encoder;
    encoder.text(kTagDomain, "netsec.session-rule.v1/rule");
    encoder.text(kTagSession, message.session_id);
    encoder.text(kTagUser, message.user_id);
    encoder.text(kTagNode, message.node_id);
    encoder.u64(kTagSeq, message.seq);
    appendRuleBody(encoder, message.rule);
    return encoder.bytes();
}

std::vector<std::uint8_t> readbackEncoding(const ReadbackReport& report) {
    CanonicalEncoder encoder;
    encoder.text(kTagDomain, "netsec.session-rule.v1/readback");
    encoder.text(kTagSession, report.session_id);
    encoder.text(kTagUser, report.user_id);
    encoder.text(kTagNode, report.node_id);
    encoder.u64(kTagSeq, report.seq);
    appendRuleBody(encoder, report.rule);
    encoder.u8(kTagState, static_cast<std::uint8_t>(report.state));
    return encoder.bytes();
}

Mac toMac(const std::array<std::uint8_t, 32>& raw) noexcept {
    Mac mac{};
    for (std::size_t i = 0; i < kMacSize; ++i) {
        mac[i] = static_cast<std::byte>(raw[i]);
    }
    return mac;
}

std::array<std::uint8_t, 32> fromMac(const Mac& mac) noexcept {
    std::array<std::uint8_t, 32> raw{};
    for (std::size_t i = 0; i < kMacSize; ++i) {
        raw[i] = static_cast<std::uint8_t>(mac[i]);
    }
    return raw;
}

} // namespace
} // namespace netsec::session_rule::detail

namespace netsec::session_rule {

Mac computeRuleMac(const RuleMessage& message, std::string_view ingress_key) {
    const auto encoding = detail::ruleEncoding(message);
    return detail::toMac(detail::hmacSha256(ingress_key, encoding));
}

void signRuleMessage(RuleMessage& message, std::string_view ingress_key) {
    message.mac = computeRuleMac(message, ingress_key);
}

bool verifyRuleMessageMac(const RuleMessage& message, std::string_view ingress_key) {
    const auto expected = detail::fromMac(computeRuleMac(message, ingress_key));
    return detail::constantTimeEqual(expected, detail::fromMac(message.mac));
}

Mac computeReadbackMac(const ReadbackReport& report, std::string_view egress_key) {
    const auto encoding = detail::readbackEncoding(report);
    return detail::toMac(detail::hmacSha256(egress_key, encoding));
}

void signReadbackReport(ReadbackReport& report, std::string_view egress_key) {
    report.mac = computeReadbackMac(report, egress_key);
}

bool verifyReadbackMac(const ReadbackReport& report, std::string_view egress_key) {
    const auto expected = detail::fromMac(computeReadbackMac(report, egress_key));
    return detail::constantTimeEqual(expected, detail::fromMac(report.mac));
}

namespace {

Result reject(RejectReason reason, std::string detail) {
    return Result{false, reason, std::move(detail)};
}

Result accept() {
    return Result{true, RejectReason::Ok, "accepted"};
}

} // namespace

SessionRuleEngine::SessionRuleEngine(EngineIdentity identity)
    : identity_(std::move(identity)) {
    for (const auto& [node_id, key] : identity_.authorized_nodes) {
        last_sequence_.emplace(node_id, 0);
    }
}

const SessionRuleEngine::StoredRule*
SessionRuleEngine::findLocked(std::string_view rule_id) const {
    const auto it = rules_.find(std::string(rule_id));
    return it == rules_.end() ? nullptr : &it->second;
}

SessionRuleEngine::StoredRule*
SessionRuleEngine::findLocked(std::string_view rule_id) {
    const auto it = rules_.find(std::string(rule_id));
    return it == rules_.end() ? nullptr : &it->second;
}

Result SessionRuleEngine::ingest(const RuleMessage& message) {
    std::lock_guard lock(mutex_);

    // 1. The sender must be an authorized control-plane node for this session.
    const auto node_it = identity_.authorized_nodes.find(message.node_id);
    if (node_it == identity_.authorized_nodes.end()) {
        return reject(RejectReason::UnauthorizedNode,
                      "node '" + message.node_id + "' is not authorized for this session");
    }

    // 2. The message must be bound to this component's session and user.
    if (message.session_id != identity_.session_id) {
        return reject(RejectReason::SessionMismatch,
                      "message session '" + message.session_id + "' does not match '" +
                          identity_.session_id + "'");
    }
    if (message.user_id != identity_.user_id) {
        return reject(RejectReason::UserMismatch,
                      "message user '" + message.user_id + "' does not match '" +
                          identity_.user_id + "'");
    }

    // 3. Authenticate the sender and integrity of every field with the
    //    node-specific ingress key. This rejects forged and tampered messages.
    if (!verifyRuleMessageMac(message, node_it->second)) {
        return reject(RejectReason::BadMac,
                      "rule message MAC verification failed for node '" + message.node_id + "'");
    }

    // 4. Sequence numbers are monotonic per authorized node; reject replay and
    //    rollback attempts before any state is touched.
    const std::uint64_t last_seq = last_sequence_[message.node_id];
    if (message.seq < last_seq) {
        return reject(RejectReason::StaleSequence,
                      "sequence " + std::to_string(message.seq) +
                          " is older than last accepted " + std::to_string(last_seq));
    }
    if (message.seq == last_seq) {
        return reject(RejectReason::DuplicateSequence,
                      "sequence " + std::to_string(message.seq) +
                          " was already accepted from node '" + message.node_id + "'");
    }

    // 5. Store in Pending state. The rule cannot affect traffic until its
    //    signed read-back is confirmed through activate().
    StoredRule stored;
    stored.body = message.rule;
    stored.node_id = message.node_id;
    stored.seq = message.seq;
    stored.state = RuleState::Pending;
    rules_[message.rule.rule_id] = stored;
    last_sequence_[message.node_id] = message.seq;

    return accept();
}

std::optional<ReadbackReport>
SessionRuleEngine::readback(std::string_view rule_id) const {
    std::lock_guard lock(mutex_);
    const StoredRule* stored = findLocked(rule_id);
    if (stored == nullptr) {
        return std::nullopt;
    }

    ReadbackReport report;
    report.session_id = identity_.session_id;
    report.user_id = identity_.user_id;
    report.node_id = stored->node_id;
    report.seq = stored->seq;
    report.rule = stored->body;
    report.state = stored->state;
    signReadbackReport(report, identity_.egress_key);
    return report;
}

Result SessionRuleEngine::verifyReadback(const ReadbackReport& report,
                                         const RuleBody& expected) const {
    std::lock_guard lock(mutex_);

    if (!verifyReadbackMac(report, identity_.egress_key)) {
        return reject(RejectReason::BadMac, "read-back report MAC verification failed");
    }
    if (report.session_id != identity_.session_id) {
        return reject(RejectReason::SessionMismatch,
                      "read-back session '" + report.session_id + "' does not match '" +
                          identity_.session_id + "'");
    }
    if (report.user_id != identity_.user_id) {
        return reject(RejectReason::UserMismatch,
                      "read-back user '" + report.user_id + "' does not match '" +
                          identity_.user_id + "'");
    }

    const StoredRule* stored = findLocked(report.rule.rule_id);
    if (stored == nullptr) {
        return reject(RejectReason::UnknownRule,
                      "rule '" + report.rule.rule_id + "' is not held by this session");
    }
    if (report.rule != expected) {
        return reject(RejectReason::ReadbackMismatch,
                      "read-back rule '" + report.rule.rule_id +
                          "' differs from the intended rule");
    }
    if (report.rule != stored->body || report.node_id != stored->node_id ||
        report.seq != stored->seq || report.state != stored->state) {
        return reject(RejectReason::ReadbackMismatch,
                      "read-back report for rule '" + report.rule.rule_id +
                          "' does not match the stored rule state");
    }
    return accept();
}

Result SessionRuleEngine::activate(const ReadbackReport& report) {
    std::lock_guard lock(mutex_);

    if (!verifyReadbackMac(report, identity_.egress_key)) {
        return reject(RejectReason::BadMac, "read-back report MAC verification failed");
    }
    if (report.session_id != identity_.session_id || report.user_id != identity_.user_id) {
        return reject(RejectReason::SessionMismatch,
                      "read-back report is not bound to this session and user");
    }

    StoredRule* stored = findLocked(report.rule.rule_id);
    if (stored == nullptr) {
        return reject(RejectReason::UnknownRule,
                      "rule '" + report.rule.rule_id + "' is not held by this session");
    }
    if (stored->state == RuleState::Active) {
        return reject(RejectReason::AlreadyActive,
                      "rule '" + report.rule.rule_id + "' is already active");
    }
    if (report.rule != stored->body || report.node_id != stored->node_id ||
        report.seq != stored->seq || report.state != RuleState::Pending) {
        return reject(RejectReason::ReadbackMismatch,
                      "read-back report for rule '" + report.rule.rule_id +
                          "' does not match the stored pending rule");
    }

    stored->state = RuleState::Active;
    return accept();
}

std::optional<RuleBody>
SessionRuleEngine::effectiveRule(std::string_view rule_id) const {
    std::lock_guard lock(mutex_);
    const StoredRule* stored = findLocked(rule_id);
    if (stored == nullptr || stored->state != RuleState::Active) {
        return std::nullopt;
    }
    return stored->body;
}

std::optional<RuleState>
SessionRuleEngine::ruleState(std::string_view rule_id) const {
    std::lock_guard lock(mutex_);
    const StoredRule* stored = findLocked(rule_id);
    if (stored == nullptr) {
        return std::nullopt;
    }
    return stored->state;
}

std::vector<std::string> SessionRuleEngine::listRuleIds() const {
    std::lock_guard lock(mutex_);
    std::vector<std::string> ids;
    ids.reserve(rules_.size());
    for (const auto& [rule_id, stored] : rules_) {
        ids.push_back(rule_id);
    }
    return ids;
}

std::optional<std::uint64_t>
SessionRuleEngine::lastSequence(std::string_view node_id) const {
    std::lock_guard lock(mutex_);
    const auto it = last_sequence_.find(std::string(node_id));
    if (it == last_sequence_.end()) {
        return std::nullopt;
    }
    return it->second;
}

std::string toHex(std::span<const std::byte> data) {
    static constexpr char kHex[] = "0123456789abcdef";
    std::string out;
    out.reserve(data.size() * 2U);
    for (const std::byte byte : data) {
        const auto value = static_cast<std::uint8_t>(byte);
        out.push_back(kHex[value >> 4U]);
        out.push_back(kHex[value & 0x0FU]);
    }
    return out;
}

const char* toString(RejectReason reason) noexcept {
    switch (reason) {
    case RejectReason::Ok: return "Ok";
    case RejectReason::UnauthorizedNode: return "UnauthorizedNode";
    case RejectReason::BadMac: return "BadMac";
    case RejectReason::SessionMismatch: return "SessionMismatch";
    case RejectReason::UserMismatch: return "UserMismatch";
    case RejectReason::StaleSequence: return "StaleSequence";
    case RejectReason::DuplicateSequence: return "DuplicateSequence";
    case RejectReason::UnknownRule: return "UnknownRule";
    case RejectReason::ReadbackMismatch: return "ReadbackMismatch";
    case RejectReason::AlreadyActive: return "AlreadyActive";
    }
    return "Unknown";
}

const char* toString(RuleState state) noexcept {
    switch (state) {
    case RuleState::Pending: return "Pending";
    case RuleState::Active: return "Active";
    }
    return "Unknown";
}

const char* toString(ChargingAction action) noexcept {
    switch (action) {
    case ChargingAction::Metered: return "Metered";
    case ChargingAction::Free: return "Free";
    }
    return "Unknown";
}

const char* toString(ForwardingAction action) noexcept {
    switch (action) {
    case ForwardingAction::Forward: return "Forward";
    case ForwardingAction::Redirect: return "Redirect";
    case ForwardingAction::Drop: return "Drop";
    }
    return "Unknown";
}

} // namespace netsec::session_rule
