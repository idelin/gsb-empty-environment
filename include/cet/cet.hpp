#ifndef CET_CET_HPP
#define CET_CET_HPP

#include <cstdint>
#include <mutex>
#include <optional>
#include <unordered_map>
#include <vector>

namespace cet {

using Address = std::uint64_t;

inline constexpr Address kPageSize = 0x1000ULL;
inline constexpr Address kPageMask = kPageSize - 1ULL;
inline constexpr Address kUserAddressLimit = 0x00007fffffffffffULL;
inline constexpr Address kKernelAddressBase = 0xffff800000000000ULL;
inline constexpr std::uint32_t kMaxIncsspqSlots = 255U;
inline constexpr Address kMaxShadowStackPages = 4096ULL;

enum class Cpl : std::uint8_t {
    Ring0 = 0,
    Ring3 = 3,
};

enum class PageKind : std::uint8_t {
    NormalWritable,
    ShadowStackReadOnly,
};

enum class TrackerState : std::uint8_t {
    Idle,
    WaitForEndbranch,
};

enum class Opcode : std::uint8_t {
    Other,
    Endbr64,
};

enum class SwitchReason : std::uint8_t {
    Rstorssp,
    InterruptReturn,
    SignalReturn,
};

enum class Status : std::uint8_t {
    Ok = 0,
    InvalidArgument,
    NotAttached,
    NonCanonicalAddress,
    MisalignedAddress,
    UnmappedPage,
    PageAlreadyMapped,
    WrongPageKind,
    CrossPrivilegeAddress,
    ShadowStackEmpty,
    ShadowStackOverflow,
    ShadowStackCorrupt,
    ReturnAddressMismatch,
    IncsspqLimitExceeded,
    IncsspqFrameExceeded,
    InvalidRestoreToken,
    TokenModeMismatch,
    TokenAddressMismatch,
    TokenBusy,
    IbtMissingEndbr64,
    NoExceptionFrame,
    InvalidStateTransition,
};

struct Result {
    Status code = Status::Ok;

    [[nodiscard]] constexpr bool ok() const noexcept {
        return code == Status::Ok;
    }

    [[nodiscard]] constexpr explicit operator bool() const noexcept {
        return ok();
    }
};

[[nodiscard]] const char* status_message(Status status) noexcept;

struct RestoreToken {
    Address address = 0;
    Cpl mode = Cpl::Ring0;
    bool busy = false;
};

inline constexpr std::uint64_t kTokenBusyBit = 1ULL << 0U;
inline constexpr std::uint64_t kTokenModeUserBit = 1ULL << 1U;
inline constexpr std::uint64_t kTokenReservedBit = 1ULL << 2U;
inline constexpr std::uint64_t kTokenAddressMask = ~0x7ULL;

[[nodiscard]] RestoreToken make_restore_token(
    Address token_address,
    Cpl mode,
    bool busy = false) noexcept;
[[nodiscard]] std::uint64_t encode_restore_token(const RestoreToken& token) noexcept;
[[nodiscard]] RestoreToken decode_restore_token(std::uint64_t raw) noexcept;

class ThreadState {
public:
    ThreadState() = default;

    [[nodiscard]] Cpl cpl() const noexcept {
        return cpl_;
    }

    [[nodiscard]] Address ssp() const noexcept {
        return ssp_;
    }

    [[nodiscard]] TrackerState tracker() const noexcept {
        return tracker_;
    }

    [[nodiscard]] bool attached() const noexcept {
        return attached_;
    }

private:
    friend class CetMachine;

    Cpl cpl_ = Cpl::Ring3;
    Address ssp_ = 0;
    Address shadow_low_ = 0;
    Address shadow_top_ = 0;
    std::uint64_t shadow_entries_ = 0;
    TrackerState tracker_ = TrackerState::Idle;
    std::vector<TrackerState> exception_trackers_;
    bool attached_ = false;
};

class CetMachine {
public:
    CetMachine() = default;
    CetMachine(const CetMachine&) = delete;
    CetMachine& operator=(const CetMachine&) = delete;

