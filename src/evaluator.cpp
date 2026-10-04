#include "debugloc/evaluator.hpp"

#include <sstream>

namespace debugloc {

const char* errorName(Error e) {
  switch (e) {
    case Error::None: return "None";
    case Error::BudgetExhausted: return "BudgetExhausted";
    case Error::StackUnderflow: return "StackUnderflow";
    case Error::StackOverflow: return "StackOverflow";
    case Error::AddressOverflow: return "AddressOverflow";
    case Error::ArithmeticOverflow: return "ArithmeticOverflow";
    case Error::InvalidWidth: return "InvalidWidth";
    case Error::InvalidRegister: return "InvalidRegister";
    case Error::InvalidOp: return "InvalidOp";
  }
  return "?";
}

namespace {

// Lowest unknown bit position across two values, or kMaxWidth if both are
// fully known.
uint32_t lowestUnknown(const BitValue& a, const BitValue& b) {
  uint64_t unk = (~a.known & BitValue::maskFor(a.width)) |
                 (~b.known & BitValue::maskFor(b.width));
  if (!unk) return kMaxWidth;
  return static_cast<uint32_t>(__builtin_ctzll(unk));
}

// Arithmetic results are exact below the lowest unknown input bit (carries
// only propagate upward); everything at or above it is unknown.
BitValue arithUnknown(uint64_t exact, uint32_t width, uint32_t lu) {
  BitValue r;
  r.width = width;
  if (lu >= width) {
    r.known = BitValue::maskFor(width);
  } else {
    r.known = lu == 0 ? 0 : (BitValue::maskFor(lu));
  }
  r.bits = exact & r.known;
  return r;
}

BitValue tristateAnd(const BitValue& a, const BitValue& b) {
  BitValue r;
  r.width = a.width;
  uint64_t m = BitValue::maskFor(r.width);
  // 0 & x = 0 (known); 1 & 1 = 1; otherwise unknown.
  r.bits = a.bits & b.bits;
  r.known = ((a.known & b.known) | (~a.bits & a.known) | (~b.bits & b.known)) & m;
  r.bits &= r.known;
  return r;
}

BitValue tristateOr(const BitValue& a, const BitValue& b) {
  BitValue r;
  r.width = a.width;
  uint64_t m = BitValue::maskFor(r.width);
  // 1 | x = 1 (known); 0 | 0 = 0; otherwise unknown.
  r.bits = a.bits | b.bits;
  r.known = ((a.known & b.known) | (a.bits & a.known) | (b.bits & b.known)) & m;
  r.bits &= r.known;
  return r;
}

BitValue tristateXor(const BitValue& a, const BitValue& b) {
  BitValue r;
  r.width = a.width;
  r.bits = a.bits ^ b.bits;
  r.known = a.known & b.known & BitValue::maskFor(r.width);
  r.bits &= r.known;
  return r;
}

}  // namespace

EvalResult Evaluator::fail(Error e, std::string msg) {
  EvalResult r;
  r.error = e;
  r.opsUsed = spent_;
  r.message = std::move(msg);
  return r;
}

bool Evaluator::charge(uint64_t cost) {
  if (cost > opts_.budget || spent_ > opts_.budget - cost) return false;
  spent_ += cost;
  return true;
}

EvalResult Evaluator::pop(BitValue& out) {
  if (stack_.empty()) return fail(Error::StackUnderflow, "pop from empty stack");
  out = stack_.back();
  stack_.pop_back();
  return {};
}

EvalResult Evaluator::push(BitValue v) {
  if (stack_.size() >= opts_.maxStackDepth)
    return fail(Error::StackOverflow, "stack depth limit reached");
  stack_.push_back(v);
  return {};
}

EvalResult Evaluator::readMemory(uint64_t addr, uint32_t size, BitValue& out) {
  if (size == 0 || size > 8)
    return fail(Error::InvalidWidth, "memory access size must be 1..8 bytes");
  if (addr > UINT64_MAX - (size - 1))
    return fail(Error::AddressOverflow, "address + size wraps the address space");
  if (!mem_) return fail(Error::InvalidOp, "no memory source configured");
  if (!charge(size)) return fail(Error::BudgetExhausted, "budget exhausted reading memory");

  out = BitValue::unknown(size * 8);
  for (uint32_t i = 0; i < size; ++i) {
    uint8_t byte = 0;
    if (!mem_->readByte(addr + i, byte)) continue;  // stays unknown, not zero
    // Little-endian: byte i is the i-th least significant byte.
    uint32_t shift = opts_.littleEndian ? 8 * i : 8 * (size - 1 - i);
    out.bits |= uint64_t{byte} << shift;
    out.known |= uint64_t{0xFF} << shift;
  }
  return {};
}

EvalResult Evaluator::eval(const std::vector<Op>& program) {
  stack_.clear();
  spent_ = 0;

  auto binary = [&](BitValue& a, BitValue& b) -> EvalResult {
    if (auto r = pop(b); !r.ok()) return r;
    if (auto r = pop(a); !r.ok()) return r;
    if (a.width != b.width)
      return fail(Error::InvalidWidth, "operand width mismatch");
    return {};
  };

  for (size_t pc = 0; pc < program.size(); ++pc) {
    const Op& op = program[pc];
    if (!charge(1)) return fail(Error::BudgetExhausted, "operation budget exhausted");

    switch (op.code) {
      case OpCode::Lit: {
        if (op.aux == 0 || op.aux > kMaxWidth)
          return fail(Error::InvalidWidth, "literal width out of range");
        if (auto r = push(BitValue::constant(op.imm, op.aux)); !r.ok()) return r;
        break;
      }
      case OpCode::Reg: {
        if (!regs_) return fail(Error::InvalidOp, "no register file configured");
        if (op.imm > UINT32_MAX)
          return fail(Error::InvalidRegister, "register number out of range");
        BitValue v;
        if (!regs_->readRegister(static_cast<uint32_t>(op.imm), v))
          return fail(Error::InvalidRegister, "unknown register number");
        if (auto r = push(v); !r.ok()) return r;
        break;
      }
      case OpCode::Deref: {
        BitValue addr;
        if (auto r = pop(addr); !r.ok()) return r;
        BitValue v;
        if (!addr.isFullyKnown()) {
          // Cannot locate the object: whole value is unknown.
          if (op.aux == 0 || op.aux > 8)
            return fail(Error::InvalidWidth, "deref size must be 1..8 bytes");
          v = BitValue::unknown(op.aux * 8);
        } else if (auto r = readMemory(addr.bits, op.aux, v); !r.ok()) {
          return r;
        }
        if (auto r = push(v); !r.ok()) return r;
        break;
      }
      case OpCode::DerefAt: {
        BitValue v;
        if (auto r = readMemory(op.imm, op.aux, v); !r.ok()) return r;
        if (auto r = push(v); !r.ok()) return r;
        break;
      }
      case OpCode::Add:
      case OpCode::Sub:
      case OpCode::Mul: {
        BitValue a, b;
        if (auto r = binary(a, b); !r.ok()) return r;
        uint32_t lu = lowestUnknown(a, b);
        uint64_t exact = 0;
        if (lu >= kMaxWidth) {  // both fully known: checked arithmetic
          uint64_t res;
          bool ovf = op.code == OpCode::Add ? __builtin_add_overflow(a.bits, b.bits, &res)
                     : op.code == OpCode::Sub ? __builtin_sub_overflow(a.bits, b.bits, &res)
                                              : __builtin_mul_overflow(a.bits, b.bits, &res);
          if (ovf || (a.width < 64 && (res & ~BitValue::maskFor(a.width))))
            return fail(Error::ArithmeticOverflow, "checked arithmetic wrapped");
          exact = res;
        } else {
          uint64_t m = lu >= 64 ? ~uint64_t{0} : (BitValue::maskFor(lu));
          uint64_t av = a.bits & m, bv = b.bits & m;
          exact = op.code == OpCode::Add ? av + bv
                  : op.code == OpCode::Sub ? av - bv
                                           : av * bv;
        }
        if (auto r = push(arithUnknown(exact, a.width, lu)); !r.ok()) return r;
        break;
      }
      case OpCode::And:
      case OpCode::Or:
      case OpCode::Xor: {
        BitValue a, b;
        if (auto r = binary(a, b); !r.ok()) return r;
        BitValue v = op.code == OpCode::And ? tristateAnd(a, b)
                     : op.code == OpCode::Or ? tristateOr(a, b)
                                             : tristateXor(a, b);
        if (auto r = push(v); !r.ok()) return r;
        break;
      }
      case OpCode::Not: {
        BitValue a;
        if (auto r = pop(a); !r.ok()) return r;
        a.bits = (~a.bits) & a.known;
        if (auto r = push(a); !r.ok()) return r;
        break;
      }
      case OpCode::Shl:
      case OpCode::Shr: {
        BitValue a;
        if (auto r = pop(a); !r.ok()) return r;
        uint32_t n = static_cast<uint32_t>(op.imm);
        BitValue v;
        if (n == 0) {
          v = a;
        } else if (n >= a.width) {
          v = BitValue::constant(0, a.width);  // zeros shifted in are known
        } else if (op.code == OpCode::Shl) {
          v.width = a.width;
          v.bits = (a.bits << n) & BitValue::maskFor(a.width);
          v.known = ((a.known << n) | BitValue::maskFor(n)) & BitValue::maskFor(a.width);
          v.bits &= v.known;
        } else {
          v.width = a.width;
          v.bits = a.bits >> n;
          v.known = (a.known >> n) | (BitValue::maskFor(n) << (a.width - n));
          v.known &= BitValue::maskFor(a.width);
          v.bits &= v.known;
        }
        if (auto r = push(v); !r.ok()) return r;
        break;
      }
      case OpCode::Eq:
      case OpCode::Lt: {
        BitValue a, b;
        if (auto r = binary(a, b); !r.ok()) return r;
        BitValue v = BitValue::unknown(1);
        uint64_t common = a.known & b.known;
        if (op.code == OpCode::Eq && (a.bits & common) != (b.bits & common)) {
          v = BitValue::constant(0, 1);  // provably unequal
        } else if (a.isFullyKnown() && b.isFullyKnown()) {
          bool res = op.code == OpCode::Eq ? (a.bits == b.bits) : (a.bits < b.bits);
          v = BitValue::constant(res ? 1 : 0, 1);
        }
        if (auto r = push(v); !r.ok()) return r;
        break;
      }
      case OpCode::Dup: {
        if (stack_.empty()) return fail(Error::StackUnderflow, "dup on empty stack");
        if (auto r = push(stack_.back()); !r.ok()) return r;
        break;
      }
      case OpCode::Drop: {
        BitValue v;
        if (auto r = pop(v); !r.ok()) return r;
        break;
      }
      case OpCode::Swap: {
        BitValue a, b;
        if (auto r = pop(b); !r.ok()) return r;
        if (auto r = pop(a); !r.ok()) return r;
        if (auto r = push(b); !r.ok()) return r;
        if (auto r = push(a); !r.ok()) return r;
        break;
      }
      case OpCode::Piece: {
        BitValue v;
        if (auto r = pop(v); !r.ok()) return r;
        uint32_t size = op.aux, offset = op.aux2;
        if (size == 0 || size > kMaxWidth ||
            uint64_t{offset} + size > v.width)
          return fail(Error::InvalidWidth, "piece out of range");
        // Big-endian targets count the offset from the MSB (DWARF rule).
        uint32_t lsbOffset = opts_.littleEndian ? offset : v.width - offset - size;
        if (auto r = push(v.slice(lsbOffset, size)); !r.ok()) return r;
        break;
      }
      case OpCode::Concat: {
        BitValue hi, lo;
        if (auto r = pop(lo); !r.ok()) return r;
        if (auto r = pop(hi); !r.ok()) return r;
        if (hi.width + lo.width > kMaxWidth)
          return fail(Error::InvalidWidth, "concatenation wider than 64 bits");
        if (auto r = push(BitValue::concat(hi, lo)); !r.ok()) return r;
        break;
      }
    }
  }

  if (stack_.empty())
    return fail(Error::StackUnderflow, "program ended with an empty stack");
  EvalResult r;
  r.value = stack_.back();
  r.opsUsed = spent_;
  return r;
}

}  // namespace debugloc
