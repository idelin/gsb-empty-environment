// SPDX-License-Identifier: MIT
//
// cet.h — Symbolic Intel CET (Shadow Stack + IBT) instruction-stream verifier.
//
// The library models a CET-enabled 64-bit user-space runtime at the
// instruction-stream level.  It tracks a conventional stack, a write-protected
// shadow stack, the shadow stack pointer (SSP), the CET restore-token table and
// the IBT landing-pad state machine (IDLE / WAIT_FOR_ENDBRANCH).
//
// The implementation is self contained (C++17 standard library only) and is
// suitable for embedding inside a runtime security-analysis host.
#ifndef CET_CET_H
#define CET_CET_H

#include <cstddef>
#include <cstdint>
#include <atomic>
#include <memory>
#include <stdexcept>
#include <string>
#include <unordered_map>
#include <unordered_set>
#include <vector>

namespace cet {

// Control-protection (#CP) fault classification.  Every model condition that a
// real CET implementation would raise as #CP maps onto one of these codes.
enum class FaultCode : std::uint8_t {
    None = 0,
    ReturnAddressMismatch,   // RET: normal stack and shadow stack disagree
    ShadowStackUnderflow,    // POP-equivalent with no shadow slot available
    RegularStackUnderflow,   // RET with an empty conventional stack
    ShadowStoreDenied,       // Plain memory write targets a shadow page
    ShadowLoadDenied,        // Plain memory read targets a shadow page
    ShadowAccessOutsidePage, // CET op touches an address with no shadow mapping
    NormalAccessOutsideRegion,
    StackOverflow,
    MisalignedToken,         // Restore-token address / SSP not 8-byte aligned
    TokenBusy,               // Token bit0 (busy) is already set
    TokenModeBitClear,       // Token bit1 (64-bit mode) is clear
    TokenForged,             // Token value is absent from the restore-token table
    TokenReplay,             // Re-acquisition of an already active shadow stack
    NoPreviousShadowStack,   // SAVEPREVSSP without a preceding RSTORSSP
    IncsspOverreach,         // INCSSP would skip a protected frame / boundary
    MissingEndBranch,        // Indirect branch landed on anything but ENDBR64
    UnexpectedEndBranch,     // ENDBR64 executed while IBT state is IDLE
    UnmappedShadowAddress,
    InvalidOperand
};

const char* fault_name(FaultCode code) noexcept;

// Modeled instruction mnemonics.
enum class Opcode : std::uint8_t {
    CallDirect,
    CallIndirect,
    Ret,
    JmpIndirect,
    EndBr64,
    Incssp,
    Rstorssp,
    SavePrevSsp,
    Rdssp,
    Wrss,
    Store,
    Load,
    SetRsp
};

// IBT finite state machine.
enum class IbtState : std::uint8_t {
    Idle,
    WaitForEndbranch
};

// Classification of a shadow-stack 8-byte slot, used by the INCSSP overreach
// policy to tell ordinary call frames apart from switch boundaries.
enum class FrameKind : std::uint8_t {
    ReturnAddress,   // Pushed by CALL
    RestoreToken,    // Issued through the restore-token table / WRSS
    PrevSspToken     // Written by SAVEPREVSSP
};

// A single instruction in the symbolic stream.  Construct instances with the
// factory functions below; every field has an explicit default.
struct Instruction {
    Opcode opcode = Opcode::Ret;
    std::uint64_t address = 0; // Memory operand (token / data address)
    std::uint64_t count = 0;   // INCSSP frame count
    std::uint64_t target = 0;  // Indirect branch destination (informational)
};

Instruction make_call_direct() noexcept;
Instruction make_call_indirect(std::uint64_t target) noexcept;
Instruction make_ret() noexcept;
Instruction make_jmp_indirect(std::uint64_t target) noexcept;
Instruction make_endbr64() noexcept;
Instruction make_incssp(std::uint64_t frames) noexcept;
Instruction make_rstorssp(std::uint64_t token_address) noexcept;
Instruction make_saveprev_ssp() noexcept;
// RDSSP stores the current SSP into a normal-memory jmp_buf slot.
Instruction make_rdssp(std::uint64_t save_address) noexcept;
// WRSS is the only sanctioned data write into a shadow page.
Instruction make_wrss(std::uint64_t shadow_address, std::uint64_t value) noexcept;
// Plain load/store against the conventional address space.
Instruction make_store(std::uint64_t address, std::uint64_t value) noexcept;
Instruction make_load(std::uint64_t address) noexcept;
// Models the conventional RSP load performed by longjmp (normal page only).
Instruction make_set_rsp(std::uint64_t new_rsp) noexcept;

// Thrown on every modeled #CP condition.  Derives from runtime_error so host
// code can distinguish CET violations from ordinary logic errors.
class ControlProtectionFault : public std::runtime_error {
public:
    ControlProtectionFault(FaultCode code, const std::string& message);
    FaultCode code() const noexcept { return code_; }

private:
    FaultCode code_;
};

// A mapped address region.  Shadow regions form the read-mostly shadow pages;
// all other regions are conventional (normal) memory.
struct Region {
    std::uint64_t base = 0;
    std::uint64_t size = 0;
    bool shadow = false;
};

// Restore-token table entry.  The token lives at token_address inside a shadow
// page; its payload encodes target_ssp, the 64-bit mode flag and the busy flag.
struct RestoreTokenRecord {
    std::uint64_t token_address = 0;
    std::uint64_t target_ssp = 0;
    std::uint64_t owner_domain = 0;
    bool busy = false;
};

// Result returned by the non-throwing verify() entry point.
struct VerificationReport {
    bool ok = true;
    FaultCode fault = FaultCode::None;
    std::string message;
    std::size_t instruction_index = 0;
    std::uint64_t rip = 0;
};

// One shadow-stack slot.  The payload is atomic because RSTORSSP performs an
// atomic compare-and-set of the busy bit, exactly like hardware.
struct ShadowSlot {
    ShadowSlot() = default;
    ShadowSlot(std::uint64_t value, FrameKind kind, std::uint64_t owner)
        : payload(value), frame_kind(kind), owner_domain(owner) {}
    ShadowSlot(const ShadowSlot& other)
        : payload(other.payload.load(std::memory_order_acquire)),
          frame_kind(other.frame_kind),
          owner_domain(other.owner_domain) {}
    ShadowSlot& operator=(const ShadowSlot& other) {
        if (this != &other) {
            payload.store(other.payload.load(std::memory_order_acquire),
                          std::memory_order_release);
            frame_kind = other.frame_kind;
            owner_domain = other.owner_domain;
        }
        return *this;
    }

