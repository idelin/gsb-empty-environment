#include "cet/cet.h"

#include <sstream>
#include <utility>

namespace cet {

namespace {

constexpr std::uint64_t kAlignmentMask = 0x7ULL;

std::string hex(std::uint64_t value) {
    std::ostringstream stream;
    stream << "0x" << std::hex << value;
    return stream.str();
}

} // namespace

const char* fault_name(FaultCode code) noexcept {
    switch (code) {
    case FaultCode::None:
        return "None";
    case FaultCode::ReturnAddressMismatch:
        return "ReturnAddressMismatch";
    case FaultCode::ShadowStackUnderflow:
        return "ShadowStackUnderflow";
    case FaultCode::RegularStackUnderflow:
        return "RegularStackUnderflow";
    case FaultCode::ShadowStoreDenied:
        return "ShadowStoreDenied";
    case FaultCode::ShadowLoadDenied:
        return "ShadowLoadDenied";
    case FaultCode::ShadowAccessOutsidePage:
        return "ShadowAccessOutsidePage";
    case FaultCode::NormalAccessOutsideRegion:
        return "NormalAccessOutsideRegion";
    case FaultCode::StackOverflow:
        return "StackOverflow";
    case FaultCode::MisalignedToken:
        return "MisalignedToken";
    case FaultCode::TokenBusy:
        return "TokenBusy";
    case FaultCode::TokenModeBitClear:
        return "TokenModeBitClear";
    case FaultCode::TokenForged:
        return "TokenForged";
    case FaultCode::TokenReplay:
        return "TokenReplay";
    case FaultCode::NoPreviousShadowStack:
        return "NoPreviousShadowStack";
    case FaultCode::IncsspOverreach:
        return "IncsspOverreach";
    case FaultCode::MissingEndBranch:
        return "MissingEndBranch";
    case FaultCode::UnexpectedEndBranch:
        return "UnexpectedEndBranch";
    case FaultCode::UnmappedShadowAddress:
        return "UnmappedShadowAddress";
    case FaultCode::InvalidOperand:
        return "InvalidOperand";
    }
    return "Unknown";
}

Instruction make_call_direct() noexcept {
    return Instruction{Opcode::CallDirect, 0, 0, 0};
}

Instruction make_call_indirect(std::uint64_t target) noexcept {
    return Instruction{Opcode::CallIndirect, 0, 0, target};
}

Instruction make_ret() noexcept {
    return Instruction{Opcode::Ret, 0, 0, 0};
}

Instruction make_jmp_indirect(std::uint64_t target) noexcept {
    return Instruction{Opcode::JmpIndirect, 0, 0, target};
}

Instruction make_endbr64() noexcept {
    return Instruction{Opcode::EndBr64, 0, 0, 0};
}

Instruction make_incssp(std::uint64_t frames) noexcept {
    return Instruction{Opcode::Incssp, 0, frames, 0};
}

Instruction make_rstorssp(std::uint64_t token_address) noexcept {
    return Instruction{Opcode::Rstorssp, token_address, 0, 0};
}

Instruction make_saveprev_ssp() noexcept {
    return Instruction{Opcode::SavePrevSsp, 0, 0, 0};
}

Instruction make_rdssp(std::uint64_t save_address) noexcept {
    return Instruction{Opcode::Rdssp, save_address, 0, 0};
}

Instruction make_wrss(std::uint64_t shadow_address,
                      std::uint64_t value) noexcept {
    return Instruction{Opcode::Wrss, shadow_address, value, 0};
}

Instruction make_store(std::uint64_t address,
                       std::uint64_t value) noexcept {
    return Instruction{Opcode::Store, address, value, 0};
}

Instruction make_load(std::uint64_t address) noexcept {
    return Instruction{Opcode::Load, address, 0, 0};
}

Instruction make_set_rsp(std::uint64_t new_rsp) noexcept {
    return Instruction{Opcode::SetRsp, new_rsp, 0, 0};
}

ControlProtectionFault::ControlProtectionFault(FaultCode code,
                                               const std::string& message)
    : std::runtime_error(message), code_(code) {}

CetMachine::CetMachine() = default;
CetMachine::~CetMachine() = default;

CetMachine::CetMachine(CetMachine&& other) noexcept
    : regions_(std::move(other.regions_)),
      normal_memory_(std::move(other.normal_memory_)),
      shadow_memory_(std::move(other.shadow_memory_)),
      token_table_(std::move(other.token_table_)),
      active_shadow_stacks_(std::move(other.active_shadow_stacks_)),
      normal_stack_(std::move(other.normal_stack_)),
      shadow_stack_(std::move(other.shadow_stack_)),
      rsp_(other.rsp_),
      ssp_(other.ssp_),
      initial_ssp_(other.initial_ssp_),
      domain_(other.domain_),
      rip_(other.rip_),
      ibt_state_(other.ibt_state_),
      have_prev_ssp_(other.have_prev_ssp_),
      prev_ssp_(other.prev_ssp_),
      prev_domain_(other.prev_domain_) {}

CetMachine& CetMachine::operator=(CetMachine&& other) noexcept {
    if (this != &other) {
        regions_ = std::move(other.regions_);
        normal_memory_ = std::move(other.normal_memory_);
        shadow_memory_ = std::move(other.shadow_memory_);
        token_table_ = std::move(other.token_table_);
        active_shadow_stacks_ = std::move(other.active_shadow_stacks_);
        normal_stack_ = std::move(other.normal_stack_);
        shadow_stack_ = std::move(other.shadow_stack_);
        rsp_ = other.rsp_;
        ssp_ = other.ssp_;
        initial_ssp_ = other.initial_ssp_;
        domain_ = other.domain_;
        rip_ = other.rip_;
        ibt_state_ = other.ibt_state_;
        have_prev_ssp_ = other.have_prev_ssp_;
        prev_ssp_ = other.prev_ssp_;
        prev_domain_ = other.prev_domain_;
    }
    return *this;
}

void CetMachine::fail(FaultCode code, const std::string& message) {
    throw ControlProtectionFault(code, message);
}

void CetMachine::fail_const(FaultCode code, const std::string& message) const {
    throw ControlProtectionFault(code, message);
}

void CetMachine::require_aligned8(std::uint64_t address, const char* what) {
    if ((address & kAlignmentMask) != 0U) {
        fail(FaultCode::MisalignedToken,
             std::string(what) + " address " + hex(address) +
                 " is not 8-byte aligned");
    }
}

CetMachine::RegionKind CetMachine::classify(
    std::uint64_t address) const noexcept {
    for (const Region& region : regions_) {
        if (address >= region.base && address < region.base + region.size) {
            return region.shadow ? RegionKind::Shadow : RegionKind::Normal;
        }
    }
    return RegionKind::None;
}

ShadowSlot* CetMachine::slot_at(std::uint64_t address) {
    auto found = shadow_memory_.find(address);
    return found == shadow_memory_.end() ? nullptr : found->second.get();
}

const ShadowSlot* CetMachine::slot_at(std::uint64_t address) const {
    auto found = shadow_memory_.find(address);
    return found == shadow_memory_.end() ? nullptr : found->second.get();
}

void CetMachine::write_shadow_slot(std::uint64_t address,
                                   std::uint64_t value,
                                   FrameKind kind,
                                   std::uint64_t owner) {
    auto found = shadow_memory_.find(address);
    if (found == shadow_memory_.end()) {
        shadow_memory_.emplace(
            address, std::make_unique<ShadowSlot>(value, kind, owner));
    } else {
        found->second->payload.store(value, std::memory_order_release);
        found->second->frame_kind = kind;
        found->second->owner_domain = owner;
    }
}

void CetMachine::map_region(std::uint64_t base,
                            std::uint64_t size,
                            bool shadow) {
    if (size == 0U) {
        fail(FaultCode::InvalidOperand, "mapped region size must be non-zero");
    }
    const std::uint64_t end = base + size;
    if (end < base) {
        fail(FaultCode::InvalidOperand, "region address range overflows");
    }
    for (const Region& existing : regions_) {
        const std::uint64_t existing_end = existing.base + existing.size;
        const bool overlap = base < existing_end && existing.base < end;
        if (overlap) {
            fail(FaultCode::InvalidOperand,
                 "region starting at " + hex(base) +
                     " overlaps an existing region");
        }
    }
    regions_.push_back(Region{base, size, shadow});
}

void CetMachine::set_rsp(std::uint64_t rsp) {
    require_aligned8(rsp, "RSP");
    if (classify(rsp) != RegionKind::Normal) {
        fail(FaultCode::NormalAccessOutsideRegion,
             "RSP " + hex(rsp) + " does not point into normal memory");
    }
    rsp_ = rsp;
}

void CetMachine::set_ssp(std::uint64_t ssp) {
    require_aligned8(ssp, "SSP");
    if (classify(ssp) != RegionKind::Shadow) {
        fail(FaultCode::UnmappedShadowAddress,
             "SSP " + hex(ssp) + " does not point into a shadow page");
    }
    ssp_ = ssp;
    initial_ssp_ = ssp;
}

RestoreTokenRecord CetMachine::issue_restore_token(
    std::uint64_t token_address,
    std::uint64_t target_ssp,
    std::uint64_t owner_domain) {
    require_aligned8(token_address, "restore-token");
    require_aligned8(target_ssp, "target SSP");
    if (classify(token_address) != RegionKind::Shadow) {
        fail(FaultCode::UnmappedShadowAddress,
             "restore-token slot " + hex(token_address) +
                 " is outside a shadow page");
    }
    if (classify(target_ssp) != RegionKind::Shadow) {
        fail(FaultCode::UnmappedShadowAddress,
             "target SSP " + hex(target_ssp) +
                 " is outside a shadow page");
    }
    if (token_table_.find(token_address) != token_table_.end()) {
        fail(FaultCode::TokenBusy,
             "a restore token already occupies " + hex(token_address));
    }
    if (slot_at(token_address) != nullptr) {
        fail(FaultCode::StackOverflow,
             "shadow slot " + hex(token_address) + " already contains data");
    }

    // A fresh token carries the 64-bit mode flag (bit1) and a clear busy flag.
    const std::uint64_t token_value =
        (target_ssp & ~(kBusyBit | kMode64Bit)) | kMode64Bit;
    RestoreTokenRecord record{token_address, target_ssp, owner_domain, false};
    token_table_.emplace(token_address, record);
    write_shadow_slot(token_address, token_value, FrameKind::RestoreToken,
                      owner_domain);
    return record;
}

bool CetMachine::is_token_registered(std::uint64_t token_address) const {
    return token_table_.find(token_address) != token_table_.end();
}

void CetMachine::push_shadow(std::uint64_t value,
                             FrameKind kind,
                             std::uint64_t owner) {
    if (ssp_ < 8U) {
        fail(FaultCode::StackOverflow,
             "shadow stack cannot grow below address 0");
    }
    const std::uint64_t next = ssp_ - 8U;
    if (classify(next) != RegionKind::Shadow) {
        fail(FaultCode::ShadowAccessOutsidePage,
             "shadow push at " + hex(next) + " leaves the shadow page");
    }
    write_shadow_slot(next, value, kind, owner);
    shadow_stack_.push_back(next);
    ssp_ = next;
}

std::uint64_t CetMachine::pop_shadow(FrameKind* kind,
                                     std::uint64_t* owner) {
    if (shadow_stack_.empty()) {
        fail(FaultCode::ShadowStackUnderflow,
             "shadow stack has no frame to pop at SSP " + hex(ssp_));
    }
    const std::uint64_t address = shadow_stack_.back();
    const ShadowSlot* slot = slot_at(address);
    if (slot == nullptr) {
        fail(FaultCode::ShadowStackUnderflow,
             "shadow stack accounting lost slot " + hex(address));
    }
    const std::uint64_t value =
        slot->payload.load(std::memory_order_acquire);
    if (kind != nullptr) {
        *kind = slot->frame_kind;
    }
    if (owner != nullptr) {
        *owner = slot->owner_domain;
    }
    shadow_stack_.pop_back();
    ssp_ = address + 8U;
    return value;
}

void CetMachine::check_ibt_landing(const Instruction& insn) {
    if (ibt_state_ != IbtState::WaitForEndbranch) {
        return;
    }
    if (insn.opcode != Opcode::EndBr64) {
        fail(FaultCode::MissingEndBranch,
             "indirect branch landed on a non-ENDBR64 instruction (opcode " +
                 std::to_string(static_cast<unsigned>(insn.opcode)) + ")");
    }
    ibt_state_ = IbtState::Idle;
}

void CetMachine::step(const Instruction& insn) {
    // The IBT landing-pad gate applies to every executed instruction.
    check_ibt_landing(insn);

    switch (insn.opcode) {
    case Opcode::CallDirect:
    case Opcode::CallIndirect: {
        // Symbolic return address: RIP of the call plus a fixed 8-byte stride.
        const std::uint64_t return_address = rip_ + 8U;
        if (rsp_ < 8U || classify(rsp_ - 8U) != RegionKind::Normal) {
            fail(FaultCode::StackOverflow,
                 "normal stack push would leave the normal region");
        }
        rsp_ -= 8U;
        normal_memory_[rsp_] = return_address;
        normal_stack_.push_back(rsp_);
        push_shadow(return_address, FrameKind::ReturnAddress, domain_);
        rip_ = return_address;
        // Only indirect control transfer arms the IBT state machine.
        if (insn.opcode == Opcode::CallIndirect) {
            ibt_state_ = IbtState::WaitForEndbranch;
        }
        break;
    }
    case Opcode::Ret: {
        if (normal_stack_.empty()) {
            fail(FaultCode::RegularStackUnderflow,
                 "RET executed with an empty conventional stack");
        }
        FrameKind shadow_kind = FrameKind::ReturnAddress;
        std::uint64_t shadow_owner = 0;
        const std::uint64_t shadow_return =
            pop_shadow(&shadow_kind, &shadow_owner);
        const std::uint64_t normal_address = normal_stack_.back();
        normal_stack_.pop_back();
        const std::uint64_t normal_return = normal_memory_[normal_address];
        rsp_ = normal_address + 8U;
        if (shadow_kind != FrameKind::ReturnAddress) {
            fail(FaultCode::ReturnAddressMismatch,
                 "RET reached shadow slot of kind " +
                     std::to_string(static_cast<unsigned>(shadow_kind)) +
                     " instead of a call frame");
        }
        if (normal_return != shadow_return) {
            fail(FaultCode::ReturnAddressMismatch,
                 "RET mismatch: normal stack " + hex(normal_return) +
                     " vs shadow stack " + hex(shadow_return));
        }
        rip_ = normal_return;
        break;
    }
    case Opcode::JmpIndirect:
        rip_ = insn.target;
        ibt_state_ = IbtState::WaitForEndbranch;
        break;
    case Opcode::EndBr64:
        // Reaching here means the pending wait was consumed above; an ENDBR64
        // in IDLE is architecturally a NOP, not a fault.
        break;
    case Opcode::Incssp: {
        if (insn.count == 0U) {
            fail(FaultCode::InvalidOperand, "INCSSP count must be non-zero");
        }
        for (std::uint64_t index = 0; index < insn.count; ++index) {
            if (shadow_stack_.empty()) {
                fail(FaultCode::ShadowStackUnderflow,
                     "INCSSP ran past the bottom of the shadow stack");
            }
            const std::uint64_t top_address = shadow_stack_.back();
            const ShadowSlot* top = slot_at(top_address);
            if (top == nullptr) {
                fail(FaultCode::ShadowStackUnderflow,
                     "INCSSP found an unrecorded shadow slot");
            }
            // A return frame may only be unwound by its owning protection
            // domain; token/boundary slots are never skippable via INCSSP.
            if (top->frame_kind != FrameKind::ReturnAddress ||
                top->owner_domain != domain_) {
                fail(FaultCode::IncsspOverreach,
                     "INCSSP would skip protected frame kind " +
                         std::to_string(
                             static_cast<unsigned>(top->frame_kind)) +
                         " owned by domain " +
                         std::to_string(top->owner_domain));
            }
            shadow_stack_.pop_back();
            ssp_ = top_address + 8U;
        }
        break;
    }
    case Opcode::Rstorssp: {
        const std::uint64_t token_address = insn.address;
        require_aligned8(token_address, "RSTORSSP token");
        if (classify(token_address) != RegionKind::Shadow) {
            fail(FaultCode::UnmappedShadowAddress,
                 "RSTORSSP token " + hex(token_address) +
                     " is outside a shadow page");
        }
        ShadowSlot* token_slot = slot_at(token_address);
        if (token_slot == nullptr) {
            fail(FaultCode::TokenForged,
                 "RSTORSSP token slot " + hex(token_address) +
                     " contains no registered restore token");
        }

        auto table_entry = token_table_.find(token_address);
        if (table_entry == token_table_.end()) {
            fail(FaultCode::TokenForged,
                 "token at " + hex(token_address) +
                     " is absent from the restore-token table");
        }

        // Atomic acquisition: compare the current word with a non-busy,
        // 64-bit-mode token and set busy in one locked step.
        const std::uint64_t expected =
            (table_entry->second.target_ssp &
             ~(kBusyBit | kMode64Bit)) |
            kMode64Bit;
        std::uint64_t observed =
            token_slot->payload.load(std::memory_order_acquire);
        if ((observed & kMode64Bit) == 0U) {
            fail(FaultCode::TokenModeBitClear,
                 "restore token " + hex(token_address) +
                     " has the 64-bit mode bit clear");
        }
        if ((observed & kBusyBit) != 0U) {
            fail(FaultCode::TokenBusy,
                 "restore token " + hex(token_address) +
                     " is already busy (replay / double activation)");
        }
        if (observed != expected) {
            fail(FaultCode::TokenForged,
                 "restore token " + hex(token_address) +
                     " value " + hex(observed) +
                     " does not match the registered token " +
                     hex(expected));
        }
        const std::uint64_t desired = observed | kBusyBit;
        if (!token_slot->payload.compare_exchange_strong(
                observed, desired,
                std::memory_order_acq_rel,
                std::memory_order_acquire)) {
            fail(FaultCode::TokenBusy,
                 "atomic RSTORSSP acquisition lost the race on " +
                     hex(token_address));
        }

        const std::uint64_t new_ssp =
            observed & ~(kBusyBit | kMode64Bit);
        require_aligned8(new_ssp, "RSTORSSP target SSP");
        if (classify(new_ssp) != RegionKind::Shadow) {
            fail(FaultCode::UnmappedShadowAddress,
                 "RSTORSSP target SSP " + hex(new_ssp) +
                     " is outside a shadow page");
        }
        if (active_shadow_stacks_.count(new_ssp) != 0U) {
            fail(FaultCode::TokenReplay,
                 "shadow stack at SSP " + hex(new_ssp) +
                     " is already active in another coroutine");
        }

        // Latch the outgoing stack for a subsequent SAVEPREVSSP, then switch.
        have_prev_ssp_ = true;
        prev_ssp_ = ssp_;
        prev_domain_ = domain_;
        active_shadow_stacks_.insert(new_ssp);
        ssp_ = new_ssp;
        domain_ = table_entry->second.owner_domain;
        table_entry->second.busy = true;

        // Rebuild the logical shadow stack from the occupied slots at or
        // below the new SSP, bounded by the containing shadow page.
        shadow_stack_.clear();
        std::uint64_t page_base = 0;
        std::uint64_t page_end = 0;
        for (const Region& region : regions_) {
            if (region.shadow && new_ssp >= region.base &&
                new_ssp < region.base + region.size) {
                page_base = region.base;
                page_end = region.base + region.size;
                break;
            }
        }
        if (page_end == 0U) {
            fail(FaultCode::UnmappedShadowAddress,
                 "new SSP " + hex(new_ssp) + " has no containing shadow page");
        }
        shadow_stack_.clear();
        for (const auto& entry : shadow_memory_) {
            const std::uint64_t address = entry.first;
            // Only slots strictly below the new SSP hold live frames; slots at
            // or above SSP are spare space or switch tokens outside the stack.
            const ShadowSlot* candidate_slot = entry.second.get();
            if (candidate_slot != nullptr &&
                candidate_slot->frame_kind == FrameKind::ReturnAddress &&
                address >= page_base && address < page_end &&
                address < new_ssp) {
                shadow_stack_.push_back(address);
            }
        }
        // Descending addresses: the lowest address is the logical top.
        for (std::size_t i = 1U; i < shadow_stack_.size(); ++i) {
            const std::uint64_t key = shadow_stack_[i];
            std::size_t j = i;
            while (j > 0U && shadow_stack_[j - 1U] < key) {
                shadow_stack_[j] = shadow_stack_[j - 1U];
                --j;
            }
            shadow_stack_[j] = key;
        }
        break;
    }
    case Opcode::SavePrevSsp: {
        if (!have_prev_ssp_) {
            fail(FaultCode::NoPreviousShadowStack,
                 "SAVEPREVSSP executed without a preceding RSTORSSP");
        }
        // The previous-SSP token is busy (bit0) and 64-bit (bit1).
        const std::uint64_t token_value =
            (prev_ssp_ & ~(kBusyBit | kMode64Bit)) | kBusyBit | kMode64Bit;
        push_shadow(token_value, FrameKind::PrevSspToken, prev_domain_);
        break;
    }
    case Opcode::Rdssp: {
        if (classify(insn.address) != RegionKind::Normal) {
            fail(FaultCode::NormalAccessOutsideRegion,
                 "RDSSP destination " + hex(insn.address) +
                     " must be normal memory (jmp_buf slot)");
        }
        normal_memory_[insn.address] = ssp_;
        break;
    }
    case Opcode::Wrss: {
        // WRSS is the single sanctioned data writer on a shadow page.
        require_aligned8(insn.address, "WRSS address");
        if (classify(insn.address) != RegionKind::Shadow) {
            fail(FaultCode::ShadowAccessOutsidePage,
                 "WRSS target " + hex(insn.address) +
                     " is not inside a shadow page");
        }
        const FrameKind kind =
            is_token_registered(insn.address)
                ? FrameKind::RestoreToken
                : FrameKind::ReturnAddress;
        write_shadow_slot(insn.address, insn.count, kind, domain_);
        break;
    }
    case Opcode::Store: {
        const RegionKind region = classify(insn.address);
        if (region == RegionKind::Shadow) {
            fail(FaultCode::ShadowStoreDenied,
                 "plain STORE to shadow address " + hex(insn.address) +
                     " is forbidden; use WRSS / CET shadow ops");
        }
        if (region == RegionKind::None) {
            fail(FaultCode::NormalAccessOutsideRegion,
                 "STORE to unmapped address " + hex(insn.address));
        }
        normal_memory_[insn.address] = insn.count;
        break;
    }
    case Opcode::Load: {
        const RegionKind region = classify(insn.address);
        if (region == RegionKind::Shadow) {
            fail(FaultCode::ShadowLoadDenied,
                 "plain LOAD from shadow address " + hex(insn.address) +
                     " is forbidden");
        }
        if (region == RegionKind::None) {
            fail(FaultCode::NormalAccessOutsideRegion,
                 "LOAD from unmapped address " + hex(insn.address));
        }
        break;
    }
    case Opcode::SetRsp: {
        require_aligned8(insn.address, "RSP");
        if (classify(insn.address) != RegionKind::Normal) {
            fail(FaultCode::NormalAccessOutsideRegion,
                 "longjmp RSP load target " + hex(insn.address) +
                     " is outside normal memory");
        }
        rsp_ = insn.address;
        break;
    }
    }
}

void CetMachine::run(const std::vector<Instruction>& program) {
    for (const Instruction& insn : program) {
        step(insn);
    }
}

VerificationReport CetMachine::verify(
    const std::vector<Instruction>& program) noexcept {
    VerificationReport report;
    try {
        for (std::size_t index = 0; index < program.size(); ++index) {
            step(program[index]);
            report.instruction_index = index + 1U;
        }
    } catch (const ControlProtectionFault& fault) {
        report.ok = false;
        report.fault = fault.code();
        report.message = fault.what();
        report.rip = rip_;
    } catch (const std::exception& ex) {
        report.ok = false;
        report.fault = FaultCode::InvalidOperand;
        report.message = ex.what();
        report.rip = rip_;
    }
    return report;
}

std::uint64_t CetMachine::normal_top() const {
    if (normal_stack_.empty()) {
        fail_const(FaultCode::RegularStackUnderflow,
             "conventional stack has no top frame");
    }
    return normal_memory_.at(normal_stack_.back());
}

std::uint64_t CetMachine::shadow_top() const {
    if (shadow_stack_.empty()) {
        fail_const(FaultCode::ShadowStackUnderflow,
             "shadow stack has no top frame");
    }
    const ShadowSlot* slot = slot_at(shadow_stack_.back());
    if (slot == nullptr) {
        fail_const(FaultCode::ShadowStackUnderflow, "shadow top slot vanished");
    }
    return slot->payload.load(std::memory_order_acquire);
}

FrameKind CetMachine::shadow_top_kind() const {
    if (shadow_stack_.empty()) {
        fail_const(FaultCode::ShadowStackUnderflow,
             "shadow stack has no top frame");
    }
    const ShadowSlot* slot = slot_at(shadow_stack_.back());
    if (slot == nullptr) {
        fail_const(FaultCode::ShadowStackUnderflow, "shadow top slot vanished");
    }
    return slot->frame_kind;
}

std::uint64_t CetMachine::shadow_top_owner() const {
    if (shadow_stack_.empty()) {
        fail_const(FaultCode::ShadowStackUnderflow,
             "shadow stack has no top frame");
    }
    const ShadowSlot* slot = slot_at(shadow_stack_.back());
    if (slot == nullptr) {
        fail_const(FaultCode::ShadowStackUnderflow, "shadow top slot vanished");
    }
    return slot->owner_domain;
}

bool CetMachine::has_shadow_slot(std::uint64_t address) const {
    return slot_at(address) != nullptr;
}

std::uint64_t CetMachine::read_shadow(std::uint64_t address) const {
    const ShadowSlot* slot = slot_at(address);
    if (slot == nullptr) {
        fail_const(FaultCode::UnmappedShadowAddress,
             "no shadow slot at " + hex(address));
    }
    return slot->payload.load(std::memory_order_acquire);
}

std::uint64_t CetMachine::read_normal(std::uint64_t address) const {
    auto found = normal_memory_.find(address);
    if (found == normal_memory_.end()) {
        fail_const(FaultCode::NormalAccessOutsideRegion,
             "no normal-memory value at " + hex(address));
    }
    return found->second;
}

} // namespace cet
