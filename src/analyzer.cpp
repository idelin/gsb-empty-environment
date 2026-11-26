#include "pacbti/analyzer.hpp"

#include <algorithm>
#include <utility>
#include <vector>

namespace pacbti {

namespace {

constexpr std::uint32_t kBlockShift = 16u;
constexpr std::uint32_t kIndexMask = (1u << kBlockShift) - 1u;

std::uint32_t encodeSite(std::uint32_t blockId, std::uint32_t insIndex) {
    return (blockId << kBlockShift) | insIndex;
}

std::uint32_t decodeBlock(std::uint32_t siteId) {
    return siteId >> kBlockShift;
}

std::uint32_t decodeIndex(std::uint32_t siteId) {
    return siteId & kIndexMask;
}

}  // namespace

Analyzer::Analyzer(const ControlFlowGraph& cfg) : cfg_(cfg) {}

const char* Analyzer::findingName(FindingKind kind) {
    switch (kind) {
        case FindingKind::AuthBypassXpac:
            return "auth-bypass-xpaci";
        case FindingKind::PoisonedBranch:
            return "poisoned-pointer-branch";
        case FindingKind::SigningOracle:
            return "signing-oracle";
        case FindingKind::ModifierTypeConflict:
            return "modifier-type-conflict";
        case FindingKind::MissingBtiLanding:
            return "missing-bti-landing";
        case FindingKind::UntypedIndirectBranch:
            return "untyped-indirect-branch";
    }
    return "unknown";
}

const char* Analyzer::keyName(KeyDomain key) {
    switch (key) {
        case KeyDomain::IA:
            return "IA";
        case KeyDomain::IB:
            return "IB";
        case KeyDomain::DA:
            return "DA";
        case KeyDomain::DB:
            return "DB";
        case KeyDomain::None:
            return "NONE";
    }
    return "NONE";
}

const char* Analyzer::btiName(BtiKind kind) {
    switch (kind) {
        case BtiKind::BtiC:
            return "BTI c";
        case BtiKind::BtiJ:
            return "BTI j";
        case BtiKind::BtiJc:
            return "BTI jc";
        case BtiKind::None:
            return "none";
    }
    return "none";
}

SiteLoc Analyzer::locate(std::uint32_t blockId, std::uint32_t insIndex) const {
    SiteLoc loc{blockId, insIndex, ""};
    if (blockId < cfg_.blocks.size()) {
        const BasicBlock& block = cfg_.blocks[blockId];
        if (insIndex < block.instructions.size()) {
            loc.instructionName = block.instructions[insIndex].name;
        }
    }
    return loc;
}

std::vector<SiteLoc> Analyzer::traceOrigins(const OriginList& origins) const {
    std::vector<SiteLoc> trace;
    for (std::uint32_t siteId : origins) {
        trace.push_back(locate(decodeBlock(siteId), decodeIndex(siteId)));
    }
    return trace;
}

bool Analyzer::alreadyEmitted(FindingKind kind,
                              const SiteLoc& site,
                              std::uint16_t typeId,
                              std::uint64_t address) {
    DedupKey key{kind, site.block, site.instruction, typeId, address};
    return std::binary_search(emitted_.begin(), emitted_.end(), key);
}

void Analyzer::emit(Finding finding) {
    if (alreadyEmitted(finding.kind, finding.site, finding.typeId,
                       finding.targetAddress)) {
        return;
    }
    emitted_.push_back(DedupKey{finding.kind, finding.site.block,
                                finding.site.instruction, finding.typeId,
                                finding.targetAddress});
    std::sort(emitted_.begin(), emitted_.end());
    report_.findings.push_back(std::move(finding));
}

const AbstractState& Analyzer::blockEntry(std::uint32_t blockId) const {
    static const AbstractState empty = AbstractState::entry();
    auto it = entryStates_.find(blockId);
    if (it == entryStates_.end()) {
        return empty;
    }
    return it->second;
}

const AbstractState& Analyzer::blockExit(std::uint32_t blockId) const {
    static const AbstractState empty = AbstractState::entry();
    auto it = exitStates_.find(blockId);
    if (it == exitStates_.end()) {
        return empty;
    }
    return it->second;
}

AnalysisReport Analyzer::run() {
    report_ = AnalysisReport{};
    entryStates_.clear();
    exitStates_.clear();
    conflictSeen_.clear();
    emitted_.clear();

    if (cfg_.blocks.empty()) {
        return report_;
    }

    const std::size_t blockCount = cfg_.blocks.size();
    std::vector<std::uint8_t> inWorklist(blockCount, 0);
    std::vector<std::uint32_t> worklist;

    auto enqueue = [&](std::uint32_t blockId) {
        if (blockId < blockCount && inWorklist[blockId] == 0u) {
            inWorklist[blockId] = 1u;
            worklist.push_back(blockId);
        }
    };

    entryStates_[cfg_.entryBlock] = AbstractState::entry();
    enqueue(cfg_.entryBlock);

    while (!worklist.empty()) {
        std::uint32_t blockId = worklist.back();
        worklist.pop_back();
        inWorklist[blockId] = 0u;
        ++report_.fixpointIterations;

        analyzeBlock(blockId);
        const AbstractState& exitState = exitStates_[blockId];
        const BasicBlock& block = cfg_.blocks[blockId];

        auto propagate = [&](std::uint32_t successor) {
            if (successor >= blockCount) {
                return;
            }
            auto it = entryStates_.find(successor);
            if (it == entryStates_.end()) {
                entryStates_[successor] = exitState;
                enqueue(successor);
                return;
            }
            AbstractState joined = joinStates(it->second, exitState);
            if (!statesEqual(joined, it->second)) {
                it->second = joined;
                enqueue(successor);
            }
        };

        if (block.instructions.empty() ||
            block.instructions.back().kind != OpKind::CondBranch) {
            if (block.hasNext) {
                propagate(block.next);
            }
        } else {
            const Instruction& branch = block.instructions.back();
            propagate(branch.trueSucc);
            propagate(branch.falseSucc);
        }
    }

    return report_;
}

void Analyzer::analyzeBlock(std::uint32_t blockId) {
    AbstractState state = entryStates_[blockId];
    const BasicBlock& block = cfg_.blocks[blockId];
    for (std::uint32_t index = 0;
         index < static_cast<std::uint32_t>(block.instructions.size());
         ++index) {
        transfer(block.instructions[static_cast<std::size_t>(index)], blockId,
                 index, state);
    }
    exitStates_[blockId] = std::move(state);
}

void Analyzer::transfer(const Instruction& ins,
                        std::uint32_t blockId,
                        std::uint32_t insIndex,
                        AbstractState& state) {
    switch (ins.kind) {
        case OpKind::Input:
            {
                Label inputLabel = Label::raw(kInvalidType, true);
                inputLabel.defOrigins =
                        OriginList{encodeSite(blockId, insIndex)};
                state.setReg(ins.rd, inputLabel);
            }
            break;
        case OpKind::MarkTainted: {
            Label label = state.getReg(ins.rd);
            label.attackerTainted = true;
            label.defOrigins =
                    mergeOrigins(label.defOrigins,
                                 OriginList{encodeSite(blockId, insIndex)});
            state.setReg(ins.rd, label);
            break;
        }
        case OpKind::LoadRaw:
            {
                Label rawLabel = Label::raw(ins.typeId, false);
                rawLabel.defOrigins =
                        OriginList{encodeSite(blockId, insIndex)};
                state.setReg(ins.rd, rawLabel);
            }
            break;
        case OpKind::Mov:
            state.setReg(ins.rd, state.getReg(ins.rn));
            break;
        case OpKind::StackLoad:
            {
                Label loaded = state.getStack(ins.stackSlot);
                loaded.defOrigins =
                        mergeOrigins(loaded.defOrigins,
                                     OriginList{encodeSite(blockId, insIndex)});
                state.setReg(ins.rd, loaded);
            }
            break;
        case OpKind::StackStore: {
            const Label& stored = state.getReg(ins.rn);
            checkOracleSink(stored, locate(blockId, insIndex),
                            "stack-slot-" + std::to_string(ins.stackSlot));
            state.setStack(ins.stackSlot, stored);
            break;
        }
        case OpKind::ExtWrite:
            checkOracleSink(state.getReg(ins.rn), locate(blockId, insIndex),
                            ins.sink);
            break;
        case OpKind::PacSign:
            doSign(ins, blockId, insIndex, state);
            break;
        case OpKind::PacAuth:
            doAuth(ins, blockId, insIndex, state);
            break;
        case OpKind::Xpac:
            doXpac(ins, blockId, insIndex, state);
            break;
        case OpKind::IndirectBranch:
            doIndirectBranch(ins, blockId, insIndex, state);
            break;
        case OpKind::Bti:
        case OpKind::Fallthrough:
        case OpKind::CondBranch:
            break;
    }
}

void Analyzer::doSign(const Instruction& ins,
                      std::uint32_t blockId,
                      std::uint32_t insIndex,
                      AbstractState& state) {
    Label source = state.getReg(ins.rn);
    Label modifier = state.getReg(ins.rm);
    std::uint32_t site = encodeSite(blockId, insIndex);

    Label signedLabel;
    signedLabel.kind = LabelKind::Signed;
    signedLabel.key = ins.key;
    signedLabel.typeId = ins.typeId;
    signedLabel.attackerTainted =
            source.attackerTainted || modifier.attackerTainted;
    signedLabel.defOrigins = OriginList{site};
    signedLabel.modifierOrigins =
            modifier.defOrigins.empty()
                    ? (ins.rm == kSpReg ? OriginList{} : OriginList{site})
                    : modifier.defOrigins;
    signedLabel.signOrigins =
            mergeOrigins(source.signOrigins, OriginList{site});

    state.setReg(ins.rd, signedLabel);
    registerConflict(signedLabel, locate(blockId, insIndex));
}

void Analyzer::registerConflict(const Label& label, const SiteLoc& site) {
    if (label.typeId == kInvalidType || label.modifierOrigins.empty()) {
        return;
    }
    ConflictKey key{label.key, label.modifierOrigins};
    auto it = conflictSeen_.find(key);
    if (it == conflictSeen_.end()) {
        conflictSeen_[key] = ConflictInfo{label.typeId, site};
        return;
    }
    if (it->second.firstType == label.typeId) {
        return;
    }
    if (alreadyEmitted(FindingKind::ModifierTypeConflict, site, label.typeId,
                       0u)) {
        return;
    }
    Finding finding;
    finding.kind = FindingKind::ModifierTypeConflict;
    finding.site = site;
    finding.typeId = label.typeId;
    finding.key = label.key;
    finding.message =
            std::string("key ") + keyName(label.key) +
            " with identical modifier source signs distinct pointer types " +
            std::to_string(static_cast<unsigned>(it->second.firstType)) +
            " and " +
            std::to_string(static_cast<unsigned>(label.typeId));
    finding.trace.push_back(it->second.firstSite);
    finding.trace.push_back(site);
    emit(std::move(finding));
}

void Analyzer::doAuth(const Instruction& ins,
                      std::uint32_t blockId,
                      std::uint32_t insIndex,
                      AbstractState& state) {
    Label source = state.getReg(ins.rn);
    Label result;

    if (source.isPoisoned()) {
        result = Label::poisoned(source.poisonOrigins);
    } else if (source.isSigned() && source.key == ins.key) {
        result = Label::raw(source.typeId, source.attackerTainted);
        result.defOrigins = OriginList{encodeSite(blockId, insIndex)};
    } else if (source.isSigned()) {
        result = Label::poisoned(OriginList{encodeSite(blockId, insIndex)});
    } else {
        result = source;
    }

    state.setReg(ins.rd, result);
}

void Analyzer::doXpac(const Instruction& ins,
                      std::uint32_t blockId,
                      std::uint32_t insIndex,
                      AbstractState& state) {
    Label source = state.getReg(ins.rn);
    OriginList stripSite{encodeSite(blockId, insIndex)};
    if (source.isSigned()) {
        Label stripped = Label::raw(source.typeId, source.attackerTainted);
        stripped.poisonOrigins =
                mergeOrigins(source.poisonOrigins, stripSite);
        state.setReg(ins.rd, stripped);
    } else if (source.isPoisoned()) {
        state.setReg(ins.rd, Label::poisoned(
                                     mergeOrigins(source.poisonOrigins,
                                                  stripSite)));
    } else {
        state.setReg(ins.rd, source);
    }
}

bool Analyzer::isCallSite(const Instruction& ins) const {
    return ins.withLink;
}

bool Analyzer::landingMatches(bool callSite, BtiKind landing) const {
    if (landing == BtiKind::BtiJc) {
        return true;
    }
    if (callSite) {
        return landing == BtiKind::BtiJ;
    }
    return landing == BtiKind::BtiC;
}

std::uint16_t Analyzer::resolveBranchType(const Instruction& ins,
                                          const Label& label) const {
    if (ins.hintType != kInvalidType) {
        return ins.hintType;
    }
    if (label.typeId != kInvalidType) {
        return label.typeId;
    }
    return kInvalidType;
}

const CodePoint* Analyzer::findCodePoint(std::uint64_t address) const {
    for (const CodePoint& point : cfg_.codePoints) {
        if (point.address == address) {
            return &point;
        }
    }
    return nullptr;
}

void Analyzer::doIndirectBranch(const Instruction& ins,
                                std::uint32_t blockId,
                                std::uint32_t insIndex,
                                AbstractState& state) {
    SiteLoc site = locate(blockId, insIndex);
    Label source = state.getReg(ins.rn);

    if (ins.authenticate) {
        if (source.isPoisoned()) {
            return;
        }
        if (source.isSigned() && source.key == ins.key) {
            Label authenticated =
                    Label::raw(source.typeId, source.attackerTainted);
            state.setReg(ins.rn, authenticated);
            checkBti(ins, site, authenticated);
            return;
        }
        if (source.isSigned()) {
            Label poisoned =
                    Label::poisoned(OriginList{encodeSite(blockId, insIndex)});
            state.setReg(ins.rn, poisoned);
            return;
        }
        checkBti(ins, site, source);
        return;
    }

    if (source.isPoisoned()) {
        bool stripSeen = false;
        bool authFailureSeen = false;
        for (std::uint32_t origin : source.poisonOrigins) {
            const BasicBlock& originBlock = cfg_.blocks[decodeBlock(origin)];
            const Instruction& originIns =
                    originBlock.instructions[decodeIndex(origin)];
            if (originIns.kind == OpKind::Xpac) {
                stripSeen = true;
            }
            if (originIns.kind == OpKind::PacAuth) {
                authFailureSeen = true;
            }
        }

        if (stripSeen && authFailureSeen) {
            Finding finding;
            finding.kind = FindingKind::AuthBypassXpac;
            finding.site = site;
            finding.message =
                    "authentication-failure poison code survives XPACI and "
                    "reaches an indirect branch without a fault";
            finding.trace = traceOrigins(source.poisonOrigins);
            emit(std::move(finding));
        } else {
            Finding finding;
            finding.kind = FindingKind::PoisonedBranch;
            finding.site = site;
            finding.message =
                    "poisoned pointer reaches an indirect branch";
            finding.trace = traceOrigins(source.poisonOrigins);
            emit(std::move(finding));
        }
        return;
    }

    checkBti(ins, site, source);
}

void Analyzer::checkBti(const Instruction& ins,
                        const SiteLoc& site,
                        const Label& label) {
    std::uint16_t typeId = resolveBranchType(ins, label);
    if (typeId == kInvalidType) {
        if (!alreadyEmitted(FindingKind::UntypedIndirectBranch, site,
                            kInvalidType, 0u)) {
            Finding finding;
            finding.kind = FindingKind::UntypedIndirectBranch;
            finding.site = site;
            finding.message =
                    "indirect branch has no resolvable function pointer "
                    "type; BTI reachability cannot be bounded";
            emit(std::move(finding));
        }
        return;
    }

    const FunctionPointerType* type = nullptr;
    for (const FunctionPointerType& candidate : cfg_.types) {
        if (candidate.id == typeId) {
            type = &candidate;
            break;
        }
    }
    if (type == nullptr) {
        if (!alreadyEmitted(FindingKind::UntypedIndirectBranch, site, typeId,
                            0u)) {
            Finding finding;
            finding.kind = FindingKind::UntypedIndirectBranch;
            finding.site = site;
            finding.typeId = typeId;
            finding.message = "function pointer type " +
                              std::to_string(static_cast<unsigned>(typeId)) +
                              " is absent from the type table";
            emit(std::move(finding));
        }
        return;
    }

    bool callSite = isCallSite(ins);
    for (std::uint64_t address : type->targets) {
        const CodePoint* point = findCodePoint(address);
        if (point == nullptr) {
            Finding finding;
            finding.kind = FindingKind::MissingBtiLanding;
            finding.site = site;
            finding.typeId = typeId;
            finding.targetAddress = address;
            finding.message =
                    "reachable target has no code-point description";
            emit(std::move(finding));
            continue;
        }
        if (!point->guardedPage) {
            continue;
        }
        if (landingMatches(callSite, point->landing)) {
            continue;
        }
        Finding finding;
        finding.kind = FindingKind::MissingBtiLanding;
        finding.site = site;
        finding.typeId = typeId;
        finding.targetAddress = address;
        finding.message =
                std::string("guarded target 0x") +
                std::to_string(static_cast<long long>(address)) +
                " requires " + (callSite ? "BTI j/jc" : "BTI c/jc") +
                " but landing is " + btiName(point->landing);
        emit(std::move(finding));
    }
}

void Analyzer::checkOracleSink(const Label& label,
                               const SiteLoc& site,
                               const std::string& sink) {
    if (!label.attackerTainted || !label.hasSignOrigin()) {
        return;
    }
    if (alreadyEmitted(FindingKind::SigningOracle, site, kInvalidType, 0u)) {
        return;
    }
    Finding finding;
    finding.kind = FindingKind::SigningOracle;
    finding.site = site;
    finding.typeId = label.typeId;
    finding.key = label.key;
    finding.message =
            "attacker-controlled value reaches PAC signing and the signed "
            "pointer flows to externally observable sink '" + sink + "'";
    finding.trace = traceOrigins(label.signOrigins);
    emit(std::move(finding));
}

}  // namespace pacbti
