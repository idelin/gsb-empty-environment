#include "freshness_validator.hpp"

#include <limits>

namespace netsec {

namespace {
constexpr std::uint64_t kLowModulus = 256ULL;
} // namespace

FreshnessValidator::FreshnessValidator(std::uint64_t initial_counter,
                                       std::uint64_t forward_window,
                                       unsigned resync_threshold)
    : counter_(initial_counter),
      forward_window_(forward_window),
      fail_count_(0),
      resync_threshold_(resync_threshold),
      resync_active_(false),
      last_reason_(RejectReason::None) {}

ReconstructResult FreshnessValidator::reconstruct(std::uint8_t low_byte) const {
    if (resync_active_) {
        last_reason_ = RejectReason::ResyncActive;
        return {std::nullopt, RejectReason::ResyncActive};
    }

    // 求最小正增量 delta，使 (L + delta) 的低 8 位等于 low_byte。
    // delta 落在 [1, 256]：低位相等时取下一圈的 256，保证 V 严格大于 L。
    std::int64_t delta = static_cast<std::int64_t>(low_byte) -
                         static_cast<std::int64_t>(counter_ & kLowMask);
    if (delta <= 0) {
        delta += static_cast<std::int64_t>(kLowModulus);
    }

    const std::uint64_t unsigned_delta = static_cast<std::uint64_t>(delta);

    // 64 位回绕溢出：先于窗口判定，计数器空间耗尽时不得静默回绕。
    if (unsigned_delta >
        std::numeric_limits<std::uint64_t>::max() - counter_) {
        last_reason_ = RejectReason::CounterOverflow;
        return {std::nullopt, RejectReason::CounterOverflow};
    }

    // 前向窗口：防止攻击者把接收方计数器推向未来。
    if (unsigned_delta > forward_window_) {
        last_reason_ = RejectReason::OutOfWindow;
        return {std::nullopt, RejectReason::OutOfWindow};
    }

    const std::uint64_t candidate = counter_ + unsigned_delta;
    last_reason_ = RejectReason::None;
    return {candidate, RejectReason::None};
}

bool FreshnessValidator::commit(std::uint64_t candidate) {
    if (resync_active_) {
        last_reason_ = RejectReason::ResyncActive;
        return false;
    }
    if (candidate <= counter_) {
        last_reason_ = RejectReason::StaleCandidate;
        return false;
    }

    const std::uint64_t delta = candidate - counter_;
    if (delta > forward_window_) {
        last_reason_ = RejectReason::OutOfWindow;
        return false;
    }

    // 只有 MAC 验证通过的调用方才会走到这里：
    // 低位回绕到 0 产生的高位进位随本次赋值一起生效。
    counter_ = candidate;
    fail_count_ = 0;
    last_reason_ = RejectReason::None;
    return true;
}

void FreshnessValidator::notifyMacFailure() {
    if (resync_active_) {
        return;
    }
    ++fail_count_;
    if (fail_count_ >= resync_threshold_) {
        resync_active_ = true;
    }
}

void FreshnessValidator::applyTrustedSync(std::uint64_t trusted_counter) {
    counter_ = trusted_counter;
    fail_count_ = 0;
    resync_active_ = false;
    last_reason_ = RejectReason::None;
}

std::uint64_t FreshnessValidator::counter() const noexcept {
    return counter_;
}

std::uint64_t FreshnessValidator::forwardWindow() const noexcept {
    return forward_window_;
}

unsigned FreshnessValidator::consecutiveFailures() const noexcept {
    return fail_count_;
}

unsigned FreshnessValidator::resyncThreshold() const noexcept {
    return resync_threshold_;
}

bool FreshnessValidator::resyncActive() const noexcept {
    return resync_active_;
}

RejectReason FreshnessValidator::lastReason() const noexcept {
    return last_reason_;
}

} // namespace netsec
