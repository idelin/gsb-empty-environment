#include "debugloc/debugloc.hpp"

#include <limits>

namespace debugloc {

const char* toString(Error e) {
    switch (e) {
    case Error::None: return "none";
    case Error::BudgetExhausted: return "budget exhausted";
    case Error::StackUnderflow: return "stack underflow";
    case Error::AddressOverflow: return "address overflow";
    case Error::UnknownAddress: return "unknown address";
    case Error::RegisterUnavailable: return "register unavailable";
    case Error::InvalidBitRange: return "invalid bit range";
    }
    return "unknown error";
}

// ---------------------------------------------------------------- Value

Value Value::fromUint(uint64_t v, size_t bits) {
    Value r;
    r.bits_ = bits;
    size_t nbytes = (bits + 7) / 8;
    r.data_.assign(nbytes, 0);
    r.known_.assign(nbytes, 0xFF);
    for (size_t i = 0; i < nbytes && i < 8; ++i)
        r.data_[i] = static_cast<uint8_t>(v >> (8 * i));
    if (bits % 8 != 0) {
        r.known_.back() = static_cast<uint8_t>((1u << (bits % 8)) - 1);
        r.data_.back() &= r.known_.back();
    }
    return r;
}

Value Value::unknown(size_t bits) {
    Value r;
    r.bits_ = bits;
    size_t nbytes = (bits + 7) / 8;
    r.data_.assign(nbytes, 0);
    r.known_.assign(nbytes, 0);
    return r;
}

Value Value::bytes(std::vector<uint8_t> data, std::vector<uint8_t> knownMask) {
    Value r;
    r.bits_ = data.size() * 8;
    r.data_ = std::move(data);
    r.known_ = std::move(knownMask);
    return r;
}

bool Value::bit(size_t i) const {
    return (data_[i / 8] >> (i % 8)) & 1;
}

bool Value::bitKnown(size_t i) const {
    return (known_[i / 8] >> (i % 8)) & 1;
}

void Value::setKnownBit(size_t i, bool b) {
    if (b)
        data_[i / 8] |= static_cast<uint8_t>(1u << (i % 8));
    else
        data_[i / 8] &= static_cast<uint8_t>(~(1u << (i % 8)));
    known_[i / 8] |= static_cast<uint8_t>(1u << (i % 8));
}

bool Value::fullyKnown() const {
    for (size_t i = 0; i < known_.size(); ++i) {
        uint8_t expect = 0xFF;
        if (i == known_.size() - 1 && bits_ % 8 != 0)
            expect = static_cast<uint8_t>((1u << (bits_ % 8)) - 1);
        if (known_[i] != expect)
            return false;
    }
    return true;
}

std::optional<uint64_t> Value::toUint64() const {
    if (bits_ > 64 || !fullyKnown())
        return std::nullopt;
    uint64_t v = 0;
    for (size_t i = 0; i < data_.size() && i < 8; ++i)
        v |= static_cast<uint64_t>(data_[i]) << (8 * i);
    return v;
}

Value Value::slice(size_t offset, size_t size) const {
    Value r = unknown(size);
    for (size_t i = 0; i < size; ++i) {
        size_t src = offset + i;
        if (bitKnown(src))
            r.setKnownBit(i, bit(src));
    }
    return r;
}

Value Value::concat(const Value& low, const Value& high) {
    Value r = unknown(low.bits_ + high.bits_);
    for (size_t i = 0; i < low.bits_; ++i)
        if (low.bitKnown(i))
            r.setKnownBit(i, low.bit(i));
    for (size_t i = 0; i < high.bits_; ++i)
        if (high.bitKnown(i))
            r.setKnownBit(low.bits_ + i, high.bit(i));
    return r;
}

Value Value::byteReversed() const {
    Value r = *this;
    for (size_t i = 0; i < data_.size(); ++i) {
        r.data_[i] = data_[data_.size() - 1 - i];
        r.known_[i] = known_[known_.size() - 1 - i];
    }
    return r;
}

// ------------------------------------------------------------ Evaluator

Evaluator::Evaluator(const Memory& mem, const Registers& regs, EvalOptions opts)
    : mem_(mem), regs_(regs), opts_(opts) {}

Result<Value> Evaluator::pop() {
    if (stack_.empty())
        return Result<Value>::fail(Error::StackUnderflow);
    Value v = std::move(stack_.back());
    stack_.pop_back();
    return Result<Value>::ok(std::move(v));
}

Result<Value> Evaluator::run(const std::vector<Instr>& program) {
    stack_.clear();
    ops_ = 0;
    endian_ = opts_.endian;
    for (const Instr& ins : program) {
        if (ops_ >= opts_.maxOps)
            return Result<Value>::fail(Error::BudgetExhausted);
        ++ops_;
        if (Error e = exec(ins); e != Error::None)
            return Result<Value>::fail(e);
    }
    if (stack_.empty())
        return Result<Value>::fail(Error::StackUnderflow);
    return Result<Value>::ok(std::move(stack_.back()));
}

Error Evaluator::exec(const Instr& ins) {
    switch (ins.op) {
    case OpCode::PushConst:
        stack_.push_back(Value::fromUint64(ins.imm));
        return Error::None;

    case OpCode::LoadReg: {
        auto bytes = regs_.read(static_cast<uint32_t>(ins.imm));
        if (!bytes)
            return Error::RegisterUnavailable;
        stack_.push_back(
            Value::bytes(*bytes, std::vector<uint8_t>(bytes->size(), 0xFF)));
        return Error::None;
    }

    case OpCode::Deref: {
        auto addrV = pop();
        if (!addrV)
            return addrV.error();
        auto addr = addrV.value().toUint64();
        if (!addr)
            return Error::UnknownAddress;
        uint64_t size = ins.imm;
        // Checked address-range computation: addr + size - 1 must not wrap.
        if (size > 0 && *addr > std::numeric_limits<uint64_t>::max() - (size - 1))
            return Error::AddressOverflow;
        auto bytes = mem_.read(*addr, static_cast<size_t>(size));
        std::vector<uint8_t> data(size, 0), known(size, 0);
        for (size_t i = 0; i < size && i < bytes.size(); ++i)
            if (bytes[i]) {
                data[i] = *bytes[i];
                known[i] = 0xFF;
            }
        Value v = Value::bytes(std::move(data), std::move(known));
        if (endian_ == Endian::Big)
            v = v.byteReversed();
        stack_.push_back(std::move(v));
        return Error::None;
    }

    case OpCode::Add:
    case OpCode::Sub:
    case OpCode::Mul: {
        auto b = pop();
        if (!b)
            return b.error();
        auto a = pop();
        if (!a)
            return a.error();
        auto ai = a.value().toUint64();
        auto bi = b.value().toUint64();
        // Unknown operand: propagate an unknown 64-bit result.
        if (!ai || !bi) {
            stack_.push_back(Value::unknown(64));
            return Error::None;
        }
        uint64_t res = 0;
        switch (ins.op) {
        case OpCode::Add:
            res = *ai + *bi;
            if (res < *ai)
                return Error::AddressOverflow;
            break;
        case OpCode::Sub:
            if (*bi > *ai)
                return Error::AddressOverflow;
            res = *ai - *bi;
            break;
        case OpCode::Mul:
            if (*ai != 0 && *bi > std::numeric_limits<uint64_t>::max() / *ai)
                return Error::AddressOverflow;
            res = *ai * *bi;
            break;
        default:
            break;
        }
        stack_.push_back(Value::fromUint64(res));
        return Error::None;
    }

    case OpCode::BitField: {
        auto v = pop();
        if (!v)
            return v.error();
        uint64_t offset = ins.imm, size = ins.imm2;
        if (size > v.value().bitSize() || offset > v.value().bitSize() - size)
            return Error::InvalidBitRange;
        stack_.push_back(v.value().slice(static_cast<size_t>(offset),
                                         static_cast<size_t>(size)));
        return Error::None;
    }

    case OpCode::Concat: {
        auto high = pop();
        if (!high)
            return high.error();
        auto low = pop();
        if (!low)
            return low.error();
        stack_.push_back(Value::concat(low.value(), high.value()));
        return Error::None;
    }

    case OpCode::SetEndian:
        endian_ = ins.imm ? Endian::Big : Endian::Little;
        return Error::None;
    }
    return Error::None;
}

} // namespace debugloc
