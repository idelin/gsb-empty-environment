#include "netsec.hpp"

namespace netsec {

void TaggedMemory::set_tag(std::uint64_t addr, std::uint8_t tag) {
    tags_[addr] = tag;
}

std::uint8_t TaggedMemory::tag_of(std::uint64_t addr) const {
    auto it = tags_.find(addr);
    return it == tags_.end() ? 0 : it->second;
}

MtePolicy::MtePolicy(MteMode mode, const TaggedMemory& mem)
    : mode_(mode), mem_(mem) {}

bool MtePolicy::terminated() const {
    std::lock_guard<std::mutex> lock(mu_);
    return terminated_;
}

std::vector<FaultReport> MtePolicy::reports() const {
    std::lock_guard<std::mutex> lock(mu_);
    return reports_;
}

MtePolicy::ThreadState& MtePolicy::current_thread() {
    return threads_[std::this_thread::get_id()];
}

const MtePolicy::ThreadState* MtePolicy::find_thread(std::thread::id id) const {
    auto it = threads_.find(id);
    return it == threads_.end() ? nullptr : &it->second;
}

void MtePolicy::report_and_terminate(const FaultReport& report) {
    reports_.push_back(report);
    terminated_ = true;
}

bool MtePolicy::check_access(UserPtr ptr, std::size_t len, AccessType access) {
    std::lock_guard<std::mutex> lock(mu_);
    if (terminated_)
        return false;

    ThreadState& state = current_thread();

    // TCO 区段内禁止访问用户内存。
    if (state.tco_depth > 0) {
        report_and_terminate({FaultKind::TcoViolation, access, ptr.addr,
                              std::this_thread::get_id(), false});
        return false;
    }

    bool mismatch = false;
    for (std::size_t i = 0; i < len; ++i) {
        if (mem_.tag_of(ptr.addr + i) != ptr.tag) {
            mismatch = true;
            break;
        }
    }
    if (!mismatch)
        return true;

    // 非对称模式：读同步、写异步。
    const bool synchronous =
        mode_ == MteMode::Sync ||
        (mode_ == MteMode::Asymm && access == AccessType::Read);

    FaultReport report{FaultKind::TagMismatch, access, ptr.addr,
                       std::this_thread::get_id(), !synchronous};
    if (synchronous) {
        report_and_terminate(report);
        return false;
    }
    // 异步：只记录待决状态，拷贝继续，门禁点补报。
    state.pending = true;
    state.pending_report = report;
    return true;
}

bool MtePolicy::copy_from_user(UserPtr ptr, std::size_t len) {
    return check_access(ptr, len, AccessType::Read);
}

bool MtePolicy::copy_to_user(UserPtr ptr, std::size_t len) {
    return check_access(ptr, len, AccessType::Write);
}

MtePolicy::TcoGuard::TcoGuard(MtePolicy& policy) : policy_(policy) {
    std::lock_guard<std::mutex> lock(policy_.mu_);
    ++policy_.current_thread().tco_depth;
}

MtePolicy::TcoGuard::~TcoGuard() {
    std::lock_guard<std::mutex> lock(policy_.mu_);
    --policy_.current_thread().tco_depth;
}

MtePolicy::TcoGuard MtePolicy::tco_enter() { return TcoGuard(*this); }

bool MtePolicy::tco_active() const {
    std::lock_guard<std::mutex> lock(mu_);
    const ThreadState* state = find_thread(std::this_thread::get_id());
    return state != nullptr && state->tco_depth > 0;
}

void MtePolicy::drain_pending() {
    std::lock_guard<std::mutex> lock(mu_);
    if (terminated_)
        return;
    ThreadState& state = current_thread();
    if (!state.pending)
        return;
    // 先清除待决状态再上报，保证上报后无残留。
    FaultReport report = state.pending_report;
    state.pending = false;
    report_and_terminate(report);
}

void MtePolicy::before_syscall_return() { drain_pending(); }

void MtePolicy::before_external_send() { drain_pending(); }

bool MtePolicy::pending_async_fault() const {
    std::lock_guard<std::mutex> lock(mu_);
    const ThreadState* state = find_thread(std::this_thread::get_id());
    return state != nullptr && state->pending;
}

} // namespace netsec
