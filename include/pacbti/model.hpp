#ifndef PACBTI_MODEL_HPP
#define PACBTI_MODEL_HPP

#include <cstdint>
#include <string>
#include <vector>

#include "pacbti/label.hpp"

namespace pacbti {

enum class OpKind : std::uint8_t {
    Input,
    MarkTainted,
    LoadRaw,
    Mov,
    PacSign,
    PacAuth,
    Xpac,
    IndirectBranch,
    Bti,
    StackLoad,
    StackStore,
    ExtWrite,
    CondBranch,
    Fallthrough
};

enum class BtiKind : std::uint8_t {
    None = 0,
    BtiC,
    BtiJ,
    BtiJc
};

struct Instruction {
    OpKind kind = OpKind::Fallthrough;
    std::string name;

    std::uint8_t rd = 0;
    std::uint8_t rn = 0;
    std::uint8_t rm = 0;

    KeyDomain key = KeyDomain::None;
    bool authenticate = false;
    bool branch = false;
    bool withLink = false;

    std::uint16_t typeId = kInvalidType;
    std::uint16_t stackSlot = 0;
    std::uint16_t hintType = kInvalidType;
    BtiKind bti = BtiKind::None;

    std::uint32_t trueSucc = 0u;
    std::uint32_t falseSucc = 0u;

    std::string sink;
};

namespace build {

inline Instruction input(std::uint8_t dest, std::string name = "input") {
    Instruction ins;
    ins.kind = OpKind::Input;
    ins.name = std::move(name);
    ins.rd = dest;
    return ins;
}

inline Instruction markTainted(std::uint8_t dest, std::string name = "taint") {
    Instruction ins;
    ins.kind = OpKind::MarkTainted;
    ins.name = std::move(name);
    ins.rd = dest;
    return ins;
}

inline Instruction loadRaw(std::uint8_t dest,
                           std::uint16_t ptrType,
                           std::string name = "loadraw") {
    Instruction ins;
    ins.kind = OpKind::LoadRaw;
    ins.name = std::move(name);
    ins.rd = dest;
    ins.typeId = ptrType;
    return ins;
}

inline Instruction mov(std::uint8_t dest, std::uint8_t src, std::string name = "mov") {
    Instruction ins;
    ins.kind = OpKind::Mov;
    ins.name = std::move(name);
    ins.rd = dest;
    ins.rn = src;
    return ins;
}

inline Instruction pacSign(std::uint8_t dest,
                           std::uint8_t source,
                           std::uint8_t modifier,
                           KeyDomain domain,
                           std::uint16_t ptrType,
                           std::string name) {
    Instruction ins;
    ins.kind = OpKind::PacSign;
    ins.name = std::move(name);
    ins.rd = dest;
    ins.rn = source;
    ins.rm = modifier;
    ins.key = domain;
    ins.typeId = ptrType;
    return ins;
}

inline Instruction pacAuth(std::uint8_t dest,
                           std::uint8_t source,
                           std::uint8_t modifier,
                           KeyDomain domain,
                           std::uint16_t ptrType,
                           std::string name) {
    Instruction ins;
    ins.kind = OpKind::PacAuth;
    ins.name = std::move(name);
    ins.rd = dest;
    ins.rn = source;
    ins.rm = modifier;
    ins.key = domain;
    ins.authenticate = true;
    ins.typeId = ptrType;
    return ins;
}

inline Instruction xpac(std::uint8_t dest,
                        std::uint8_t source,
                        KeyDomain domain,
                        std::string name = "xpac") {
    Instruction ins;
    ins.kind = OpKind::Xpac;
    ins.name = std::move(name);
    ins.rd = dest;
    ins.rn = source;
    ins.key = domain;
    return ins;
}

inline Instruction indirectBranch(std::uint8_t target,
                                  KeyDomain authKey,
                                  bool withLink,
                                  std::uint16_t hintType,
                                  std::string name) {
    Instruction ins;
    ins.kind = OpKind::IndirectBranch;
    ins.name = std::move(name);
    ins.rn = target;
    ins.key = authKey;
    ins.branch = true;
    ins.hintType = hintType;
    if (authKey != KeyDomain::None) {
        ins.authenticate = true;
    }
    ins.withLink = withLink;
    return ins;
}

inline Instruction bti(BtiKind kind, std::string name = "bti") {
    Instruction ins;
    ins.kind = OpKind::Bti;
    ins.name = std::move(name);
    ins.bti = kind;
    return ins;
}

inline Instruction authenticatedReturn(std::uint8_t target,
                                       KeyDomain authKey,
                                       std::string name) {
    Instruction ins;
    ins.kind = OpKind::IndirectBranch;
    ins.name = std::move(name);
    ins.rn = target;
    ins.key = authKey;
    ins.branch = true;
    ins.withLink = false;
    ins.authenticate = true;
    ins.hintType = kInvalidType;
    return ins;
}

inline Instruction stackStore(std::uint8_t source,
                              std::uint16_t slot,
                              std::string name = "str") {
    Instruction ins;
    ins.kind = OpKind::StackStore;
    ins.name = std::move(name);
    ins.rn = source;
    ins.stackSlot = slot;
    return ins;
}

inline Instruction stackLoad(std::uint8_t dest,
                             std::uint16_t slot,
                             std::string name = "ldr") {
    Instruction ins;
    ins.kind = OpKind::StackLoad;
    ins.name = std::move(name);
    ins.rd = dest;
    ins.stackSlot = slot;
    return ins;
}

inline Instruction extWrite(std::uint8_t source, std::string sinkName) {
    Instruction ins;
    ins.kind = OpKind::ExtWrite;
    ins.name = "extwrite";
    ins.rn = source;
    ins.sink = std::move(sinkName);
    return ins;
}

inline Instruction condBranch(std::uint32_t onTrue,
                              std::uint32_t onFalse,
                              std::string name = "cbz") {
    Instruction ins;
    ins.kind = OpKind::CondBranch;
    ins.name = std::move(name);
    ins.trueSucc = onTrue;
    ins.falseSucc = onFalse;
    return ins;
}

inline Instruction fallthrough(std::string name = "nop") {
    Instruction ins;
    ins.kind = OpKind::Fallthrough;
    ins.name = std::move(name);
    return ins;
}

}  // namespace build

struct BasicBlock {
    std::uint32_t id = 0;
    std::vector<Instruction> instructions;
    std::uint32_t next = 0u;
    bool hasNext = false;
};

struct CodePoint {
    std::uint64_t address = 0;
    BtiKind landing = BtiKind::None;
    bool guardedPage = false;
};

struct FunctionPointerType {
    std::uint16_t id = 0;
    std::string name;
    std::vector<std::uint64_t> targets;
};

struct ControlFlowGraph {
    std::vector<BasicBlock> blocks;
    std::vector<CodePoint> codePoints;
    std::vector<FunctionPointerType> types;
    std::uint32_t entryBlock = 0;
};

}  // namespace pacbti

#endif
