#include "netsec.hpp"

#include <cstdlib>
#include <iostream>
#include <set>
#include <string>
#include <vector>

namespace {

int failures = 0;

#define CHECK(cond)                                                                                \
    do {                                                                                           \
        if (!(cond)) {                                                                             \
            ++failures;                                                                            \
            std::cerr << "FAIL " << __FILE__ << ":" << __LINE__ << "  " << #cond << '\n';          \
        }                                                                                          \
    } while (0)

using netsec::Clock;
using netsec::FaxGeneration;
using netsec::RouteError;
using netsec::RoutedPage;
using netsec::SessionBinding;
using netsec::SessionRouter;
using netsec::TimePoint;

SessionBinding make_binding(const std::string& caller, const std::string& callee) {
    SessionBinding binding;
    binding.caller = caller;
    binding.callee = callee;
    binding.capabilities.max_baud = 33600;
    binding.capabilities.error_correction = true;
    binding.capabilities.page_size = "A4";
    binding.generation = FaxGeneration::G3;
    return binding;
}

std::vector<std::uint8_t> page(int tag) {
    return {static_cast<std::uint8_t>(tag), 0xDE, 0xAD, 0xBE, 0xEF};
}

bool holds_error(const std::variant<RoutedPage, RouteError>& result, RouteError error) {
    return std::holds_alternative<RouteError>(result) && std::get<RouteError>(result) == error;
}

// 合法传真：页面按序送达，且只发原被叫。
void test_legitimate_fax() {
    SessionRouter router(std::chrono::seconds{30});
    const TimePoint t0 = Clock::now();
    auto id = router.open_session(make_binding("+8610001", "+8620002"), t0);

    auto binding = router.binding_of(id);
    CHECK(binding.has_value());
    CHECK(binding->caller == "+8610001");
    CHECK(binding->callee == "+8620002");
    CHECK(binding->capabilities.max_baud == 33600);
    CHECK(binding->generation == FaxGeneration::G3);

    for (std::uint64_t seq = 0; seq < 3; ++seq) {
        auto result = router.route_page(id, "+8620002", seq, page(static_cast<int>(seq)), t0 + std::chrono::seconds{1});
        CHECK(std::holds_alternative<RoutedPage>(result));
        if (std::holds_alternative<RoutedPage>(result)) {
            const auto& routed = std::get<RoutedPage>(result);
            CHECK(routed.callee == "+8620002");
            CHECK(routed.sequence == seq);
        }
    }
    CHECK(router.end_session(id, t0 + std::chrono::seconds{2}));
    CHECK(router.active_sessions() == 0);
}

// 目标替换：攻击者中途把会话目标改成自己号码，必须拒绝，页面只发原被叫。
void test_target_replacement() {
    SessionRouter router(std::chrono::seconds{30});
    const TimePoint t0 = Clock::now();
    auto id = router.open_session(make_binding("+8610001", "+8620002"), t0);

    auto hijack = router.route_page(id, "+8699999", 0, page(1), t0 + std::chrono::seconds{1});
    CHECK(holds_error(hijack, RouteError::CalleeMismatch));

    // 改址被拒绝后，原会话仍可按绑定被叫正常传页。
    auto legit = router.route_page(id, "+8620002", 0, page(1), t0 + std::chrono::seconds{2});
    CHECK(std::holds_alternative<RoutedPage>(legit));
    if (std::holds_alternative<RoutedPage>(legit)) {
        CHECK(std::get<RoutedPage>(legit).callee == "+8620002");
    }
}

// 会话复用：会话结束后会话标识作废，再次使用必须拒绝。
void test_session_reuse() {
    SessionRouter router(std::chrono::seconds{30});
    const TimePoint t0 = Clock::now();
    auto id = router.open_session(make_binding("+8610001", "+8620002"), t0);

    auto first = router.route_page(id, "+8620002", 0, page(1), t0);
    CHECK(std::holds_alternative<RoutedPage>(first));
    CHECK(router.end_session(id, t0 + std::chrono::seconds{1}));

    auto reused = router.route_page(id, "+8620002", 1, page(2), t0 + std::chrono::seconds{2});
    CHECK(holds_error(reused, RouteError::SessionConsumed));
    CHECK(!router.end_session(id, t0 + std::chrono::seconds{3}));
    CHECK(!router.binding_of(id).has_value());
}

// 重放：重复发送已传页面（旧序号）必须拒绝。
void test_replay() {
    SessionRouter router(std::chrono::seconds{30});
    const TimePoint t0 = Clock::now();
    auto id = router.open_session(make_binding("+8610001", "+8620002"), t0);

    auto first = router.route_page(id, "+8620002", 0, page(1), t0);
    CHECK(std::holds_alternative<RoutedPage>(first));

    auto replayed = router.route_page(id, "+8620002", 0, page(1), t0 + std::chrono::seconds{1});
    CHECK(holds_error(replayed, RouteError::ReplayDetected));

    auto skipped = router.route_page(id, "+8620002", 5, page(6), t0 + std::chrono::seconds{1});
    CHECK(holds_error(skipped, RouteError::ReplayDetected));

    auto next = router.route_page(id, "+8620002", 1, page(2), t0 + std::chrono::seconds{2});
    CHECK(std::holds_alternative<RoutedPage>(next));
}

// 超时：超过会话生存期后传页必须拒绝，且会话作废不可恢复。
void test_timeout() {
    SessionRouter router(std::chrono::seconds{30});
    const TimePoint t0 = Clock::now();
    auto id = router.open_session(make_binding("+8610001", "+8620002"), t0);

    auto late = router.route_page(id, "+8620002", 0, page(1), t0 + std::chrono::seconds{31});
    CHECK(holds_error(late, RouteError::SessionExpired));

    auto after_expiry = router.route_page(id, "+8620002", 0, page(1), t0 + std::chrono::seconds{32});
    CHECK(holds_error(after_expiry, RouteError::SessionConsumed));
    CHECK(!router.binding_of(id).has_value());
}

// 会话标识：不可预测（足够长随机）且全局唯一。
void test_session_id_uniqueness() {
    SessionRouter router;
    std::set<std::string> ids;
    for (int i = 0; i < 1000; ++i) {
        auto id = router.open_session(make_binding("+8610001", "+8620002"));
        CHECK(id.hex().size() == 64); // 256 位
        ids.insert(id.hex());
    }
    CHECK(ids.size() == 1000);
}

} // namespace

int main() {
    test_legitimate_fax();
    test_target_replacement();
    test_session_reuse();
    test_replay();
    test_timeout();
    test_session_id_uniqueness();

    if (failures == 0) {
        std::cout << "all session router tests passed\n";
        return EXIT_SUCCESS;
    }
    std::cerr << failures << " check(s) failed\n";
    return EXIT_FAILURE;
}
