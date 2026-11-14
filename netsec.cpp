#include "netsec.hpp"

#include <array>
#include <cerrno>
#include <mutex>
#include <utility>
#include <vector>

#if defined(__unix__) || defined(__APPLE__)
#include <fcntl.h>
#include <unistd.h>
#elif defined(_WIN32)
#include <windows.h>
#include <bcrypt.h>
#else
#include <random>
#endif

namespace netsec {
namespace {

constexpr std::string_view kBase64UrlAlphabet =
    "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_";

std::string base64UrlEncode(
    const std::array<std::uint8_t, kSessionIdBytes>& in) {
    std::string out;
    out.resize(kSessionIdEncodedLen);
    std::size_t bitBuffer = 0;
    int bitCount = 0;
    std::size_t pos = 0;
    for (std::uint8_t byte : in) {
        bitBuffer = (bitBuffer << 8) | byte;
        bitCount += 8;
        while (bitCount >= 6) {
            bitCount -= 6;
            out[pos++] =
                kBase64UrlAlphabet[(bitBuffer >> bitCount) & 0x3F];
        }
    }
    if (bitCount > 0) {
        out[pos++] =
            kBase64UrlAlphabet[(bitBuffer << (6 - bitCount)) & 0x3F];
    }
    return out;
}

// CSPRNG bytes taken straight from the operating system. std::random_device is
// avoided because its security is implementation-defined.
bool secureRandomBytes(
    std::array<std::uint8_t, kSessionIdBytes>& bytes) {
#if defined(__unix__) || defined(__APPLE__)
    int fd = ::open("/dev/urandom", O_RDONLY | O_CLOEXEC);
    if (fd < 0) {
        return false;
    }
    std::size_t filled = 0;
    while (filled < bytes.size()) {
        ssize_t n =
            ::read(fd, bytes.data() + filled, bytes.size() - filled);
        if (n < 0) {
            if (errno == EINTR) {
                continue;
            }
            ::close(fd);
            return false;
        }
        if (n == 0) {
            ::close(fd);
            return false;
        }
        filled += static_cast<std::size_t>(n);
    }
    ::close(fd);
    return true;
#elif defined(_WIN32)
    return ::BCryptGenRandom(nullptr, bytes.data(),
                             static_cast<ULONG>(bytes.size()),
                             BCRYPT_USE_SYSTEM_PREFERRED_RNG) == 0;
#else
    std::random_device rd;
    if (rd.entropy() <= 0.0) {
        return false;
    }
    for (auto& b : bytes) {
        b = static_cast<std::uint8_t>(rd());
    }
    return true;
#endif
}

std::string trimToken(std::string_view value) {
    std::size_t begin = 0;
    std::size_t end = value.size();
    while (begin < end &&
           (value[begin] == ' ' || value[begin] == '\t')) {
        ++begin;
    }
    while (end > begin &&
           (value[end - 1] == ' ' || value[end - 1] == '\t')) {
        --end;
    }
    return std::string(value.substr(begin, end - begin));
}

std::uint16_t parseU16(std::string_view text) {
    unsigned value = 0;
    for (char c : text) {
        if (c < '0' || c > '9') {
            return 0;
        }
        value = value * 10U + static_cast<unsigned>(c - '0');
        if (value > 0xFFFFU) {
            return 0;
        }
    }
    return static_cast<std::uint16_t>(value);
}

// "5000-5001" -> {5000, 5001}; "5000" -> {5000, 0}.
std::pair<std::uint16_t, std::uint16_t> parsePortPair(
    std::string_view text) {
    auto dash = text.find('-');
    if (dash == std::string_view::npos) {
        return {parseU16(text), 0};
    }
    return {parseU16(text.substr(0, dash)),
            parseU16(text.substr(dash + 1))};
}

std::uint8_t parseU8(std::string_view text) {
    unsigned value = 0;
    for (char c : text) {
        if (c < '0' || c > '9') {
            return 0;
        }
        value = value * 10U + static_cast<unsigned>(c - '0');
        if (value > 255U) {
            return 0;
        }
    }
    return static_cast<std::uint8_t>(value);
}

std::uint16_t parseHexGroup(std::string_view text) {
    unsigned value = 0;
    for (char c : text) {
        unsigned digit = 0;
        if (c >= '0' && c <= '9') {
            digit = static_cast<unsigned>(c - '0');
        } else if (c >= 'a' && c <= 'f') {
            digit = static_cast<unsigned>(c - 'a' + 10);
        } else if (c >= 'A' && c <= 'F') {
            digit = static_cast<unsigned>(c - 'A' + 10);
        } else {
            return 0;
        }
        value = value * 16U + digit;
        if (value > 0xFFFFU) {
            return 0;
        }
    }
    return static_cast<std::uint16_t>(value);
}

// Expands an IPv6 literal (including "::") into its eight 16-bit segments.
// Returns an empty vector when the input is not a well-formed IPv6 address.
std::vector<std::uint16_t> expandIpv6(std::string_view ip) {
    std::vector<std::uint16_t> head;
    std::vector<std::uint16_t> tail;
    auto doubleColon = ip.find("::");

    auto parseGroups = [](std::string_view part,
                          std::vector<std::uint16_t>& out) {
        if (part.empty()) {
            return true;
        }
        std::size_t start = 0;
        while (true) {
            auto colon = part.find(':', start);
            auto group = part.substr(
                start, colon == std::string_view::npos
                           ? std::string_view::npos
                           : colon - start);
            if (group.empty()) {
                return false;
            }
            out.push_back(parseHexGroup(group));
            if (colon == std::string_view::npos) {
                break;
            }
            start = colon + 1;
        }
        return true;
    };

    if (doubleColon != std::string_view::npos) {
        if (ip.find("::", doubleColon + 1) != std::string_view::npos) {
            return {};
        }
        if (!parseGroups(ip.substr(0, doubleColon), head) ||
            !parseGroups(ip.substr(doubleColon + 2), tail)) {
            return {};
        }
        if (head.size() + tail.size() > 7) {
            return {};
        }
    } else {
        if (!parseGroups(ip, head) || head.size() != 8) {
            return {};
        }
        return head;
    }

    std::vector<std::uint16_t> result = std::move(head);
    while (result.size() + tail.size() < 8) {
        result.push_back(0);
    }
    for (std::uint16_t segment : tail) {
        result.push_back(segment);
    }
    return result;
}

// /24 match for IPv4, /64 match for IPv6. Mixed families never match.
bool sameNetwork(const std::string& a, const std::string& b) {
    bool aV6 = a.find(':') != std::string::npos;
    bool bV6 = b.find(':') != std::string::npos;
    if (aV6 != bV6) {
        return false;
    }
    if (aV6) {
        auto pa = expandIpv6(a);
        auto pb = expandIpv6(b);
        if (pa.size() != 8 || pb.size() != 8) {
            return false;
        }
        for (std::size_t i = 0; i < 4; ++i) {
            if (pa[i] != pb[i]) {
                return false;
            }
        }
        return true;
    }

    std::vector<std::uint16_t> pa;
    std::vector<std::uint16_t> pb;
    auto splitV4 = [](std::string_view ip,
                      std::vector<std::uint16_t>& out) {
        std::size_t start = 0;
        while (true) {
            auto dot = ip.find('.', start);
            auto octet = ip.substr(
                start, dot == std::string_view::npos
                             ? std::string_view::npos
                             : dot - start);
            if (octet.empty()) {
                return false;
            }
            std::uint16_t value = parseU8(octet);
            if (octet != "0" && value == 0) {
                return false;
            }
            out.push_back(value);
            if (dot == std::string_view::npos) {
                break;
            }
            start = dot + 1;
        }
        return out.size() == 4;
    };
    if (!splitV4(a, pa) || !splitV4(b, pb)) {
        return false;
    }
    return pa[0] == pb[0] && pa[1] == pb[1] && pa[2] == pb[2];
}

} // namespace

std::optional<TransportParams> parseTransportHeader(
    std::string_view header) {
    TransportParams params;

    auto semi = header.find(';');
    std::string profile = trimToken(header.substr(0, semi));
    // The profile token may carry an upper layer, e.g. "RTP/AVP/TCP".
    if (profile.rfind("RTP/AVP", 0) != 0) {
        return std::nullopt;
    }
    if (profile == "RTP/AVP/TCP") {
        params.mode = TransportMode::UnicastTcp;
    } else if (profile == "RTP/AVP") {
        params.mode = TransportMode::UnicastUdp;
    } else {
        params.mode = TransportMode::Other;
    }

    std::size_t cursor =
        (semi == std::string_view::npos) ? header.size() : semi + 1;
    while (cursor < header.size()) {
        auto next = header.find(';', cursor);
        std::string token = trimToken(header.substr(
            cursor, next == std::string_view::npos
                         ? std::string_view::npos
                         : next - cursor));
        cursor = (next == std::string_view::npos) ? header.size()
                                                   : next + 1;
        if (token.empty()) {
            continue;
        }
        auto eq = token.find('=');
        std::string key = (eq == std::string::npos)
                              ? token
                              : std::string(token.substr(0, eq));
        std::string_view value =
            (eq == std::string::npos)
                ? std::string_view{}
                : std::string_view{token}.substr(eq + 1);
        if (key == "unicast") {
            params.unicast = true;
        } else if (key == "multicast") {
            params.unicast = false;
        } else if (key == "client_port") {
            auto [portA, portB] = parsePortPair(value);
            params.clientPortA = portA;
            params.clientPortB = portB;
            params.hasClientPort = portA != 0;
        } else if (key == "interleaved") {
            auto [chA, chB] = parsePortPair(value);
            params.interleavedA = static_cast<std::uint8_t>(chA);
            params.interleavedB = static_cast<std::uint8_t>(chB);
            params.hasInterleaved = true;
        } else if (key == "destination" || key == "client_ip") {
            params.mediaIp = trimToken(value);
        } else if (key == "ttl") {
            params.ttl = parseU8(value);
        }
    }
    return params;
}

SessionManager::SessionManager(NatPolicy policy) : policy_(policy) {}

bool SessionManager::generateRandomId(std::string& out) {
    std::array<std::uint8_t, kSessionIdBytes> bytes{};
    if (!secureRandomBytes(bytes)) {
        return false;
    }
    out = base64UrlEncode(bytes);
    return true;
}

bool SessionManager::authorizeSourceLocked(
    const RequestSource& bound, const RequestSource& current) const {
    if (policy_ == NatPolicy::RejectChange) {
        // Exact 5-tuple source: neither the IP nor the port may change.
        return bound.peerIp == current.peerIp &&
               bound.peerPort == current.peerPort;
    }
    if (bound.peerIp == current.peerIp) {
        return true;
    }
    switch (policy_) {
    case NatPolicy::RejectChange:
        return false; // handled above; kept for exhaustive enumeration
    case NatPolicy::SameIpAllowPortChange:
        // The port is free to change, but a different source IP is rejected.
        return false;
    case NatPolicy::SameNetworkAllowRebind:
        return sameNetwork(bound.peerIp, current.peerIp);
    case NatPolicy::AuthenticatedRebind:
        return current.authenticated && bound.authenticated &&
               !bound.principal.empty() &&
               bound.principal == current.principal;
    }
    return false;
}

VerifyResult SessionManager::setup(const RequestSource& source,
                                   const TransportParams& transport,
                                   std::string& issuedSessionIdOut) {
    if (source.peerIp.empty()) {
        return {false, RejectReason::InvalidArgument, 0};
    }

    std::unique_lock lock(mutex_);
    std::string id;
    // Collisions are astronomically unlikely; keep retrying until an id is
    // produced that has never been issued. Tombstoned ids never come back.
    do {
        if (!generateRandomId(id)) {
            return {false, RejectReason::InvalidArgument, 0};
        }
    } while (tombstones_.find(id) != tombstones_.end());

    Session session;
    session.info.id = id;
    session.info.source = source;
    session.info.transport = transport;
    session.info.state = SessionState::Init;
    session.info.generation = 1;

    tombstones_.emplace(id, nullptr);
    issuedSessionIdOut = id;
    sessions_.emplace(id, std::move(session));
    return {true, RejectReason::None, 1};
}

VerifyResult SessionManager::setup(const RequestSource& source,
                                   std::string_view transportHeader,
                                   std::string& issuedSessionIdOut) {
    auto parsed = parseTransportHeader(transportHeader);
    if (!parsed) {
        return {false, RejectReason::InvalidTransportHeader, 0};
    }
    return setup(source, *parsed, issuedSessionIdOut);
}

VerifyResult SessionManager::play(std::string_view sessionId,
                                  const RequestSource& source,
                                  const TransportParams& transport) {
    std::unique_lock lock(mutex_);
    auto it = sessions_.find(sessionId);
    if (it == sessions_.end()) {
        // The id was either never issued or already torn down. A copied id
        // therefore cannot resurrect or hijack a session.
        RejectReason reason =
            tombstones_.find(sessionId) == tombstones_.end()
                ? RejectReason::UnknownSession
                : RejectReason::OneTimeSessionReused;
        return {false, reason, 0};
    }

    Session& session = it->second;
    if (!authorizeSourceLocked(session.info.source, source)) {
        return {false, RejectReason::SourceAddressMismatch,
                session.info.generation};
    }
    // An authenticated binding keeps requiring the same principal. Anonymous
    // sessions cannot be upgraded by later credentials either.
    if (session.info.source.authenticated != source.authenticated ||
        session.info.source.principal != source.principal) {
        return {false, RejectReason::PrincipalMismatch,
                session.info.generation};
    }
    // Every transport field must match the SETUP binding. Replacing the
    // destination/ports on PLAY is rejected, so media never redirects.
    if (session.info.transport != transport) {
        return {false, RejectReason::TransportMismatch,
                session.info.generation};
    }

    session.info.state = SessionState::Playing;
    session.info.generation += 1;
    return {true, RejectReason::None, session.info.generation};
}

VerifyResult SessionManager::play(std::string_view sessionId,
                                  const RequestSource& source,
                                  std::string_view transportHeader) {
    auto parsed = parseTransportHeader(transportHeader);
    if (!parsed) {
        return {false, RejectReason::InvalidTransportHeader, 0};
    }
    return play(sessionId, source, *parsed);
}

VerifyResult SessionManager::pause(std::string_view sessionId,
                                   const RequestSource& source) {
    std::unique_lock lock(mutex_);
    auto it = sessions_.find(sessionId);
    if (it == sessions_.end()) {
        return {false, RejectReason::UnknownSession, 0};
    }
    Session& session = it->second;
    if (!authorizeSourceLocked(session.info.source, source)) {
        return {false, RejectReason::SourceAddressMismatch,
                session.info.generation};
    }
    if (session.info.source.authenticated != source.authenticated ||
        session.info.source.principal != source.principal) {
        return {false, RejectReason::PrincipalMismatch,
                session.info.generation};
    }
    if (session.info.state != SessionState::Playing) {
        return {false, RejectReason::InvalidState, session.info.generation};
    }
    session.info.state = SessionState::Ready;
    session.info.generation += 1;
    return {true, RejectReason::None, session.info.generation};
}

VerifyResult SessionManager::teardown(std::string_view sessionId,
                                      const RequestSource& source) {
    std::unique_lock lock(mutex_);
    auto it = sessions_.find(sessionId);
    if (it == sessions_.end()) {
        RejectReason reason =
            tombstones_.find(sessionId) == tombstones_.end()
                ? RejectReason::UnknownSession
                : RejectReason::OneTimeSessionReused;
        return {false, reason, 0};
    }
    Session& session = it->second;
    if (!authorizeSourceLocked(session.info.source, source)) {
        return {false, RejectReason::SourceAddressMismatch,
                session.info.generation};
    }
    if (session.info.source.authenticated != source.authenticated ||
        session.info.source.principal != source.principal) {
        return {false, RejectReason::PrincipalMismatch,
                session.info.generation};
    }
    std::uint64_t generation = session.info.generation;
    sessions_.erase(it);
    // The id stays in tombstones_ for the lifetime of the manager.
    return {true, RejectReason::None, generation};
}

std::optional<MediaTarget> SessionManager::mediaTarget(
    std::string_view sessionId, const RequestSource& source) const {
    std::shared_lock lock(mutex_);
    auto it = sessions_.find(sessionId);
    if (it == sessions_.end()) {
        return std::nullopt;
    }
    const Session& session = it->second;
    if (session.info.state != SessionState::Playing) {
        return std::nullopt;
    }
    if (!authorizeSourceLocked(session.info.source, source) ||
        session.info.source.authenticated != source.authenticated ||
        session.info.source.principal != source.principal) {
        return std::nullopt;
    }
    // Resolve exclusively from the stored binding. An explicit Transport
    // destination wins; otherwise the bound control-connection IP is used.
    const TransportParams& transport = session.info.transport;
    std::string ip = transport.mediaIp.empty()
                         ? session.info.source.peerIp
                         : transport.mediaIp;
    return MediaTarget{std::move(ip), transport.clientPortA,
                       session.info.generation};
}

std::optional<SessionInfo> SessionManager::lookup(
    std::string_view sessionId) const {
    std::shared_lock lock(mutex_);
    auto it = sessions_.find(sessionId);
    if (it == sessions_.end()) {
        return std::nullopt;
    }
    return it->second.info;
}

bool SessionManager::hasSession(std::string_view sessionId) const {
    std::shared_lock lock(mutex_);
    return sessions_.find(sessionId) != sessions_.end();
}

NatPolicy SessionManager::policy() const noexcept {
    std::shared_lock lock(mutex_);
    return policy_;
}

void SessionManager::setPolicy(NatPolicy policy) noexcept {
    std::unique_lock lock(mutex_);
    policy_ = policy;
}

std::size_t SessionManager::activeCount() const {
    std::shared_lock lock(mutex_);
    return sessions_.size();
}

std::string_view explainReject(RejectReason reason) noexcept {
    switch (reason) {
    case RejectReason::None:
        return "ok";
    case RejectReason::UnknownSession:
        return "session id is not known";
    case RejectReason::OneTimeSessionReused:
        return "one-time session id was already torn down";
    case RejectReason::SourceAddressMismatch:
        return "request source address violates the NAT policy";
    case RejectReason::PrincipalMismatch:
        return "authenticated principal does not match the binding";
    case RejectReason::UnauthenticatedRebind:
        return "address rebind requires the bound principal";
    case RejectReason::TransportMismatch:
        return "transport parameters differ from the SETUP binding";
    case RejectReason::InvalidTransportHeader:
        return "Transport header is not a supported RTP/AVP profile";
    case RejectReason::InvalidState:
        return "operation is not valid in the current session state";
    case RejectReason::InvalidArgument:
        return "invalid argument";
    }
    return "unknown rejection";
}

} // namespace netsec
