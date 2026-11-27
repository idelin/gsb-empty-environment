#include "cet/cet.hpp"

#include <limits>

namespace cet {
namespace {

[[nodiscard]] constexpr bool is_canonical(Address address) noexcept {
    const Address high = address >> 47U;
    return high == 0ULL || high == 0x1ffffULL;
}

[[nodiscard]] constexpr bool is_user_address(Address address) noexcept {
    return is_canonical(address) && address <= kUserAddressLimit;
}

[[nodiscard]] constexpr bool is_kernel_address(Address address) noexcept {
    return is_canonical(address) && address >= kKernelAddressBase;
}

[[nodiscard]] constexpr bool is_valid_cpl(Cpl cpl) noexcept {
    return cpl == Cpl::Ring0 || cpl == Cpl::Ring3;
}

[[nodiscard]] constexpr bool belongs_to_cpl(Address address, Cpl cpl) noexcept {
    if (cpl == Cpl::Ring3) {
        return is_user_address(address);
    }
    if (cpl == Cpl::Ring0) {
        return is_kernel_address(address);
    }
    return false;
}

[[nodiscard]] constexpr bool is_page_aligned(Address address) noexcept {
    return (address & kPageMask) == 0ULL;
}

[[nodiscard]] constexpr bool is_word_aligned(Address address) noexcept {
    return (address & 0x7ULL) == 0ULL;
}

[[nodiscard]] constexpr Address page_base(Address address) noexcept {
    return address & ~kPageMask;
}

[[nodiscard]] constexpr Address page_end(Address base) noexcept {
    return base + kPageSize;
}

[[nodiscard]] constexpr std::uint64_t mode_bit(Cpl cpl) noexcept {
    return cpl == Cpl::Ring3 ? kTokenModeUserBit : 0ULL;
}

[[nodiscard]] Result result(Status status) noexcept {
    return Result{status};
}

}  // namespace

const char* status_message(Status status) noexcept {
    switch (status) {
        case Status::Ok:
            return "ok";
        case Status::InvalidArgument:
            return "invalid argument";
        case Status::NotAttached:
            return "thread has no attached shadow stack";
        case Status::NonCanonicalAddress:
            return "non-canonical virtual address";
        case Status::MisalignedAddress:
            return "address alignment violation";
        case Status::UnmappedPage:
            return "virtual page is not mapped";
        case Status::PageAlreadyMapped:
            return "virtual page is already mapped";
        case Status::WrongPageKind:
            return "page kind does not permit the operation";
        case Status::CrossPrivilegeAddress:
            return "address crosses the current privilege boundary";
        case Status::ShadowStackEmpty:
            return "shadow stack has no return frame";
        case Status::ShadowStackOverflow:
            return "shadow stack bound exceeded";
        case Status::ShadowStackCorrupt:
            return "shadow stack slot is missing or corrupt";
        case Status::ReturnAddressMismatch:
            return "normal and shadow return addresses differ";
        case Status::IncsspqLimitExceeded:
            return "INCSSPQ step exceeds the architectural limit";
        case Status::IncsspqFrameExceeded:
            return "INCSSPQ step would skip beyond the current frame chain";
        case Status::InvalidRestoreToken:
            return "restore token encoding is invalid";
        case Status::TokenModeMismatch:
            return "restore token mode does not match CPL";
        case Status::TokenAddressMismatch:
            return "restore token is not exactly self-referential";
        case Status::TokenBusy:
            return "restore token busy bit is already set";
        case Status::IbtMissingEndbr64:
            return "indirect branch target does not begin with ENDBR64";
        case Status::NoExceptionFrame:
            return "no exception or signal frame to return from";
        case Status::InvalidStateTransition:
            return "invalid CET state transition";
    }
    return "unknown CET status";
}

RestoreToken make_restore_token(Address token_address, Cpl mode, bool busy) noexcept {
    return RestoreToken{token_address, mode, busy};
}

std::uint64_t encode_restore_token(const RestoreToken& token) noexcept {
    std::uint64_t raw = token.address & kTokenAddressMask;
    raw |= mode_bit(token.mode);
    if (token.busy) {
        raw |= kTokenBusyBit;
    }
    return raw;
}

RestoreToken decode_restore_token(std::uint64_t raw) noexcept {
    return RestoreToken{
        raw & kTokenAddressMask,
        (raw & kTokenModeUserBit) != 0ULL ? Cpl::Ring3 : Cpl::Ring0,
        (raw & kTokenBusyBit) != 0ULL,
    };
}

Result CetMachine::map_page(Address base, PageKind kind) {
    if (!is_page_aligned(base) || !is_canonical(base) ||
        base >= std::numeric_limits<Address>::max() - kPageMask) {
        return result(Status::InvalidArgument);
    }

    const std::lock_guard<std::mutex> lock(mutex_);
    const auto inserted = pages_.emplace(base, kind);
    if (!inserted.second) {
        return result(Status::PageAlreadyMapped);
    }
    return result(Status::Ok);
}

Result CetMachine::unmap_page(Address base) {
    if (!is_page_aligned(base)) {
        return result(Status::InvalidArgument);
    }

    const std::lock_guard<std::mutex> lock(mutex_);
    const auto erased = pages_.erase(base);
    if (erased == 0U) {
        return result(Status::UnmappedPage);
    }

    const Address first = base;
    const Address last = page_end(base);
    for (auto it = memory_.begin(); it != memory_.end();) {
        if (it->first >= first && it->first < last) {
            it = memory_.erase(it);
        } else {
            ++it;
        }
    }
    return result(Status::Ok);
}

std::optional<PageKind> CetMachine::page_kind(Address address) const {
    const std::lock_guard<std::mutex> lock(mutex_);
    const auto it = pages_.find(page_base(address));
    if (it == pages_.end()) {
        return std::nullopt;
    }
    return it->second;
}

Result CetMachine::attach_thread(
    ThreadState& thread,
    Cpl cpl,
    Address shadow_low,
    Address shadow_top) {
    if (!is_valid_cpl(cpl) || !is_page_aligned(shadow_low) ||
        !is_page_aligned(shadow_top) || shadow_low >= shadow_top) {
        return result(Status::InvalidArgument);
    }

    const std::lock_guard<std::mutex> lock(mutex_);

    if (!belongs_to_cpl(shadow_low, cpl) ||
        !belongs_to_cpl(shadow_top - 1ULL, cpl)) {
        return result(Status::CrossPrivilegeAddress);
    }

    const Address page_count = (shadow_top - shadow_low) / kPageSize;
    if (page_count == 0ULL || page_count > kMaxShadowStackPages) {
        return result(Status::InvalidArgument);
    }

    for (Address base = shadow_low; base < shadow_top; base += kPageSize) {
        const auto it = pages_.find(base);
        if (it == pages_.end()) {
            return result(Status::UnmappedPage);
        }
        if (it->second != PageKind::ShadowStackReadOnly) {
            return result(Status::WrongPageKind);
        }
    }

    thread.cpl_ = cpl;
    thread.ssp_ = shadow_top;
    thread.shadow_low_ = shadow_low;
    thread.shadow_top_ = shadow_top;
    thread.shadow_entries_ = 0ULL;
    thread.tracker_ = TrackerState::Idle;
    thread.exception_trackers_.clear();
    thread.attached_ = true;
    return result(Status::Ok);
}

Status CetMachine::validate_code_address(Address address, Cpl cpl) const noexcept {
    if (!is_canonical(address)) {
        return Status::NonCanonicalAddress;
    }
    if (!belongs_to_cpl(address, cpl)) {
        return Status::CrossPrivilegeAddress;
    }
    return Status::Ok;
}

Status CetMachine::validate_shadow_address(Address address, Cpl cpl) const {
    if (!is_word_aligned(address)) {
        return Status::MisalignedAddress;
    }
    if (!is_canonical(address)) {
        return Status::NonCanonicalAddress;
    }
    if (!belongs_to_cpl(address, cpl)) {
        return Status::CrossPrivilegeAddress;
    }

    const auto it = pages_.find(page_base(address));
    if (it == pages_.end()) {
        return Status::UnmappedPage;
    }
    if (it->second != PageKind::ShadowStackReadOnly) {
        return Status::WrongPageKind;
    }
    return Status::Ok;
}

Status CetMachine::validate_attached(const ThreadState& thread) const noexcept {
    if (!thread.attached_ || !is_valid_cpl(thread.cpl_)) {
        return Status::NotAttached;
    }
    return Status::Ok;
}

Status CetMachine::push_return(ThreadState& thread, Address return_address) {
    const Status address_status = validate_code_address(return_address, thread.cpl_);
    if (address_status != Status::Ok) {
        return address_status;
    }

    if (thread.shadow_entries_ >=
        (thread.shadow_top_ - thread.shadow_low_) / sizeof(Address)) {
        return Status::ShadowStackOverflow;
    }
    if (thread.ssp_ < thread.shadow_low_ + sizeof(Address)) {
        return Status::ShadowStackOverflow;
    }

    const Address next_ssp = thread.ssp_ - sizeof(Address);
    const Status shadow_status = validate_shadow_address(next_ssp, thread.cpl_);
    if (shadow_status != Status::Ok) {
        return shadow_status;
    }

    memory_[next_ssp] = return_address;
    thread.ssp_ = next_ssp;
    ++thread.shadow_entries_;
    return Status::Ok;
}

Status CetMachine::execute_direct_branch(
    ThreadState& thread,
    Address target,
    bool is_call,
    Address return_address) {
    const Status attached_status = validate_attached(thread);
    if (attached_status != Status::Ok) {
        return attached_status;
    }
    if (thread.tracker_ == TrackerState::WaitForEndbranch) {
        return Status::IbtMissingEndbr64;
    }

    const Status target_status = validate_code_address(target, thread.cpl_);
    if (target_status != Status::Ok) {
        return target_status;
    }

    if (is_call) {
        return push_return(thread, return_address);
    }
    return Status::Ok;
}

Status CetMachine::execute_indirect_branch(
    ThreadState& thread,
    Address target,
    bool is_call,
    Address return_address) {
    const Status attached_status = validate_attached(thread);
    if (attached_status != Status::Ok) {
        return attached_status;
    }
    if (thread.tracker_ == TrackerState::WaitForEndbranch) {
        return Status::IbtMissingEndbr64;
    }

    const Status target_status = validate_code_address(target, thread.cpl_);
    if (target_status != Status::Ok) {
        return target_status;
    }

    if (is_call) {
        const Status push_status = push_return(thread, return_address);
        if (push_status != Status::Ok) {
            return push_status;
        }
    }

    thread.tracker_ = TrackerState::WaitForEndbranch;
    return Status::Ok;
}

Result CetMachine::direct_call(
    ThreadState& thread,
    Address return_address,
    Address target) {
    const std::lock_guard<std::mutex> lock(mutex_);
    return result(execute_direct_branch(thread, target, true, return_address));
}

Result CetMachine::indirect_call(
    ThreadState& thread,
    Address return_address,
    Address target) {
    const std::lock_guard<std::mutex> lock(mutex_);
    return result(execute_indirect_branch(thread, target, true, return_address));
}

Result CetMachine::direct_jump(ThreadState& thread, Address target) {
    const std::lock_guard<std::mutex> lock(mutex_);
    return result(execute_direct_branch(thread, target, false, 0ULL));
}

Result CetMachine::conditional_jump(ThreadState& thread, Address target) {
    const std::lock_guard<std::mutex> lock(mutex_);
    return result(execute_direct_branch(thread, target, false, 0ULL));
}

Result CetMachine::indirect_jump(ThreadState& thread, Address target) {
    const std::lock_guard<std::mutex> lock(mutex_);
    return result(execute_indirect_branch(thread, target, false, 0ULL));
}

Result CetMachine::ret(ThreadState& thread, Address normal_return_address) {
    const std::lock_guard<std::mutex> lock(mutex_);

    const Status attached_status = validate_attached(thread);
    if (attached_status != Status::Ok) {
        return result(attached_status);
    }
    const Status address_status =
        validate_code_address(normal_return_address, thread.cpl_);
    if (address_status != Status::Ok) {
        return result(address_status);
    }
    if (thread.shadow_entries_ == 0ULL) {
        return result(Status::ShadowStackEmpty);
    }

    const Status shadow_status = validate_shadow_address(thread.ssp_, thread.cpl_);
    if (shadow_status != Status::Ok) {
        return result(shadow_status);
    }

    const auto it = memory_.find(thread.ssp_);
    if (it == memory_.end()) {
        return result(Status::ShadowStackCorrupt);
    }
    if (it->second != normal_return_address) {
        return result(Status::ReturnAddressMismatch);
    }

    if (thread.ssp_ > std::numeric_limits<Address>::max() - sizeof(Address)) {
        return result(Status::ShadowStackOverflow);
    }
    thread.ssp_ += sizeof(Address);
    --thread.shadow_entries_;
    return result(Status::Ok);
}

Result CetMachine::incsspq(ThreadState& thread, std::uint32_t slots) {
    const std::lock_guard<std::mutex> lock(mutex_);

    const Status attached_status = validate_attached(thread);
    if (attached_status != Status::Ok) {
        return result(attached_status);
    }
    if (slots > kMaxIncsspqSlots) {
        return result(Status::IncsspqLimitExceeded);
    }

    const std::uint64_t slot_count = static_cast<std::uint64_t>(slots);
    if (slot_count > thread.shadow_entries_) {
        return result(Status::IncsspqFrameExceeded);
    }
    if (slot_count == 0ULL) {
        return result(Status::Ok);
    }

    const std::uint64_t byte_count = slot_count * sizeof(Address);
    if (thread.ssp_ > std::numeric_limits<Address>::max() - byte_count) {
        return result(Status::ShadowStackOverflow);
    }
    const Address next_ssp = thread.ssp_ + byte_count;
    if (next_ssp > thread.shadow_top_) {
        return result(Status::IncsspqFrameExceeded);
    }

    Address cursor = thread.ssp_;
    for (std::uint64_t index = 0ULL; index < slot_count; ++index) {
        const Status shadow_status = validate_shadow_address(cursor, thread.cpl_);
        if (shadow_status != Status::Ok) {
            return result(shadow_status);
        }
        cursor += sizeof(Address);
    }

    thread.ssp_ = next_ssp;
    thread.shadow_entries_ -= slot_count;
    return result(Status::Ok);
}

Result CetMachine::fetch(ThreadState& thread, Opcode opcode) {
    const std::lock_guard<std::mutex> lock(mutex_);

    const Status attached_status = validate_attached(thread);
    if (attached_status != Status::Ok) {
        return result(attached_status);
    }

    if (thread.tracker_ == TrackerState::WaitForEndbranch) {
        if (opcode != Opcode::Endbr64) {
            return result(Status::IbtMissingEndbr64);
        }
        thread.tracker_ = TrackerState::Idle;
    }
    return result(Status::Ok);
}

Result CetMachine::enter_exception(ThreadState& thread) {
    const std::lock_guard<std::mutex> lock(mutex_);

    const Status attached_status = validate_attached(thread);
    if (attached_status != Status::Ok) {
        return result(attached_status);
    }

    thread.exception_trackers_.push_back(thread.tracker_);
    thread.tracker_ = TrackerState::Idle;
    return result(Status::Ok);
}

Result CetMachine::leave_exception(ThreadState& thread) {
    const std::lock_guard<std::mutex> lock(mutex_);

    const Status attached_status = validate_attached(thread);
    if (attached_status != Status::Ok) {
        return result(attached_status);
    }
    if (thread.exception_trackers_.empty()) {
        return result(Status::NoExceptionFrame);
    }

    thread.tracker_ = thread.exception_trackers_.back();
    thread.exception_trackers_.pop_back();
    return result(Status::Ok);
}

Result CetMachine::enter_signal(ThreadState& thread) {
    return enter_exception(thread);
}

Result CetMachine::leave_signal(ThreadState& thread) {
    return leave_exception(thread);
}

Result CetMachine::saveprevssp(ThreadState& thread, Address token_address) {
    const std::lock_guard<std::mutex> lock(mutex_);

    const Status attached_status = validate_attached(thread);
    if (attached_status != Status::Ok) {
        return result(attached_status);
    }
    if (thread.ssp_ < thread.shadow_low_ + sizeof(Address)) {
        return result(Status::ShadowStackOverflow);
    }
    if (token_address != thread.ssp_ - sizeof(Address)) {
        return result(Status::InvalidArgument);
    }

    const Status shadow_status =
        validate_shadow_address(token_address, thread.cpl_);
    if (shadow_status != Status::Ok) {
        return result(shadow_status);
    }

    const auto existing = memory_.find(token_address);
    if (existing != memory_.end() &&
        (existing->second & kTokenBusyBit) != 0ULL) {
        return result(Status::TokenBusy);
    }

    memory_[token_address] =
        encode_restore_token(make_restore_token(token_address, thread.cpl_, false));
    thread.ssp_ = token_address;
    return result(Status::Ok);
}

CetMachine::ShadowRegion CetMachine::find_shadow_region(
    Address address,
    Cpl cpl) const {
    const Address center = page_base(address);
    Address low = center;
    Address top = page_end(center);

    Address down = 0ULL;
    while (low > 0ULL && down < kMaxShadowStackPages) {
        const Address candidate = low - kPageSize;
        const auto it = pages_.find(candidate);
        if (it == pages_.end() ||
            it->second != PageKind::ShadowStackReadOnly ||
            !belongs_to_cpl(candidate, cpl)) {
            break;
        }
        low = candidate;
        ++down;
    }

    Address up = 0ULL;
    while (up < kMaxShadowStackPages) {
        if (top > std::numeric_limits<Address>::max() - kPageSize) {
            break;
        }
        const auto it = pages_.find(top);
        if (it == pages_.end() ||
            it->second != PageKind::ShadowStackReadOnly ||
            !belongs_to_cpl(top, cpl)) {
            break;
        }
        top = page_end(top);
        ++up;
    }

    return ShadowRegion{low, top};
}

Status CetMachine::restore_shadow_stack(
    ThreadState& thread,
    Address token_address,
    Cpl target_cpl,
    SwitchReason reason) {
    const Status attached_status = validate_attached(thread);
    if (attached_status != Status::Ok) {
        return attached_status;
    }
    if (!is_valid_cpl(target_cpl)) {
        return Status::InvalidArgument;
    }
    const Cpl previous_cpl = thread.cpl_;
    if (previous_cpl == Cpl::Ring3 && target_cpl == Cpl::Ring0) {
        return Status::CrossPrivilegeAddress;
    }
    if (target_cpl != previous_cpl && reason == SwitchReason::Rstorssp) {
        return Status::InvalidStateTransition;
    }

    const Status token_page_status =
        validate_shadow_address(token_address, target_cpl);
    if (token_page_status != Status::Ok) {
        return token_page_status;
    }

    const auto it = memory_.find(token_address);
    if (it == memory_.end()) {
        return Status::InvalidRestoreToken;
    }

    const std::uint64_t raw = it->second;
    if ((raw & kTokenReservedBit) != 0ULL) {
        return Status::InvalidRestoreToken;
    }

    const RestoreToken token = decode_restore_token(raw);
    if (token.mode != target_cpl) {
        return Status::TokenModeMismatch;
    }
    if (token.address != token_address) {
        return Status::TokenAddressMismatch;
    }
    if (token.busy || (raw & kTokenBusyBit) != 0ULL) {
        return Status::TokenBusy;
    }
    if (token_address >
        std::numeric_limits<Address>::max() - sizeof(Address)) {
        return Status::ShadowStackOverflow;
    }

    const Address next_ssp = token_address + sizeof(Address);
    const ShadowRegion region = find_shadow_region(token_address, target_cpl);
    if (next_ssp < region.low || next_ssp > region.top) {
        return Status::CrossPrivilegeAddress;
    }

    const Status next_status = validate_shadow_address(next_ssp, target_cpl);
    if (next_status != Status::Ok) {
        const bool empty_stack_at_region_top =
            next_status == Status::UnmappedPage && next_ssp == region.top;
        if (!empty_stack_at_region_top) {
            return next_status;
        }
    }

    it->second = raw | kTokenBusyBit;
    thread.cpl_ = target_cpl;
    thread.ssp_ = next_ssp;
    thread.shadow_low_ = region.low;
    thread.shadow_top_ = region.top;
    thread.shadow_entries_ = 0ULL;
    if (target_cpl != previous_cpl) {
        thread.tracker_ = TrackerState::Idle;
        thread.exception_trackers_.clear();
    }
    return Status::Ok;
}

Result CetMachine::rstorssp(
    ThreadState& thread,
    Address token_address,
    SwitchReason reason) {
    if (reason != SwitchReason::Rstorssp &&
        reason != SwitchReason::InterruptReturn &&
        reason != SwitchReason::SignalReturn) {
        return result(Status::InvalidArgument);
    }

    const std::lock_guard<std::mutex> lock(mutex_);
    return result(
        restore_shadow_stack(thread, token_address, thread.cpl_, reason));
}

Result CetMachine::switch_shadow_stack(
    ThreadState& thread,
    Address token_address,
    Cpl target_cpl,
    SwitchReason reason) {
    if (reason != SwitchReason::Rstorssp &&
        reason != SwitchReason::InterruptReturn &&
        reason != SwitchReason::SignalReturn) {
        return result(Status::InvalidArgument);
    }

    const std::lock_guard<std::mutex> lock(mutex_);
    return result(
        restore_shadow_stack(thread, token_address, target_cpl, reason));
}

Result CetMachine::interrupt_return(ThreadState& thread, Address token_address) {
    return rstorssp(thread, token_address, SwitchReason::InterruptReturn);
}

Result CetMachine::signal_return(ThreadState& thread, Address token_address) {
    return rstorssp(thread, token_address, SwitchReason::SignalReturn);
}

Result CetMachine::interrupt_return_to_user(
    ThreadState& thread,
    Address token_address) {
    return switch_shadow_stack(
        thread,
        token_address,
        Cpl::Ring3,
        SwitchReason::InterruptReturn);
}

Result CetMachine::signal_return_to_user(
    ThreadState& thread,
    Address token_address) {
    return switch_shadow_stack(
        thread,
        token_address,
        Cpl::Ring3,
        SwitchReason::SignalReturn);
}

Result CetMachine::write_shadow_word(Address address, std::uint64_t value) {
    const std::lock_guard<std::mutex> lock(mutex_);

    const auto kind = pages_.find(page_base(address));
    if (kind == pages_.end()) {
        return result(Status::UnmappedPage);
    }
    if (!is_word_aligned(address)) {
        return result(Status::MisalignedAddress);
    }
    if (kind->second != PageKind::ShadowStackReadOnly) {
        return result(Status::WrongPageKind);
    }

    memory_[address] = value;
    return result(Status::Ok);
}

Result CetMachine::write_normal_word(Address address, std::uint64_t value) {
    const std::lock_guard<std::mutex> lock(mutex_);

    const auto kind = pages_.find(page_base(address));
    if (kind == pages_.end()) {
        return result(Status::UnmappedPage);
    }
    if (!is_word_aligned(address)) {
        return result(Status::MisalignedAddress);
    }
    if (kind->second != PageKind::NormalWritable) {
        return result(Status::WrongPageKind);
    }

    memory_[address] = value;
    return result(Status::Ok);
}

std::optional<std::uint64_t> CetMachine::read_shadow_word(Address address) const {
    const std::lock_guard<std::mutex> lock(mutex_);

    if (!is_word_aligned(address)) {
        return std::nullopt;
    }
    const auto kind = pages_.find(page_base(address));
    if (kind == pages_.end() ||
        kind->second != PageKind::ShadowStackReadOnly) {
        return std::nullopt;
    }

    const auto it = memory_.find(address);
    if (it == memory_.end()) {
        return std::nullopt;
    }
    return it->second;
}

std::optional<std::uint64_t> CetMachine::read_word(Address address) const {
    const std::lock_guard<std::mutex> lock(mutex_);

    if (!is_word_aligned(address) ||
        pages_.find(page_base(address)) == pages_.end()) {
        return std::nullopt;
    }

    const auto it = memory_.find(address);
    if (it == memory_.end()) {
        return std::nullopt;
    }
    return it->second;
}

Result CetMachine::place_restore_token(
    Address token_address,
    const RestoreToken& token) {
    const std::lock_guard<std::mutex> lock(mutex_);

    if (!is_word_aligned(token_address)) {
        return result(Status::MisalignedAddress);
    }

    const auto kind = pages_.find(page_base(token_address));
    if (kind == pages_.end()) {
        return result(Status::UnmappedPage);
    }
    if (kind->second != PageKind::ShadowStackReadOnly) {
        return result(Status::WrongPageKind);
    }

    memory_[token_address] = encode_restore_token(token);
    return result(Status::Ok);
}

std::optional<RestoreToken> CetMachine::read_restore_token(
    Address token_address) const {
    const std::lock_guard<std::mutex> lock(mutex_);

    if (!is_word_aligned(token_address)) {
        return std::nullopt;
    }
    const auto it = memory_.find(token_address);
    if (it == memory_.end()) {
        return std::nullopt;
    }
    return decode_restore_token(it->second);
}

}  // namespace cet
