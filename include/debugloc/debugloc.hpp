// debugloc: budgeted evaluator for stack-based debug location expressions.
//
// Supports reading registers and memory, concatenating arbitrary (possibly
// unaligned) bit segments, and switching endianness mid-expression. Values
// track known/unknown state per bit: unavailable memory yields partially
// unknown values instead of zero. All address arithmetic is overflow-checked.
#pragma once

#include <cstddef>
#include <cstdint>
#include <optional>
#include <vector>

namespace debugloc {

enum class Error {
    None,
    BudgetExhausted,     // operation budget ran out
    StackUnderflow,      // pop from empty expression stack
    AddressOverflow,     // address arithmetic wrapped around
    UnknownAddress,      // dereference of a value with unknown bits
    RegisterUnavailable, // register provider has no such register
    InvalidBitRange,     // bit-field offset/size outside the value
};

const char* toString(Error e);

// Minimal expected-like result type (C++17 has no std::expected).
template <typename T>
class Result {
public:
    static Result ok(T value) {
        Result r;
        r.ok_ = true;
        r.value_ = std::move(value);
        return r;
    }
    static Result fail(Error e) {
        Result r;
        r.error_ = e;
        return r;
    }
    bool hasValue() const { return ok_; }
    explicit operator bool() const { return ok_; }
    Error error() const { return error_; }
    T& value() & { return value_; }
    const T& value() const& { return value_; }
    T&& value() && { return std::move(value_); }

private:
    Result() = default;
    bool ok_ = false;
    Error error_ = Error::None;
    T value_{};
};

// A little-endian bit vector with a per-bit known mask. Bit 0 is the LSB.
class Value {
public:
    // Fully known value of `bits` bits (1..64).
    static Value fromUint(uint64_t v, size_t bits);
    static Value fromUint64(uint64_t v) { return fromUint(v, 64); }
    // Fully unknown value of `bits` bits.
    static Value unknown(size_t bits);
    // From raw little-endian bytes plus a per-byte known mask.
    static Value bytes(std::vector<uint8_t> data, std::vector<uint8_t> knownMask);

    size_t bitSize() const { return bits_; }
    bool bit(size_t i) const;
    bool bitKnown(size_t i) const;
    bool fullyKnown() const;

    // Integer value, only if fully known and at most 64 bits.
    std::optional<uint64_t> toUint64() const;

    // Bits [offset, offset+size) as a new value; preserves unknown bits.
    Value slice(size_t offset, size_t size) const;
    // `low` in the low bits, `high` above it.
    static Value concat(const Value& low, const Value& high);
    // Reverse byte order (bit size must be byte-aligned).
    Value byteReversed() const;

    const std::vector<uint8_t>& data() const { return data_; }
    const std::vector<uint8_t>& knownMask() const { return known_; }

private:
    void setKnownBit(size_t i, bool b);

    std::vector<uint8_t> data_;  // little-endian bytes
    std::vector<uint8_t> known_; // per-byte mask: 1 = bit known
    size_t bits_ = 0;
};

enum class Endian { Little, Big };

enum class OpCode : uint8_t {
    PushConst, // imm = constant (pushed as 64-bit value)
    LoadReg,   // imm = register id
    Deref,     // imm = size in bytes; pops address, pushes memory contents
    Add,       // pops b, a; pushes a + b (overflow-checked)
    Sub,       // pops b, a; pushes a - b (underflow-checked)
    Mul,       // pops b, a; pushes a * b (overflow-checked)
    BitField,  // imm = bit offset, imm2 = bit size; pops value, pushes slice
    Concat,    // pops high, low; pushes low ++ high
    SetEndian, // imm = 0 little, 1 big; affects subsequent Deref
};

struct Instr {
    OpCode op;
    uint64_t imm = 0;
    uint64_t imm2 = 0;

    static Instr pushConst(uint64_t v) { return {OpCode::PushConst, v, 0}; }
    static Instr loadReg(uint32_t id) { return {OpCode::LoadReg, id, 0}; }
    static Instr deref(uint64_t size) { return {OpCode::Deref, size, 0}; }
    static Instr add() { return {OpCode::Add, 0, 0}; }
    static Instr sub() { return {OpCode::Sub, 0, 0}; }
    static Instr mul() { return {OpCode::Mul, 0, 0}; }
    static Instr bitField(uint64_t offset, uint64_t size) {
        return {OpCode::BitField, offset, size};
    }
    static Instr concat() { return {OpCode::Concat, 0, 0}; }
    static Instr setEndian(Endian e) {
        return {OpCode::SetEndian, e == Endian::Big ? 1u : 0u, 0};
    }
};

// Memory callback: returns one optional byte per address; nullopt means the
// byte is unavailable (unmapped, paged out, ...).
class Memory {
public:
    virtual ~Memory() = default;
    virtual std::vector<std::optional<uint8_t>> read(uint64_t addr,
                                                     size_t size) const = 0;
};

// Register callback: little-endian register bytes, or nullopt if unavailable.
class Registers {
public:
    virtual ~Registers() = default;
    virtual std::optional<std::vector<uint8_t>> read(uint32_t id) const = 0;
};

struct EvalOptions {
    size_t maxOps = 1000;               // instruction budget
    Endian endian = Endian::Little;     // initial endianness for Deref
};

class Evaluator {
public:
    Evaluator(const Memory& mem, const Registers& regs,
              EvalOptions opts = {});

    // Runs the program; returns the top of the stack.
    Result<Value> run(const std::vector<Instr>& program);

private:
    Result<Value> pop();
    Error exec(const Instr& ins);

    const Memory& mem_;
    const Registers& regs_;
    EvalOptions opts_;
    Endian endian_ = Endian::Little;
    size_t ops_ = 0;
    std::vector<Value> stack_;
};

} // namespace debugloc
