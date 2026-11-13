#pragma once

#include <cstdint>
#include <optional>

namespace netsec {

// 普通报文被拒绝的原因，供 PDU 层统计与诊断。
enum class RejectReason {
    None = 0,
    ResyncActive,     // 处于重同步状态，普通报文一律拒绝
    OutOfWindow,      // 候选值与本地计数器之差超过前向窗口
    CounterOverflow,  // 候选值超过 64 位计数器范围
    StaleCandidate,   // 提交的候选值不大于本地计数器（重放/误用）
};

struct ReconstructResult {
    std::optional<std::uint64_t> candidate;
    RejectReason reason = RejectReason::None;

    constexpr explicit operator bool() const noexcept {
        return candidate.has_value();
    }
};

// SecOC 新鲜度值低位重建器。
//
// 约定：
//  - 本地维护 64 位单调计数器 L，报文只携带低 8 位。
//  - 候选值 V 取大于 L、且低 8 位等于报文值的最小整数。
//  - V - L 超过前向窗口则拒绝；重建本身不修改任何状态。
//  - 只有 MAC 验证通过后调用 commit() 才推进 L（含回绕进位）。
//  - 连续 resync_threshold 次 MAC 失败进入重同步状态，此后
//    reconstruct()/processNormalPdu() 拒绝一切普通报文，
//    仅 applyTrustedSync() 可装载受信计数器并退出该状态。
//
// 本类不含内部锁，假定每个实例只在单一 PDU 接收上下文中使用。
class FreshnessValidator {
public:
    static constexpr unsigned kDefaultResyncThreshold = 3;
    static constexpr std::uint64_t kLowMask = 0xFFULL;

    FreshnessValidator(std::uint64_t initial_counter = 0,
                       std::uint64_t forward_window = 128,
                       unsigned resync_threshold = kDefaultResyncThreshold);

    // 仅根据报文低 8 位重建候选完整值，不修改 L 与失败计数。
    ReconstructResult reconstruct(std::uint8_t low_byte) const;

    // MAC 验证通过后提交候选值，推进 L 并清零连续失败计数。
    // 重同步中、候选值过期或超出窗口时返回 false 且不改变状态。
    bool commit(std::uint64_t candidate);

    // 记录一次 MAC 验证失败；L 不变，达到阈值后进入重同步状态。
    void notifyMacFailure();

    // 受信同步消息入口：直接装载完整计数器，退出重同步并清零失败计数。
    void applyTrustedSync(std::uint64_t trusted_counter);

    // 普通 PDU 一步处理：重建 -> 窗口校验 -> 外部 MAC 校验 -> 提交/计失败。
    // verify_mac 接收重建出的完整新鲜度值，返回 MAC 是否通过。
    // 重建或窗口被拒时不会调用 verify_mac，也不会累计 MAC 失败。
    template <typename MacVerifier>
    bool processNormalPdu(std::uint8_t low_byte, MacVerifier&& verify_mac) {
        ReconstructResult result = reconstruct(low_byte);
        if (!result) {
            return false;
        }
        const std::uint64_t candidate = *result.candidate;
        if (verify_mac(candidate)) {
            return commit(candidate);
        }
        notifyMacFailure();
        return false;
    }

    std::uint64_t counter() const noexcept;
    std::uint64_t forwardWindow() const noexcept;
    unsigned consecutiveFailures() const noexcept;
    unsigned resyncThreshold() const noexcept;
    bool resyncActive() const noexcept;
    RejectReason lastReason() const noexcept;

private:
    std::uint64_t counter_;
    std::uint64_t forward_window_;
    unsigned fail_count_;
    unsigned resync_threshold_;
    bool resync_active_;
    // reconstruct() 逻辑上为 const，诊断原因作为可变附属状态记录。
    mutable RejectReason last_reason_;
};

} // namespace netsec
