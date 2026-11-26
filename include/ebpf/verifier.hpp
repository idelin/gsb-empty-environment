#ifndef EBPF_VERIFIER_VERIFIER_HPP
#define EBPF_VERIFIER_VERIFIER_HPP

#include <array>
#include <cstddef>
#include <cstdint>
#include <limits>
#include <optional>
#include <string>
#include <utility>
#include <vector>

namespace ebpf {

inline constexpr std::uint64_t kU64Max = std::numeric_limits<std::uint64_t>::max();
inline constexpr std::uint32_t kU32Max = std::numeric_limits<std::uint32_t>::max();
inline constexpr std::int64_t kI64Min = std::numeric_limits<std::int64_t>::min();
inline constexpr std::int64_t kI64Max = std::numeric_limits<std::int64_t>::max();
inline constexpr std::int32_t kI32Min = std::numeric_limits<std::int32_t>::min();
inline constexpr std::int32_t kI32Max = std::numeric_limits<std::int32_t>::max();

[[nodiscard]] constexpr std::uint64_t sign_extend32(std::int32_t value) noexcept {
    return static_cast<std::uint64_t>(static_cast<std::int64_t>(value));
}

// A bit-level value: known one bits live in `value`, unknown bits in `mask`.
// The empty mask is a constant; a full mask represents every uint64_t value.
struct Tnum {
    std::uint64_t value = 0;
    std::uint64_t mask = kU64Max;

    constexpr Tnum() noexcept = default;
    constexpr Tnum(std::uint64_t v, std::uint64_t m) noexcept
        : value(v & ~m), mask(m) {}

    [[nodiscard]] static constexpr Tnum full() noexcept { return {0, kU64Max}; }
    [[nodiscard]] static constexpr Tnum constant(std::uint64_t v) noexcept { return {v, 0}; }
    [[nodiscard]] static constexpr Tnum make(std::uint64_t v, std::uint64_t m) noexcept {
        return {v & ~m, m};
    }
    [[nodiscard]] static Tnum from_unsigned_range(std::uint64_t lo, std::uint64_t hi) noexcept;

