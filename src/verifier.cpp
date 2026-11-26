#include "ebpf/verifier.hpp"

#include <algorithm>
#include <array>
#include <cstdint>
#include <deque>
#include <functional>
#include <limits>
#include <optional>
#include <string>
#include <utility>
#include <vector>

namespace ebpf {
namespace {

constexpr std::uint8_t kClassMask = 0x07;
constexpr std::uint8_t kOpMask = 0xf0;
constexpr std::uint8_t kSourceX = 0x08;
constexpr std::uint8_t kSizeMask = 0x18;
constexpr std::uint8_t kModeMask = 0xe0;

constexpr std::uint8_t kClassLd = 0x00;
constexpr std::uint8_t kClassLdx = 0x01;
constexpr std::uint8_t kClassSt = 0x02;
constexpr std::uint8_t kClassStx = 0x03;
constexpr std::uint8_t kClassAlu = 0x04;
constexpr std::uint8_t kClassJmp = 0x05;
constexpr std::uint8_t kClassJmp32 = 0x06;
constexpr std::uint8_t kClassAlu64 = 0x07;

constexpr std::uint8_t kModeMem = 0x60;
constexpr std::uint8_t kModeMemSx = 0x80;
constexpr std::uint8_t kModeAtomic = 0xc0;

constexpr std::uint8_t kJmpJa = 0x00;
constexpr std::uint8_t kJmpJset = 0x40;
constexpr std::uint8_t kJmpCall = 0x80;
constexpr std::uint8_t kJmpExit = 0x90;

struct VMState {
    std::array<RegState, kRegisterCount> regs{};
};

[[nodiscard]] bool same_region(const Pointer& lhs,
                               const Pointer& rhs) noexcept {
    return lhs.kind == rhs.kind && lhs.region_id == rhs.region_id &&
           lhs.region_size == rhs.region_size;
}

[[nodiscard]] VMState join_states(const VMState& lhs, const VMState& rhs) {
    VMState out;
    for (std::size_t reg = 0; reg < kRegisterCount; ++reg) {
        const auto& left = lhs.regs[reg];
        const auto& right = rhs.regs[reg];
        if (left.pointer.has_value() && right.pointer.has_value() &&
            same_region(*left.pointer, *right.pointer)) {
            Pointer pointer = *left.pointer;
            pointer.offset_min =
                std::min(left.pointer->offset_min, right.pointer->offset_min);
            pointer.offset_max =
                std::max(left.pointer->offset_max, right.pointer->offset_max);
            out.regs[reg].pointer = pointer;
            out.regs[reg].scalar = Scalar::full();
        } else if (!left.pointer.has_value() && !right.pointer.has_value()) {
            out.regs[reg].scalar = join_scalar(left.scalar, right.scalar);
        } else {
            out.regs[reg].scalar = Scalar::full();
        }
    }
    return out;
}

[[nodiscard]] std::optional<AluOp> decode_alu(std::uint8_t opcode) noexcept {
    switch (opcode & kOpMask) {
        case 0x00:
            return AluOp::Add;
        case 0x10:
            return AluOp::Sub;
        case 0x20:
            return AluOp::Mul;
        case 0x30:
            return AluOp::Div;
        case 0x40:
            return AluOp::Or;
        case 0x50:
            return AluOp::And;
        case 0x60:
            return AluOp::Lsh;
        case 0x70:
            return AluOp::Rsh;
        case 0x80:
            return AluOp::Neg;
        case 0x90:
            return AluOp::Mod;
        case 0xa0:
            return AluOp::Xor;
        case 0xb0:
            return AluOp::Mov;
        case 0xc0:
            return AluOp::Arsh;
        default:
            return std::nullopt;
    }
}

[[nodiscard]] std::optional<CompareOp> decode_compare(
    std::uint8_t opcode) noexcept {
    switch (opcode & kOpMask) {
        case 0x10:
            return CompareOp::Eq;
        case 0x20:
            return CompareOp::Gt;
        case 0x30:
            return CompareOp::Ge;
        case 0x50:
            return CompareOp::Ne;
        case 0x60:
            return CompareOp::Sgt;
        case 0x70:
            return CompareOp::Sge;
        case 0xa0:
            return CompareOp::Lt;
        case 0xb0:
            return CompareOp::Le;
        case 0xc0:
            return CompareOp::Slt;
        case 0xd0:
            return CompareOp::Sle;
        default:
            return std::nullopt;
    }
}

[[nodiscard]] CompareOp swap_compare(CompareOp op) noexcept {
    switch (op) {
        case CompareOp::Lt:
            return CompareOp::Gt;
        case CompareOp::Le:
            return CompareOp::Ge;
        case CompareOp::Gt:
            return CompareOp::Lt;
        case CompareOp::Ge:
            return CompareOp::Le;
        case CompareOp::Slt:
            return CompareOp::Sgt;
        case CompareOp::Sle:
            return CompareOp::Sge;
        case CompareOp::Sgt:
            return CompareOp::Slt;
        case CompareOp::Sge:
            return CompareOp::Sle;
        default:
            return op;
    }
}

[[nodiscard]] std::uint64_t access_size(std::uint8_t opcode) noexcept {
    switch (opcode & kSizeMask) {
        case 0x00:
            return 4;
        case 0x08:
            return 2;
        case 0x10:
            return 1;
        case 0x18:
            return 8;
        default:
            return 0;
    }
}

[[nodiscard]] Scalar loaded_scalar(std::uint64_t size, bool sign_extend) {
    if (size == 8 && !sign_extend) {
        return Scalar::full();
    }
    if (sign_extend) {
        if (size == 8) {
            return Scalar::full();
        }
        const unsigned bits = static_cast<unsigned>(size * 8);
        const std::int64_t half = static_cast<std::int64_t>(
            std::uint64_t{1} << (bits - 1));
        Scalar out;
        out.smin64 = -half;
        out.smax64 = half - 1;
        if (bits == 32) {
            out.s32_min = kI32Min;
            out.s32_max = kI32Max;
            out.u32_min = 0;
            out.u32_max = kU32Max;
        } else {
            out.s32_min =
                -static_cast<std::int32_t>(std::uint32_t{1} << (bits - 1));
            out.s32_max =
                static_cast<std::int32_t>((std::uint32_t{1} << (bits - 1)) - 1);
        }
        out.normalize();
        return out;
    }

    const unsigned bits = static_cast<unsigned>(size * 8);
    const std::uint64_t mask =
        bits == 64 ? kU64Max : ((std::uint64_t{1} << bits) - 1);
    Scalar out;
    out.tnum = Tnum::make(0, mask);
    out.umin64 = 0;
    out.umax64 = mask;
    out.smin64 = 0;
    out.smax64 = static_cast<std::int64_t>(mask);
    if (bits <= 32) {
        out.u32_min = 0;
        out.u32_max = static_cast<std::uint32_t>(mask);
        if (bits < 32) {
            out.s32_min = 0;
            out.s32_max = static_cast<std::int32_t>(mask);
        }
    }
    out.normalize();
    return out;
}

[[nodiscard]] bool translate_pointer(Pointer& pointer, __int128 delta_lo,
                                     __int128 delta_hi) noexcept {
    const __int128 lo = static_cast<__int128>(pointer.offset_min) + delta_lo;
    const __int128 hi = static_cast<__int128>(pointer.offset_max) + delta_hi;
    if (lo < kI64Min || hi > kI64Max) {
        return false;
    }
    pointer.offset_min = static_cast<std::int64_t>(lo);
    pointer.offset_max = static_cast<std::int64_t>(hi);
    return true;
}

[[nodiscard]] bool self_comparison_truth(CompareOp op) noexcept {
    return op == CompareOp::Eq || op == CompareOp::Le ||
           op == CompareOp::Ge || op == CompareOp::Sle ||
           op == CompareOp::Sge;
}

[[nodiscard]] bool is_pointer_mode(std::uint8_t opcode) noexcept {
    const auto mode = static_cast<std::uint8_t>(opcode & kModeMask);
    return mode == kModeMem || mode == kModeMemSx || mode == kModeAtomic;
}

}  // namespace

MemoryAccess check_memory_access(const RegState& pointer_reg,
                                 std::int64_t insn_offset,
                                 std::uint64_t size) noexcept {
    MemoryAccess result;
    if (!pointer_reg.pointer.has_value()) {
        result.reason = "register is not a pointer";
        return result;
    }
    const Pointer& pointer = *pointer_reg.pointer;
    const __int128 min_value =
        static_cast<__int128>(pointer.offset_min) + insn_offset;
    const __int128 max_value =
        static_cast<__int128>(pointer.offset_max) + insn_offset;
    if (min_value < kI64Min || max_value > kI64Max) {
        result.reason = "pointer offset arithmetic overflow";
        return result;
    }
    result.effective_min = static_cast<std::int64_t>(min_value);
    result.effective_max = static_cast<std::int64_t>(max_value);
    if (min_value < 0 || max_value < 0) {
        result.reason = "pointer offset is negative";
        return result;
    }
    const __int128 end = max_value + static_cast<__int128>(size);
    if (end > static_cast<__int128>(pointer.region_size)) {
        result.reason = "access exceeds region boundary";
        return result;
    }
    result.safe = true;
    return result;
}

VerificationResult verify_dag(const Program& program) {
    VerificationResult result;
    const std::size_t count = program.instructions.size();
    result.reachable.assign(count, false);
    if (count == 0) {
        result.error = "empty program";
        return result;
    }
    if (program.packet_length >
            static_cast<std::uint64_t>(kI64Max) ||
        program.stack_size > static_cast<std::uint64_t>(kI64Max)) {
        result.error = "packet or stack size exceeds signed offset range";
        return result;
    }

    std::vector<bool> starts(count, false);
    starts[0] = true;
    for (std::size_t pc = 0; pc < count;) {
        const Instruction& insn = program.instructions[pc];
        const auto cls = static_cast<std::uint8_t>(insn.opcode & kClassMask);
        if (cls == kClassLd && insn.opcode == 0x18) {
            if (pc + 1 >= count) {
                result.pc = pc;
                result.error = "truncated BPF_LD_IMM64";
                return result;
            }
            pc += 2;
        } else {
            ++pc;
        }
        if (pc < count) {
            starts[pc] = true;
        }
    }

    std::vector<std::vector<std::size_t>> successors(count);

    auto valid_target = [&](std::int64_t target) {
        return target >= 0 && static_cast<std::size_t>(target) < count &&
               starts[static_cast<std::size_t>(target)];
    };

    for (std::size_t pc = 0; pc < count; ++pc) {
        if (!starts[pc]) {
            continue;
        }
        const Instruction& insn = program.instructions[pc];
        const auto cls = static_cast<std::uint8_t>(insn.opcode & kClassMask);
        if (insn.dst() >= kRegisterCount || insn.src() >= kRegisterCount) {
            result.pc = pc;
            result.error = "register index out of range";
            return result;
        }
        if (cls == kClassJmp32 &&
            ((insn.opcode & kOpMask) == kJmpJa ||
             (insn.opcode & kOpMask) == kJmpCall ||
             (insn.opcode & kOpMask) == kJmpExit)) {
            result.pc = pc;
            result.error = "invalid JMP32 instruction";
            return result;
        }
        if (cls == kClassLd && insn.opcode == 0x18) {
            if (pc + 2 < count) {
                successors[pc].push_back(pc + 2);
            } else if (pc + 2 == count) {
                result.pc = pc;
                result.error = "program falls off the end";
                return result;
            }
            continue;
        }

        const bool is_exit = cls == kClassJmp && (insn.opcode & kOpMask) == kJmpExit;
        if (is_exit) {
            continue;
        }
        const bool is_ja = cls == kClassJmp && (insn.opcode & kOpMask) == kJmpJa;
        const bool is_cond =
            (cls == kClassJmp || cls == kClassJmp32) &&
            (insn.opcode & kOpMask) != kJmpJa &&
            (insn.opcode & kOpMask) != kJmpExit &&
            (insn.opcode & kOpMask) != kJmpCall;

        if (is_ja || is_cond) {
            const std::int64_t target =
                static_cast<std::int64_t>(pc) + 1 + insn.off;
            if (!valid_target(target)) {
                result.pc = pc;
                result.error = "jump target is not an instruction start";
                return result;
            }
            successors[pc].push_back(static_cast<std::size_t>(target));
        }
        if (!is_ja) {
            if (pc + 1 >= count) {
                result.pc = pc;
                result.error = "program falls off the end";
                return result;
            }
            if (!starts[pc + 1]) {
                result.pc = pc;
                result.error = "fall-through enters BPF_LD_IMM64 payload";
                return result;
            }
            successors[pc].push_back(pc + 1);
        }
    }

    std::vector<int> color(count, 0);
    std::function<bool(std::size_t)> visit = [&](std::size_t pc) {
        color[pc] = 1;
        for (std::size_t next : successors[pc]) {
            if (color[next] == 1) {
                return false;
            }
            if (color[next] == 0 && !visit(next)) {
                return false;
            }
        }
        color[pc] = 2;
        return true;
    };
    for (std::size_t pc = 0; pc < count; ++pc) {
        if (starts[pc] && color[pc] == 0 && !visit(pc)) {
            result.error = "CFG contains a cycle; verify_dag requires a DAG";
            return result;
        }
    }

    VMState initial;
    for (auto& reg : initial.regs) {
        reg.scalar = Scalar::full();
    }
    if (program.use_custom_initial_regs) {
        initial.regs = program.initial_regs;
        for (auto& reg : initial.regs) {
            if (!reg.pointer.has_value()) {
                reg.scalar.normalize();
            }
        }
    } else {
        Pointer start;
        start.kind = PointerKind::Packet;
        start.region_id = 0;
        start.region_size = program.packet_length;
        start.offset_min = 0;
        start.offset_max = 0;
        initial.regs[1].pointer = start;
        initial.regs[1].scalar = Scalar::full();

        Pointer end = start;
        end.offset_min = static_cast<std::int64_t>(program.packet_length);
        end.offset_max = static_cast<std::int64_t>(program.packet_length);
        initial.regs[2].pointer = end;
        initial.regs[2].scalar = Scalar::full();

        Pointer stack;
        stack.kind = PointerKind::Stack;
        stack.region_id = 0;
        stack.region_size = program.stack_size;
        stack.offset_min = static_cast<std::int64_t>(program.stack_size);
        stack.offset_max = static_cast<std::int64_t>(program.stack_size);
        initial.regs[10].pointer = stack;
        initial.regs[10].scalar = Scalar::full();
    }

    std::vector<VMState> incoming(count);
    std::vector<bool> has_incoming(count, false);
    std::vector<std::size_t> indegree(count, 0);
    for (const auto& edges : successors) {
        for (std::size_t next : edges) {
            ++indegree[next];
        }
    }
    std::deque<std::size_t> work;
    for (std::size_t pc = 0; pc < count; ++pc) {
        if (starts[pc] && indegree[pc] == 0) {
            work.push_back(pc);
        }
    }
    incoming[0] = initial;
    has_incoming[0] = true;

    auto propagate = [&](std::size_t pc, const VMState& state) {
        if (!has_incoming[pc]) {
            incoming[pc] = state;
            has_incoming[pc] = true;
        } else {
            incoming[pc] = join_states(incoming[pc], state);
        }
        result.reachable[pc] = true;
    };

    bool saw_reachable_exit = false;
    std::string error;
    std::size_t error_pc = 0;

    while (!work.empty() && error.empty()) {
        const std::size_t pc = work.front();
        work.pop_front();
        for (std::size_t next : successors[pc]) {
            if (indegree[next] > 0) {
                --indegree[next];
                if (indegree[next] == 0) {
                    work.push_back(next);
                }
            }
        }
        if (!starts[pc] || !has_incoming[pc]) {
            continue;
        }
        result.reachable[pc] = true;
        VMState state = incoming[pc];
        const Instruction& insn = program.instructions[pc];
        const auto cls = static_cast<std::uint8_t>(insn.opcode & kClassMask);
        const auto op_bits = static_cast<std::uint8_t>(insn.opcode & kOpMask);

        if (cls == kClassLd && insn.opcode == 0x18) {
            const std::uint64_t low =
                static_cast<std::uint32_t>(insn.imm);
            const std::uint64_t high = static_cast<std::uint32_t>(
                program.instructions[pc + 1].imm);
            state.regs[insn.dst()].pointer.reset();
            state.regs[insn.dst()].scalar =
                Scalar::constant(low | (high << 32));
            propagate(pc + 2, state);
            continue;
        }

        if ((cls == kClassJmp || cls == kClassJmp32) && op_bits == kJmpExit) {
            saw_reachable_exit = true;
            continue;
        }

        if (cls == kClassJmp && op_bits == kJmpCall) {
            state.regs[0].pointer.reset();
            state.regs[0].scalar = Scalar::full();
            for (std::size_t reg = 1; reg <= 5; ++reg) {
                state.regs[reg].pointer.reset();
                state.regs[reg].scalar = Scalar::full();
            }
            propagate(pc + 1, state);
            continue;
        }

        if (cls == kClassJmp && op_bits == kJmpJa) {
            const std::size_t target =
                static_cast<std::size_t>(static_cast<std::int64_t>(pc) + 1 +
                                         insn.off);
            propagate(target, state);
            continue;
        }

        if (cls == kClassJmp || cls == kClassJmp32) {
            const bool is32 = cls == kClassJmp32;
            const std::int64_t target =
                static_cast<std::size_t>(static_cast<std::int64_t>(pc) + 1 +
                                         insn.off);
            VMState taken_state = state;
            VMState fall_state = state;
            bool taken_reachable = true;
            bool fall_reachable = true;

            if (op_bits == kJmpJset) {
                const Scalar& rhs =
                    (insn.opcode & kSourceX) != 0
                        ? state.regs[insn.src()].scalar
                        : Scalar::constant(
                              is32 ? static_cast<std::uint32_t>(insn.imm)
                                   : sign_extend32(insn.imm));
                if (rhs.is_constant()) {
                    const std::uint64_t mask64 = rhs.tnum.value;
                    const std::uint32_t mask32 =
                        static_cast<std::uint32_t>(mask64);
                    if (is32) {
                        const auto lhs_const =
                            cast32(state.regs[insn.dst()].scalar)
                                .constant_value();
                        if (lhs_const.has_value()) {
                            const bool truth =
                                (static_cast<std::uint32_t>(*lhs_const) &
                                 mask32) != 0;
                            taken_reachable = truth;
                            fall_reachable = !truth;
                        } else if (mask32 == 0) {
                            taken_reachable = false;
                        }
                    } else {
                        const auto lhs_const =
                            state.regs[insn.dst()].scalar.constant_value();
                        if (lhs_const.has_value()) {
                            const bool truth = (*lhs_const & mask64) != 0;
                            taken_reachable = truth;
                            fall_reachable = !truth;
                        } else if (mask64 == 0) {
                            taken_reachable = false;
                        }
                    }
                }
            } else {
                const auto op = decode_compare(insn.opcode);
                if (!op.has_value()) {
                    error = "unsupported conditional jump";
                    error_pc = pc;
                    break;
                }
                const bool self = insn.dst() == insn.src() &&
                                  (insn.opcode & kSourceX) != 0;
                if (self) {
                    const bool truth = self_comparison_truth(*op);
                    taken_reachable = truth;
                    fall_reachable = !truth;
                } else if (!state.regs[insn.dst()].pointer.has_value() &&
                           ((insn.opcode & kSourceX) == 0 ||
                            !state.regs[insn.src()].pointer.has_value())) {
                    BranchResult split;
                    if ((insn.opcode & kSourceX) != 0) {
                        split = refine_branch(state.regs[insn.dst()].scalar,
                                              state.regs[insn.src()].scalar,
                                              *op, is32);
                    } else {
                        split = refine_branch_constant(
                            state.regs[insn.dst()].scalar, *op,
                            is32 ? static_cast<std::uint32_t>(insn.imm)
                                 : sign_extend32(insn.imm),
                            is32);
                    }
                    taken_reachable = split.taken.reachable;
                    fall_reachable = split.fallthrough.reachable;
                    if (taken_reachable) {
                        taken_state.regs[insn.dst()].scalar = split.taken.lhs;
                        if ((insn.opcode & kSourceX) != 0) {
                            taken_state.regs[insn.src()].scalar =
                                split.taken.rhs;
                        }
                    }
                    if (fall_reachable) {
                        fall_state.regs[insn.dst()].scalar =
                            split.fallthrough.lhs;
                        if ((insn.opcode & kSourceX) != 0) {
                            fall_state.regs[insn.src()].scalar =
                                split.fallthrough.rhs;
                        }
                    }
                } else if (is32) {
                    error = "32-bit comparison on pointers is not supported";
                    error_pc = pc;
                    break;
                } else {
                    auto refine_pointer_pair =
                        [&](Pointer& lhs, Pointer& rhs, bool truth) {
                            Scalar lhs_offset = Scalar::from_signed_range(
                                lhs.offset_min, lhs.offset_max);
                            Scalar rhs_offset = Scalar::from_signed_range(
                                rhs.offset_min, rhs.offset_max);
                            BranchResult split = refine_branch(
                                lhs_offset, rhs_offset, *op, false);
                            const auto& part =
                                truth ? split.taken : split.fallthrough;
                            if (!part.reachable) {
                                return false;
                            }
                            lhs.offset_min = part.lhs.smin64;
                            lhs.offset_max = part.lhs.smax64;
                            rhs.offset_min = part.rhs.smin64;
                            rhs.offset_max = part.rhs.smax64;
                            return true;
                        };
                    auto refine_pointer_constant =
                        [&](Pointer& pointer, Scalar& other, bool truth) {
                            Scalar offset = Scalar::from_signed_range(
                                pointer.offset_min, pointer.offset_max);
                            const auto part =
                                refine_constant(offset, *op,
                                                sign_extend32(insn.imm), truth,
                                                false);
                            if (!part.has_value()) {
                                return false;
                            }
                            pointer.offset_min = part->smin64;
                            pointer.offset_max = part->smax64;
                            other = std::move(*part);
                            return true;
                        };
                    auto refine_pointer_scalar =
                        [&](Pointer& pointer, Scalar& other_scalar, bool truth) {
                            Scalar offset = Scalar::from_signed_range(
                                pointer.offset_min, pointer.offset_max);
                            BranchResult split =
                                refine_branch(offset, other_scalar, *op, false);
                            const auto& part =
                                truth ? split.taken : split.fallthrough;
                            if (!part.reachable) {
                                return false;
                            }
                            pointer.offset_min = part.lhs.smin64;
                            pointer.offset_max = part.lhs.smax64;
                            other_scalar = part.rhs;
                            return true;
                        };

                    if (state.regs[insn.dst()].pointer.has_value() &&
                        (insn.opcode & kSourceX) != 0 &&
                        state.regs[insn.src()].pointer.has_value()) {
                        Pointer lhs = *state.regs[insn.dst()].pointer;
                        Pointer rhs = *state.regs[insn.src()].pointer;
                        const bool same = same_region(lhs, rhs);
                        if (same) {
                            Pointer lhs_taken = lhs;
                            Pointer rhs_taken = rhs;
                            Pointer lhs_fall = lhs;
                            Pointer rhs_fall = rhs;
                            taken_reachable = refine_pointer_pair(
                                lhs_taken, rhs_taken, true);
                            fall_reachable = refine_pointer_pair(
                                lhs_fall, rhs_fall, false);
                            if (taken_reachable) {
                                taken_state.regs[insn.dst()].pointer =
                                    lhs_taken;
                                taken_state.regs[insn.src()].pointer =
                                    rhs_taken;
                            }
                            if (fall_reachable) {
                                fall_state.regs[insn.dst()].pointer = lhs_fall;
                                fall_state.regs[insn.src()].pointer = rhs_fall;
                            }
                        }
                    } else if (state.regs[insn.dst()].pointer.has_value()) {
                        Pointer taken = *state.regs[insn.dst()].pointer;
                        Pointer fall = taken;
                        if ((insn.opcode & kSourceX) != 0) {
                            Scalar taken_rhs = state.regs[insn.src()].scalar;
                            Scalar fall_rhs = taken_rhs;
                            taken_reachable =
                                refine_pointer_scalar(taken, taken_rhs, true);
                            fall_reachable =
                                refine_pointer_scalar(fall, fall_rhs, false);
                            if (taken_reachable) {
                                taken_state.regs[insn.dst()].pointer = taken;
                                taken_state.regs[insn.src()].scalar =
                                    taken_rhs;
                            }
                            if (fall_reachable) {
                                fall_state.regs[insn.dst()].pointer = fall;
                                fall_state.regs[insn.src()].scalar = fall_rhs;
                            }
                        } else {
                            Scalar ignored;
                            taken_reachable =
                                refine_pointer_constant(taken, ignored, true);
                            fall_reachable =
                                refine_pointer_constant(fall, ignored, false);
                            if (taken_reachable) {
                                taken_state.regs[insn.dst()].pointer = taken;
                            }
                            if (fall_reachable) {
                                fall_state.regs[insn.dst()].pointer = fall;
                            }
                        }
                    } else {
                        // Scalar compared with a pointer: reverse the relation.
                        const CompareOp swapped = swap_compare(*op);
                        Pointer taken = *state.regs[insn.src()].pointer;
                        Pointer fall = taken;
                        Scalar taken_lhs = state.regs[insn.dst()].scalar;
                        Scalar fall_lhs = taken_lhs;
                        auto apply = [&](Pointer& pointer, Scalar& scalar,
                                         bool truth) {
                            Scalar offset = Scalar::from_signed_range(
                                pointer.offset_min, pointer.offset_max);
                            BranchResult split = refine_branch(
                                offset, scalar, swapped, false);
                            const auto& part =
                                truth ? split.taken : split.fallthrough;
                            if (!part.reachable) {
                                return false;
                            }
                            pointer.offset_min = part.lhs.smin64;
                            pointer.offset_max = part.lhs.smax64;
                            scalar = part.rhs;
                            return true;
                        };
                        taken_reachable = apply(taken, taken_lhs, true);
                        fall_reachable = apply(fall, fall_lhs, false);
                        if (taken_reachable) {
                            taken_state.regs[insn.src()].pointer = taken;
                            taken_state.regs[insn.dst()].scalar = taken_lhs;
                        }
                        if (fall_reachable) {
                            fall_state.regs[insn.src()].pointer = fall;
                            fall_state.regs[insn.dst()].scalar = fall_lhs;
                        }
                    }
                }
            }

            if (taken_reachable) {
                propagate(static_cast<std::size_t>(target), taken_state);
            }
            if (fall_reachable) {
                propagate(pc + 1, fall_state);
            }
            continue;
        }

        if (cls == kClassAlu || cls == kClassAlu64) {
            const bool is32 = cls == kClassAlu;
            if (op_bits == 0xd0) {
                const unsigned width = static_cast<unsigned>(insn.imm);
                if (width != 16 && width != 32 && width != 64) {
                    error = "invalid BPF_END width";
                    error_pc = pc;
                    break;
                }
                if (state.regs[insn.dst()].pointer.has_value()) {
                    error = "BPF_END applied to a pointer";
                    error_pc = pc;
                    break;
                }
                const bool to_big = (insn.opcode & kSourceX) != 0;
                const auto constant =
                    state.regs[insn.dst()].scalar.constant_value();
                if (constant.has_value() && to_big) {
                    std::uint64_t value = *constant;
                    if (width == 16) {
                        value = ((value & 0xff) << 8) | ((value >> 8) & 0xff);
                        value &= 0xffff;
                    } else if (width == 32) {
                        value = __builtin_bswap32(
                            static_cast<std::uint32_t>(value));
                    } else {
                        value = __builtin_bswap64(value);
                    }
                    state.regs[insn.dst()].scalar = Scalar::constant(value);
                } else {
                    state.regs[insn.dst()].scalar =
                        loaded_scalar(width / 8, false);
                }
                propagate(pc + 1, state);
                continue;
            }

            const auto op = decode_alu(insn.opcode);
            if (!op.has_value()) {
                error = "unsupported ALU operation";
                error_pc = pc;
                break;
            }
            RegState& dst_reg = state.regs[insn.dst()];
            const bool source_is_reg = (insn.opcode & kSourceX) != 0;
            const RegState* src_reg =
                source_is_reg ? &state.regs[insn.src()] : nullptr;

            if (*op == AluOp::Mov && source_is_reg &&
                src_reg->pointer.has_value() && !is32) {
                dst_reg = *src_reg;
                propagate(pc + 1, state);
                continue;
            }

            if (*op == AluOp::Mov &&
                !(dst_reg.pointer.has_value() && source_is_reg &&
                  src_reg->pointer.has_value())) {
                if (is32 && source_is_reg && src_reg->pointer.has_value()) {
                    error = "ALU32 MOV from a pointer";
                    error_pc = pc;
                    break;
                }
            }

            if (dst_reg.pointer.has_value()) {
                if (*op == AluOp::Mov) {
                    if (is32) {
                        error = "ALU32 pointer move";
                        error_pc = pc;
                        break;
                    }
                    if (source_is_reg) {
                        if (src_reg->pointer.has_value()) {
                            dst_reg = *src_reg;
                        } else {
                            dst_reg.pointer.reset();
                            dst_reg.scalar = src_reg->scalar;
                        }
                    } else {
                        dst_reg.pointer.reset();
                        dst_reg.scalar =
                            Scalar::constant(sign_extend32(insn.imm));
                    }
                    propagate(pc + 1, state);
                    continue;
                }
                if (*op != AluOp::Add && *op != AluOp::Sub) {
                    error = "unsupported pointer ALU operation";
                    error_pc = pc;
                    break;
                }
                if (source_is_reg && src_reg->pointer.has_value()) {
                    error = "pointer arithmetic with a pointer operand";
                    error_pc = pc;
                    break;
                }
                Pointer pointer = *dst_reg.pointer;
                bool adjusted = true;
                if (source_is_reg) {
                    const Scalar& scalar = src_reg->scalar;
                    if (scalar.empty) {
                        adjusted = false;
                    } else if (*op == AluOp::Add) {
                        adjusted = translate_pointer(pointer, scalar.smin64,
                                                     scalar.smax64);
                    } else {
                        if (scalar.smax64 == kI64Min) {
                            adjusted = false;
                        } else {
                            adjusted = translate_pointer(pointer,
                                                         -scalar.smax64,
                                                         -scalar.smin64);
                        }
                    }
                } else {
                    std::int64_t immediate =
                        static_cast<std::int64_t>(sign_extend32(insn.imm));
                    if (*op == AluOp::Sub) {
                        immediate = -immediate;
                    }
                    adjusted =
                        translate_pointer(pointer, immediate, immediate);
                }
                if (!adjusted) {
                    dst_reg.pointer.reset();
                    dst_reg.scalar = Scalar::full();
                } else {
                    dst_reg.pointer = pointer;
                }
                propagate(pc + 1, state);
                continue;
            }

            if (source_is_reg && src_reg->pointer.has_value()) {
                error = "pointer used as a scalar operand";
                error_pc = pc;
                break;
            }
            const Scalar rhs =
                source_is_reg
                    ? src_reg->scalar
                    : Scalar::constant(is32
                                           ? static_cast<std::uint32_t>(insn.imm)
                                           : sign_extend32(insn.imm));
            dst_reg.scalar = alu(*op, dst_reg.scalar, rhs, is32);
            propagate(pc + 1, state);
            continue;
        }

        if (cls == kClassLdx || cls == kClassSt || cls == kClassStx) {
            const auto mode = static_cast<std::uint8_t>(insn.opcode & kModeMask);
            if (!is_pointer_mode(insn.opcode)) {
                error = "unsupported memory mode";
                error_pc = pc;
                break;
            }
            const std::size_t pointer_reg =
                cls == kClassLdx ? insn.src() : insn.dst();
            const std::uint64_t size = access_size(insn.opcode);
            if (size == 0) {
                error = "invalid memory access size";
                error_pc = pc;
                break;
            }
            const MemoryAccess access = check_memory_access(
                state.regs[pointer_reg], insn.off, size);
            if (!access.safe) {
                error = "unsafe memory access: " + access.reason;
                error_pc = pc;
                break;
            }
            if (cls == kClassLdx) {
                const bool sign_extend = mode == kModeMemSx;
                if (mode == kModeAtomic) {
                    error = "atomic load is invalid";
                    error_pc = pc;
                    break;
                }
                state.regs[insn.dst()].pointer.reset();
                state.regs[insn.dst()].scalar =
                    loaded_scalar(size, sign_extend);
            }
            propagate(pc + 1, state);
            continue;
        }

        error = "unsupported instruction class";
        error_pc = pc;
    }

    if (!error.empty()) {
        result.pc = error_pc;
        result.error = std::move(error);
        return result;
    }
    if (!saw_reachable_exit) {
        result.error = "program has no reachable exit";
        return result;
    }
    result.ok = true;
    return result;
}

}  // namespace ebpf
