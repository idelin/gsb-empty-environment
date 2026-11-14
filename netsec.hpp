#pragma once

#include <array>
#include <chrono>
#include <cstddef>
#include <cstdint>
#include <optional>
#include <string>
#include <string_view>
#include <unordered_map>
#include <variant>
#include <vector>

namespace netsec {

using Clock = std::chrono::steady_clock;
using TimePoint = Clock::time_point;

// 能力协商结果：会话建立时锁定，传输期间不可变更。
struct FaxCapabilities {
    std::uint32_t max_baud{14400};
    bool error_correction{true};
    std::string page_size{"A4"};
    friend bool operator==(const FaxCapabilities&, const FaxCapabilities&) = default;
};

// 传真代次（G3/G4）。
enum class FaxGeneration : std::uint8_t { G3 = 3, G4 = 4 };

// 会话绑定：主叫、被叫、能力协商与代次，建立后不可变。
struct SessionBinding {
    std::string caller;
    std::string callee;
    FaxCapabilities capabilities;
    FaxGeneration generation{FaxGeneration::G3};
};

// 会话标识：256 位加密随机值，不可预测，一次性使用。
class SessionId {
public:
    static constexpr std::size_t kSize = 32;

    std::string hex() const;
    friend bool operator==(const SessionId&, const SessionId&) = default;

private:
    friend class SessionRouter;
    struct Hash {
        std::size_t operator()(const SessionId& id) const noexcept;
    };
    std::array<std::uint8_t, kSize> bytes_{};
};

enum class RouteError {
    UnknownSession,    // 会话标识不存在
    SessionConsumed,   // 会话标识已使用（一次性，拒绝复用/重放）
    SessionExpired,    // 会话超时
    CalleeMismatch,    // 目标与绑定被叫不一致（中途改址）
    ReplayDetected,    // 页面序号回退（重放）
};

struct RoutedPage {
    std::string callee;              // 复核后的原被叫
    std::vector<std::uint8_t> payload;
    std::uint64_t sequence;
};

// 传真网关会话路由组件：按会话标识转发页面，传输前复核目标。
class SessionRouter {
public:
    explicit SessionRouter(std::chrono::milliseconds session_ttl = std::chrono::seconds{30});

    // 建立会话：绑定主叫/被叫/能力/代次，返回不可预测的一次性会话标识。
    SessionId open_session(SessionBinding binding, TimePoint now = Clock::now());

    // 查询会话绑定（用于网关审计/显示）。
    std::optional<SessionBinding> binding_of(const SessionId& id) const;

    // 页面路由：复核会话有效、未超时、目标与绑定一致、序号无重放后才放行。
    // sequence 必须从 0 开始逐页递增。
    std::variant<RoutedPage, RouteError> route_page(const SessionId& id,
                                                    std::string_view callee,
                                                    std::uint64_t sequence,
                                                    std::vector<std::uint8_t> page_payload,
                                                    TimePoint now = Clock::now());

    // 结束会话：会话标识随即作废（一次性），此后任何使用均被拒绝。
    bool end_session(const SessionId& id, TimePoint now = Clock::now());

    std::size_t active_sessions() const;

private:
    enum class State : std::uint8_t { Active, Consumed };

    struct Session {
        SessionBinding binding;
        TimePoint created;
        State state{State::Active};
        std::uint64_t next_sequence{0};
    };

    static SessionId generate_id();

    std::chrono::milliseconds ttl_;
    std::unordered_map<SessionId, Session, SessionId::Hash> sessions_;
};

} // namespace netsec
