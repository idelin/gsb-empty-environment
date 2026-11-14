#include "netsec.hpp"

#include <random>

namespace netsec {

std::string SessionId::hex() const {
    static constexpr char digits[] = "0123456789abcdef";
    std::string out;
    out.reserve(kSize * 2);
    for (std::uint8_t byte : bytes_) {
        out.push_back(digits[byte >> 4]);
        out.push_back(digits[byte & 0x0F]);
    }
    return out;
}

std::size_t SessionId::Hash::operator()(const SessionId& id) const noexcept {
    std::size_t h = 0;
    for (std::size_t i = 0; i < kSize; ++i) {
        h ^= static_cast<std::size_t>(id.bytes_[i]) + 0x9e3779b9u + (h << 6) + (h >> 2);
    }
    return h;
}

SessionRouter::SessionRouter(std::chrono::milliseconds session_ttl) : ttl_(session_ttl) {}

SessionId SessionRouter::generate_id() {
    SessionId id;
    std::random_device rd;
    for (std::size_t i = 0; i < SessionId::kSize; i += sizeof(unsigned int)) {
        unsigned int value = rd();
        for (std::size_t j = 0; j < sizeof(unsigned int) && i + j < SessionId::kSize; ++j) {
            id.bytes_[i + j] = static_cast<std::uint8_t>((value >> (j * 8)) & 0xFF);
        }
    }
    return id;
}

SessionId SessionRouter::open_session(SessionBinding binding, TimePoint now) {
    SessionId id;
    do {
        id = generate_id();
    } while (sessions_.contains(id)); // 标识全局唯一，绝不复用
    sessions_.emplace(id, Session{std::move(binding), now, State::Active, 0});
    return id;
}

std::optional<SessionBinding> SessionRouter::binding_of(const SessionId& id) const {
    auto it = sessions_.find(id);
    if (it == sessions_.end() || it->second.state != State::Active) {
        return std::nullopt;
    }
    return it->second.binding;
}

std::variant<RoutedPage, RouteError> SessionRouter::route_page(const SessionId& id,
                                                               std::string_view callee,
                                                               std::uint64_t sequence,
                                                               std::vector<std::uint8_t> page_payload,
                                                               TimePoint now) {
    auto it = sessions_.find(id);
    if (it == sessions_.end()) {
        return RouteError::UnknownSession;
    }
    Session& session = it->second;
    if (session.state == State::Consumed) {
        return RouteError::SessionConsumed; // 一次性标识：复用/重放拒绝
    }
    if (now - session.created > ttl_) {
        session.state = State::Consumed; // 超时即作废
        return RouteError::SessionExpired;
    }
    if (callee != session.binding.callee) {
        return RouteError::CalleeMismatch; // 中途改址拒绝：页面只发原被叫
    }
    if (sequence != session.next_sequence) {
        return RouteError::ReplayDetected; // 序号必须单调递增
    }
    ++session.next_sequence;
    return RoutedPage{session.binding.callee, std::move(page_payload), sequence};
}

bool SessionRouter::end_session(const SessionId& id, TimePoint now) {
    auto it = sessions_.find(id);
    if (it == sessions_.end() || it->second.state == State::Consumed) {
        return false;
    }
    (void)now;
    it->second.state = State::Consumed;
    return true;
}

std::size_t SessionRouter::active_sessions() const {
    std::size_t count = 0;
    for (const auto& [id, session] : sessions_) {
        if (session.state == State::Active) {
            ++count;
        }
    }
    return count;
}

} // namespace netsec