    std::atomic<std::uint64_t> payload{0};
    FrameKind frame_kind = FrameKind::ReturnAddress;
    std::uint64_t owner_domain = 0;
};

class CetMachine {
public:
    CetMachine();
    ~CetMachine();
    CetMachine(const CetMachine&) = delete;
    CetMachine& operator=(const CetMachine&) = delete;
    CetMachine(CetMachine&&) noexcept;
    CetMachine& operator=(CetMachine&&) noexcept;

    // Memory configuration ------------------------------------------------
    void map_region(std::uint64_t base, std::uint64_t size, bool shadow);

    // Initial stack placement.  Both pointers must reference mapped memory and
    // be 8-byte aligned; the shadow stack top marks the empty-growth limit.
    void set_rsp(std::uint64_t rsp);
    void set_ssp(std::uint64_t ssp);

    // Protection-domain tracking used for INCSSP overreach detection.
    void set_domain(std::uint64_t domain) noexcept { domain_ = domain; }
    std::uint64_t domain() const noexcept { return domain_; }

    // Restore-token table -------------------------------------------------
    // Issues a valid, non-busy 64-bit token (target_ssp | mode_bit) at
    // token_address inside a shadow page and registers it in the token table.
    RestoreTokenRecord issue_restore_token(std::uint64_t token_address,
                                           std::uint64_t target_ssp,
                                           std::uint64_t owner_domain);
    bool is_token_registered(std::uint64_t token_address) const;

    // Instruction stream execution ---------------------------------------
    void step(const Instruction& insn);
    void run(const std::vector<Instruction>& program);

    // Non-throwing convenience wrapper.
    VerificationReport verify(const std::vector<Instruction>& program) noexcept;

    // Observability -------------------------------------------------------
    std::uint64_t rsp() const noexcept { return rsp_; }
    std::uint64_t ssp() const noexcept { return ssp_; }
    IbtState ibt_state() const noexcept { return ibt_state_; }
    std::uint64_t rip() const noexcept { return rip_; }
    void set_rip(std::uint64_t rip) noexcept { rip_ = rip; }
    std::size_t normal_depth() const noexcept { return normal_stack_.size(); }
    std::size_t shadow_depth() const noexcept { return shadow_stack_.size(); }

    std::uint64_t normal_top() const;
    std::uint64_t shadow_top() const;
    FrameKind shadow_top_kind() const;
    std::uint64_t shadow_top_owner() const;
    bool has_shadow_slot(std::uint64_t address) const;
    std::uint64_t read_shadow(std::uint64_t address) const;
    std::uint64_t read_normal(std::uint64_t address) const;

    // Restore-token bit layout (Intel SDM): bit0 busy, bit1 64-bit mode.
    static constexpr std::uint64_t kBusyBit = 0x1ULL;
    static constexpr std::uint64_t kMode64Bit = 0x2ULL;

private:
    enum class RegionKind { None, Normal, Shadow };

    RegionKind classify(std::uint64_t address) const noexcept;
    bool in_any_region(std::uint64_t address) const noexcept;
    ShadowSlot* slot_at(std::uint64_t address);
    const ShadowSlot* slot_at(std::uint64_t address) const;
    void fail_const(FaultCode code, const std::string& message) const;
    void write_shadow_slot(std::uint64_t address, std::uint64_t value,
                           FrameKind kind, std::uint64_t owner);
    void fail(FaultCode code, const std::string& message);
    void require_aligned8(std::uint64_t address, const char* what);
    void push_shadow(std::uint64_t value, FrameKind kind, std::uint64_t owner);
    std::uint64_t pop_shadow(FrameKind* kind, std::uint64_t* owner);
    void check_ibt_landing(const Instruction& insn);

    std::vector<Region> regions_;
    std::unordered_map<std::uint64_t, std::uint64_t> normal_memory_;
    std::unordered_map<std::uint64_t, std::unique_ptr<ShadowSlot>> shadow_memory_;
    std::unordered_map<std::uint64_t, RestoreTokenRecord> token_table_;
    std::unordered_set<std::uint64_t> active_shadow_stacks_;
    std::vector<std::uint64_t> normal_stack_;
    std::vector<std::uint64_t> shadow_stack_;

    std::uint64_t rsp_ = 0;
    std::uint64_t ssp_ = 0;
    std::uint64_t initial_ssp_ = 0;
    std::uint64_t domain_ = 0;
    std::uint64_t rip_ = 0;
    IbtState ibt_state_ = IbtState::Idle;
    bool have_prev_ssp_ = false;
    std::uint64_t prev_ssp_ = 0;
    std::uint64_t prev_domain_ = 0;
};

} // namespace cet

#endif // CET_CET_H
