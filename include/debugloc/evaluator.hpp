// debugloc/evaluator.hpp
//
// Budgeted, stack-based evaluator for debug location expressions.
//
// A program is a sequence of Ops executed against a value stack. Values are
// BitValues: every bit is either known or unknown. Memory that cannot be
// read yields *unknown* bits (never zero). All address computations are
// checked for overflow. Execution stops with an error when the operation
// budget is exhausted, the stack underflows/overflows, or an address
// computation wraps.
#pragma once

#include <cstdint>
#include <functional>
#include <map>
#include <string>
#include <vector>

#include "debugloc/value.hpp"

namespace debugloc {

enum class Error {
  None,
  BudgetExhausted,     // operation budget ran out
  StackUnderflow,      // pop from an empty stack
  StackOverflow,       // stack deeper than the configured limit
  AddressOverflow,     // address + size wrapped around the address space
  ArithmeticOverflow,  // checked add/sub/mul wrapped
  InvalidWidth,        // zero width, or result wider than kMaxWidth
  InvalidRegister,     // register number not present in the register file
  InvalidOp,           // malformed op (e.g. bad operand)
};

const char* errorName(Error e);

enum class OpCode : uint8_t {
  Lit,     // push constant {imm, width=aux}
  Reg,     // push register number imm
  Deref,   // pop address, push memory[addr .. addr+aux) (aux = byte size)
  DerefAt, // push memory[imm .. imm+aux)
  Add,     // checked: pop b, pop a, push a+b (error on wrap)
  Sub,     // checked: pop b, pop a, push a-b (error on wrap)
  Mul,     // checked: pop b, pop a, push a*b (error on wrap)
  And, Or, Xor, Not,
  Shl,     // pop a, push a << imm
  Shr,     // pop a, push a >> imm
  Eq,      // pop b, pop a, push 1-bit (a == b)
  Lt,      // pop b, pop a, push 1-bit (a <  b), unsigned
  Dup,     // push copy of top
  Drop,    // discard top
  Swap,    // exchange top two
  Piece,   // pop v, push bit slice of v: aux = size in bits, aux2 = offset.
           // Offset counts from the LSB on little-endian targets and from
           // the MSB on big-endian targets (DWARF DW_OP_bit_piece rules).
  Concat,  // pop lo, pop hi, push hi:lo (hi occupies the high bits)
};

struct Op {
  OpCode code;
  uint64_t imm = 0;
  uint32_t aux = 0;
  uint32_t aux2 = 0;

  static Op lit(uint64_t v, uint32_t width) { return {OpCode::Lit, v, width, 0}; }
  static Op reg(uint64_t n) { return {OpCode::Reg, n, 0, 0}; }
  static Op deref(uint32_t size) { return {OpCode::Deref, 0, size, 0}; }
  static Op derefAt(uint64_t addr, uint32_t size) { return {OpCode::DerefAt, addr, size, 0}; }
  static Op simple(OpCode c) { return {c, 0, 0, 0}; }
  static Op shl(uint32_t n) { return {OpCode::Shl, n, 0, 0}; }
  static Op shr(uint32_t n) { return {OpCode::Shr, n, 0, 0}; }
  static Op piece(uint32_t sizeBits, uint32_t offsetBits) {
    return {OpCode::Piece, 0, sizeBits, offsetBits};
  }
};

// Byte-granular memory. readByte returns false when the byte is
// unavailable; the evaluator turns unavailable bytes into unknown bits.
class Memory {
 public:
  virtual ~Memory() = default;
  virtual bool readByte(uint64_t addr, uint8_t& out) const = 0;
};

// Sparse memory backed by a map; addresses not present are unavailable.
class SparseMemory final : public Memory {
 public:
  void writeByte(uint64_t addr, uint8_t v) { bytes_[addr] = v; }
  void write(uint64_t addr, const std::vector<uint8_t>& data) {
    for (size_t i = 0; i < data.size(); ++i) bytes_[addr + i] = data[i];
  }
  bool readByte(uint64_t addr, uint8_t& out) const override {
    auto it = bytes_.find(addr);
    if (it == bytes_.end()) return false;
    out = it->second;
    return true;
  }
 private:
  std::map<uint64_t, uint8_t> bytes_;
};

// Register file. readRegister returns false when the register number is
// invalid; a register that exists but has no recoverable value should set
// `out` to BitValue::unknown(width) and return true.
class RegisterFile {
 public:
  virtual ~RegisterFile() = default;
  virtual bool readRegister(uint32_t num, BitValue& out) const = 0;
};

// Simple register file with a fixed width per register number.
class MapRegisters final : public RegisterFile {
 public:
  explicit MapRegisters(uint32_t width) : width_(width) {}
  void set(uint32_t num, uint64_t v) { values_[num] = BitValue::constant(v, width_); }
  void setUnknown(uint32_t num) { values_[num] = BitValue::unknown(width_); }
  bool readRegister(uint32_t num, BitValue& out) const override {
    auto it = values_.find(num);
    if (it == values_.end()) return false;
    out = it->second;
    return true;
  }
 private:
  uint32_t width_;
  std::map<uint32_t, BitValue> values_;
};

struct EvalOptions {
  bool littleEndian = true;
  uint64_t budget = 1000;        // 1 per op, plus 1 per memory byte read
  size_t maxStackDepth = 256;
};

struct EvalResult {
  Error error = Error::None;
  BitValue value;                // valid only when error == Error::None
  uint64_t opsUsed = 0;
  std::string message;           // detail when error != Error::None
  bool ok() const { return error == Error::None; }
};

class Evaluator {
 public:
  Evaluator(const Memory* mem, const RegisterFile* regs, EvalOptions opts)
      : mem_(mem), regs_(regs), opts_(opts) {}

  EvalResult eval(const std::vector<Op>& program);

 private:
  EvalResult fail(Error e, std::string msg);
  bool charge(uint64_t cost);    // false => budget exhausted
  EvalResult pop(BitValue& out);
  EvalResult push(BitValue v);
  EvalResult readMemory(uint64_t addr, uint32_t size, BitValue& out);

  const Memory* mem_;
  const RegisterFile* regs_;
  EvalOptions opts_;
  std::vector<BitValue> stack_;
  uint64_t spent_ = 0;
};

}  // namespace debugloc