    [[nodiscard]] Result map_page(Address page_base, PageKind kind);
    [[nodiscard]] Result unmap_page(Address page_base);
    [[nodiscard]] std::optional<PageKind> page_kind(Address address) const;

    [[nodiscard]] Result attach_thread(
        ThreadState& thread,
        Cpl cpl,
        Address shadow_low,
        Address shadow_top);

    [[nodiscard]] Result direct_call(
        ThreadState& thread,
        Address return_address,
        Address target);
    [[nodiscard]] Result indirect_call(
        ThreadState& thread,
        Address return_address,
        Address target);
    [[nodiscard]] Result direct_jump(ThreadState& thread, Address target);
    [[nodiscard]] Result conditional_jump(ThreadState& thread, Address target);
    [[nodiscard]] Result indirect_jump(ThreadState& thread, Address target);
    [[nodiscard]] Result ret(ThreadState& thread, Address normal_return_address);
    [[nodiscard]] Result incsspq(ThreadState& thread, std::uint32_t slots);

    [[nodiscard]] Result fetch(ThreadState& thread, Opcode opcode);
    [[nodiscard]] Result enter_exception(ThreadState& thread);
    [[nodiscard]] Result leave_exception(ThreadState& thread);
    [[nodiscard]] Result enter_signal(ThreadState& thread);
    [[nodiscard]] Result leave_signal(ThreadState& thread);

    [[nodiscard]] Result saveprevssp(ThreadState& thread, Address token_address);
    [[nodiscard]] Result rstorssp(
        ThreadState& thread,
        Address token_address,
        SwitchReason reason = SwitchReason::Rstorssp);
    [[nodiscard]] Result switch_shadow_stack(
        ThreadState& thread,
        Address token_address,
        Cpl target_cpl,
        SwitchReason reason = SwitchReason::SignalReturn);
    [[nodiscard]] Result interrupt_return(ThreadState& thread, Address token_address);
    [[nodiscard]] Result signal_return(ThreadState& thread, Address token_address);
    [[nodiscard]] Result interrupt_return_to_user(
        ThreadState& thread,
        Address token_address);
    [[nodiscard]] Result signal_return_to_user(
        ThreadState& thread,
        Address token_address);

    [[nodiscard]] Result write_shadow_word(Address address, std::uint64_t value);
    [[nodiscard]] Result write_normal_word(Address address, std::uint64_t value);
    [[nodiscard]] std::optional<std::uint64_t> read_shadow_word(Address address) const;
    [[nodiscard]] std::optional<std::uint64_t> read_word(Address address) const;

    [[nodiscard]] Result place_restore_token(
        Address token_address,
        const RestoreToken& token);
    [[nodiscard]] std::optional<RestoreToken> read_restore_token(
        Address token_address) const;

private:
    struct ShadowRegion {
        Address low = 0;
        Address top = 0;
    };

    [[nodiscard]] Status validate_code_address(Address address, Cpl cpl) const noexcept;
    [[nodiscard]] Status validate_shadow_address(Address address, Cpl cpl) const;
    [[nodiscard]] Status validate_attached(const ThreadState& thread) const noexcept;
    [[nodiscard]] Status push_return(ThreadState& thread, Address return_address);
    [[nodiscard]] Status execute_direct_branch(
        ThreadState& thread,
        Address target,
        bool is_call,
        Address return_address);
    [[nodiscard]] Status execute_indirect_branch(
        ThreadState& thread,
        Address target,
        bool is_call,
        Address return_address);
    [[nodiscard]] ShadowRegion find_shadow_region(Address address, Cpl cpl) const;
    [[nodiscard]] Status restore_shadow_stack(
        ThreadState& thread,
        Address token_address,
        Cpl target_cpl,
        SwitchReason reason);

    mutable std::mutex mutex_;
    std::unordered_map<Address, PageKind> pages_;
    std::unordered_map<Address, std::uint64_t> memory_;
};

}  // namespace cet

#endif