    [[nodiscard]] constexpr bool contains(std::uint64_t v) const noexcept {
        return (v & ~mask) == value;
    }
    [[nodiscard]] constexpr bool is_constant() const noexcept { return mask == 0; }
    [[nodiscard]] constexpr std::uint64_t constant_value() const noexcept { return value; }
    [[nodiscard]] constexpr std::uint64_t umin() const noexcept { return value; }
    [[nodiscard]] constexpr std::uint64_t umax() const noexcept { return value | mask; }
    [[nodiscard]] constexpr std::int64_t smin() const noexcept {
        if ((value & (std::uint64_t{1} << 63)) != 0) {
            return static_cast<std::int64_t>(value);
        }
        if ((mask & (std::uint64_t{1} << 63)) != 0) {
            return static_cast<std::int64_t>(value | (std::uint64_t{1} << 63));
        }
        return static_cast<std::int64_t>(value);
    }
    [[nodiscard]] constexpr std::int64_t smax() const noexcept {
        if ((value & (std::uint64_t{1} << 63)) != 0) {
            return static_cast<std::int64_t>(value | mask);
        }
        if ((mask & (std::uint64_t{1} << 63)) != 0) {
            return static_cast<std::int64_t>(value | (mask & ~(std::uint64_t{1} << 63)));
        }
        return static_cast<std::int64_t>(value | mask);
    }
    [[nodiscard]] constexpr std::uint32_t u32_min() const noexcept {
        return static_cast<std::uint32_t>(value);
    }
    [[nodiscard]] constexpr std::uint32_t u32_max() const noexcept {
        return static_cast<std::uint32_t>(value | mask);
    }
    [[nodiscard]] constexpr std::int32_t s32_min() const noexcept {
        const auto v = static_cast<std::uint32_t>(value);
        const auto m = static_cast<std::uint32_t>(mask);
        if ((v & 0x80000000U) != 0) {
            return static_cast<std::int32_t>(v);
        }
        if ((m & 0x80000000U) != 0) {
            return static_cast<std::int32_t>(v | 0x80000000U);
        }
        return static_cast<std::int32_t>(v);
    }
    [[nodiscard]] constexpr std::int32_t s32_max() const noexcept {
        const auto v = static_cast<std::uint32_t>(value);
        const auto m = static_cast<std::uint32_t>(mask);
        if ((v & 0x80000000U) != 0) {
            return static_cast<std::int32_t>(v | m);
        }
        if ((m & 0x80000000U) != 0) {
            return static_cast<std::int32_t>(v | (m & 0x7fffffffU));
        }
        return static_cast<std::int32_t>(v | m);
    }
};

[[nodiscard]] constexpr bool operator==(Tnum a, Tnum b) noexcept {
    return a.value == b.value && a.mask == b.mask;
}
[[nodiscard]] constexpr bool operator!=(Tnum a, Tnum b) noexcept { return !(a == b); }

[[nodiscard]] Tnum tnum_add(Tnum a, Tnum b) noexcept;
[[nodiscard]] Tnum tnum_sub(Tnum a, Tnum b) noexcept;
[[nodiscard]] Tnum tnum_and(Tnum a, Tnum b) noexcept;
[[nodiscard]] Tnum tnum_or(Tnum a, Tnum b) noexcept;
[[nodiscard]] Tnum tnum_xor(Tnum a, Tnum b) noexcept;
[[nodiscard]] Tnum tnum_lshift(Tnum a, unsigned shift) noexcept;
[[nodiscard]] Tnum tnum_rshift(Tnum a, unsigned shift) noexcept;
[[nodiscard]] Tnum tnum_arshift(Tnum a, unsigned shift) noexcept;
[[nodiscard]] Tnum tnum_lshift32(Tnum a, unsigned shift) noexcept;
[[nodiscard]] Tnum tnum_rshift32(Tnum a, unsigned shift) noexcept;
[[nodiscard]] Tnum tnum_arshift32(Tnum a, unsigned shift) noexcept;
[[nodiscard]] Tnum tnum_cast32(Tnum a) noexcept;
[[nodiscard]] Tnum tnum_join(Tnum a, Tnum b) noexcept;

// The scalar state is the intersection of all five component constraints.
// `normalize()` computes a mutual fixed point and sets `empty` if no value can
// satisfy the intersection. Every transfer below returns a normalized state.
struct Scalar {
    Tnum tnum = Tnum::full();
    std::uint64_t umin64 = 0;
    std::uint64_t umax64 = kU64Max;
    std::int64_t smin64 = kI64Min;
    std::int64_t smax64 = kI64Max;
    std::uint32_t u32_min = 0;
    std::uint32_t u32_max = kU32Max;
    std::int32_t s32_min = kI32Min;
    std::int32_t s32_max = kI32Max;
    bool empty = false;

    [[nodiscard]] static Scalar full() noexcept { return {}; }
    [[nodiscard]] static Scalar constant(std::uint64_t value) noexcept;
    [[nodiscard]] static Scalar from_unsigned_range(std::uint64_t lo,
                                                    std::uint64_t hi) noexcept;
    [[nodiscard]] static Scalar from_signed_range(std::int64_t lo,
                                                  std::int64_t hi) noexcept;
    [[nodiscard]] static Scalar from_tnum(Tnum value) noexcept;

    [[nodiscard]] bool contains(std::uint64_t value) const noexcept;
    [[nodiscard]] std::optional<std::uint64_t> constant_value() const noexcept;
    [[nodiscard]] bool is_constant() const noexcept { return constant_value().has_value(); }
    [[nodiscard]] bool is_pointer_safe_nonnegative() const noexcept;
    [[nodiscard]] bool has_value() const noexcept;

    void normalize() noexcept;
    [[nodiscard]] Scalar normalized() const noexcept {
        Scalar copy = *this;
        copy.normalize();
        return copy;
    }
};

using ScalarState = Scalar;

[[nodiscard]] Scalar cast32(const Scalar& value) noexcept;
[[nodiscard]] Scalar join_scalar(const Scalar& a, const Scalar& b) noexcept;

enum class AluOp : std::uint8_t {
    Add,
    Sub,
    Mul,
    Div,
    Mod,
    And,
    Or,
    Xor,
    Lsh,
    Rsh,
    Arsh,
    Neg,
    Mov,
};

[[nodiscard]] Scalar alu64(AluOp op, const Scalar& lhs, const Scalar& rhs) noexcept;
[[nodiscard]] Scalar alu32(AluOp op, const Scalar& lhs, const Scalar& rhs) noexcept;
[[nodiscard]] Scalar alu(AluOp op, const Scalar& lhs, const Scalar& rhs, bool is32) noexcept;
[[nodiscard]] Scalar neg64(const Scalar& value) noexcept;
[[nodiscard]] Scalar neg32(const Scalar& value) noexcept;

enum class CompareOp : std::uint8_t {
    Eq,
    Ne,
    Lt,
    Le,
    Gt,
    Ge,
    Slt,
    Sle,
    Sgt,
    Sge,
};

struct BranchSplit {
    bool reachable = true;
    Scalar lhs;
    Scalar rhs;
};

struct BranchResult {
    BranchSplit taken;
    BranchSplit fallthrough;
};

// Splits a conditional edge into the taken and fall-through abstract states.
// `is32` compares only the low 32 bits; this intentionally does not equate the
// high halves of two registers.
[[nodiscard]] BranchResult refine_branch(const Scalar& lhs,
                                         const Scalar& rhs,
                                         CompareOp op,
                                         bool is32 = false) noexcept;

[[nodiscard]] BranchResult refine_branch_constant(const Scalar& lhs,
                                                   CompareOp op,
                                                   std::uint64_t rhs,
                                                   bool is32 = false) noexcept;

[[nodiscard]] std::optional<Scalar> refine_constant(const Scalar& lhs,
                                                    CompareOp op,
                                                    std::uint64_t rhs,
                                                    bool truth,
                                                    bool is32 = false) noexcept;

enum class PointerKind : std::uint8_t {
    Packet,
    MapValue,
    Stack,
};

struct Pointer {
    PointerKind kind = PointerKind::Packet;
    std::uint32_t region_id = 0;
    std::uint64_t region_size = 0;
    std::int64_t offset_min = 0;
    std::int64_t offset_max = 0;
};

struct RegState {
    Scalar scalar;
    std::optional<Pointer> pointer;

    [[nodiscard]] bool is_pointer() const noexcept { return pointer.has_value(); }
};

struct MemoryAccess {
    bool safe = false;
    std::int64_t effective_min = 0;
    std::int64_t effective_max = 0;
    std::string reason;
};

[[nodiscard]] MemoryAccess check_memory_access(const RegState& pointer_reg,
                                               std::int64_t insn_offset,
                                               std::uint64_t size) noexcept;

// Classic 8-byte eBPF instruction encoding. The verifier consumes this form so
// a loader can pass bytecode without depending on kernel UAPI headers.
struct Instruction {
    std::uint8_t opcode = 0;
    std::uint8_t regs = 0;
    std::int16_t off = 0;
    std::int32_t imm = 0;

    constexpr Instruction() = default;
    constexpr Instruction(std::uint8_t op, std::uint8_t d, std::uint8_t s,
                          std::int16_t o, std::int32_t i) noexcept
        : opcode(op),
          regs(static_cast<std::uint8_t>((d & 0x0fU) | ((s & 0x0fU) << 4))),
          off(o),
          imm(i) {}

    [[nodiscard]] constexpr std::uint8_t dst() const noexcept {
        return static_cast<std::uint8_t>(regs & 0x0fU);
    }
    [[nodiscard]] constexpr std::uint8_t src() const noexcept {
        return static_cast<std::uint8_t>((regs >> 4) & 0x0fU);
    }
    constexpr void set_dst(std::uint8_t value) noexcept {
        regs = static_cast<std::uint8_t>((regs & 0xf0U) | (value & 0x0fU));
    }
    constexpr void set_src(std::uint8_t value) noexcept {
        regs = static_cast<std::uint8_t>((regs & 0x0fU) |
                                         ((value & 0x0fU) << 4));
    }
};

static_assert(sizeof(Instruction) == 8,
              "Instruction must match classic 8-byte eBPF encoding");
static_assert(offsetof(Instruction, opcode) == 0);
static_assert(offsetof(Instruction, regs) == 1);
static_assert(offsetof(Instruction, off) == 2);
static_assert(offsetof(Instruction, imm) == 4);
using Insn = Instruction;

inline constexpr std::size_t kRegisterCount = 11;

struct Program {
    std::vector<Instruction> instructions;
    std::uint64_t packet_length = 0;
    std::uint64_t stack_size = 512;
    bool use_custom_initial_regs = false;
    std::array<RegState, kRegisterCount> initial_regs{};
};

struct VerificationResult {
    bool ok = false;
    std::size_t pc = 0;
    std::string error;
    std::vector<bool> reachable;
};

[[nodiscard]] VerificationResult verify_dag(const Program& program);

[[nodiscard]] inline VerificationResult verify(const Program& program) {
    return verify_dag(program);
}

}  // namespace ebpf

#endif
