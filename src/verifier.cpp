#include "pacbti/verifier.hpp"

#include <algorithm>
#include <cctype>
#include <deque>
#include <exception>
#include <limits>
#include <map>
#include <set>
#include <sstream>
#include <stdexcept>
#include <string>
#include <utility>
#include <vector>

namespace pacbti {
namespace {

std::string trim(const std::string& input) {
    std::size_t begin = 0;
    while (begin < input.size() &&
           std::isspace(static_cast<unsigned char>(input[begin])) != 0) {
        ++begin;
    }
    std::size_t end = input.size();
    while (end > begin &&
           std::isspace(static_cast<unsigned char>(input[end - 1])) != 0) {
        --end;
    }
    return input.substr(begin, end - begin);
}

std::string lower(std::string input) {
    for (char& ch : input) {
        ch = static_cast<char>(
            std::tolower(static_cast<unsigned char>(ch)));
    }
    return input;
}

bool startsWith(const std::string& value, const std::string& prefix) {
    return value.size() >= prefix.size() &&
           value.compare(0, prefix.size(), prefix) == 0;
}

std::string stripComment(const std::string& input) {
    const std::size_t slash = input.find("//");
    const std::size_t semicolon = input.find(';');
    std::size_t end = input.size();
    if (slash != std::string::npos) {
        end = std::min(end, slash);
    }
    if (semicolon != std::string::npos) {
        end = std::min(end, semicolon);
    }
    return trim(input.substr(0, end));
}

std::vector<std::string> splitOnComma(const std::string& input) {
    std::vector<std::string> result;
    std::size_t begin = 0;
    while (begin <= input.size()) {
        const std::size_t comma = input.find(',', begin);
        const std::size_t end =
            (comma == std::string::npos) ? input.size() : comma;
        const std::string part = trim(input.substr(begin, end - begin));
        if (!part.empty()) {
            result.push_back(part);
        }
        if (comma == std::string::npos) {
            break;
        }
        begin = comma + 1;
    }
    return result;
}

std::string normalizeRegister(const std::string& input) {
    const std::string reg = lower(trim(input));
    if (reg == "lr") {
        return "x30";
    }
    if (reg == "fp") {
        return "x29";
    }
    if (reg == "ip0") {
        return "x16";
    }
    if (reg == "ip1") {
        return "x17";
    }
    if (reg == "xzr" || reg == "wzr") {
        return "xzr";
    }
    if (reg.size() >= 2 && reg[0] == 'w' &&
        std::isdigit(static_cast<unsigned char>(reg[1])) != 0) {
        if (reg.substr(1) == "30") {
            return "x30";
        }
    }
    return reg;
}

std::int64_t parseImmediate(const std::string& input) {
    std::string value = trim(input);
    if (!value.empty() && value[0] == '#') {
        value = trim(value.substr(1));
    }
    if (value.empty()) {
        return 0;
    }
    std::size_t consumed = 0;
    const long long parsed = std::stoll(value, &consumed, 0);
    if (consumed != value.size()) {
        throw std::invalid_argument("invalid immediate: " + input);
    }
    return static_cast<std::int64_t>(parsed);
}

std::int64_t parseImmediateOrUnknown(const std::string& input, bool& known) {
    try {
        return parseImmediate(input);
    } catch (const std::exception&) {
        known = false;
        return 0;
    }
}

bool checkedAdd(std::int64_t left, std::int64_t right,
                std::int64_t& result) {
    if (right > 0 &&
        left > std::numeric_limits<std::int64_t>::max() - right) {
        return false;
    }
    if (right < 0 &&
        left < std::numeric_limits<std::int64_t>::min() - right) {
        return false;
    }
    result = left + right;
    return true;
}

bool checkedNegate(std::int64_t value, std::int64_t& result) {
    if (value == std::numeric_limits<std::int64_t>::min()) {
        return false;
    }
    result = -value;
    return true;
}

bool isConditional(Opcode opcode) {
    return opcode == Opcode::BCond || opcode == Opcode::Cbz ||
           opcode == Opcode::Cbnz || opcode == Opcode::Tbz ||
           opcode == Opcode::Tbnz;
}

bool isIndirectBranch(Opcode opcode) {
    return opcode == Opcode::Blr || opcode == Opcode::Br;
}

bool isStore(Opcode opcode) {
    return opcode == Opcode::Str || opcode == Opcode::Stp;
}

bool isPair(Opcode opcode) {
    return opcode == Opcode::Ldp || opcode == Opcode::Stp;
}

bool isAddressTrustedRegister(const std::string& reg) {
    const std::string normalized = normalizeRegister(reg);
    return normalized == "x16" || normalized == "x17";
}

struct MemoryOperands {
    std::vector<std::string> registers;
    std::string base;
    std::int64_t offset = 0;
    bool offset_known = true;
    std::int64_t writeback = 0;
    bool writeback_known = true;
    MemoryMode mode = MemoryMode::Offset;
};

MemoryOperands parseMemoryOperands(const std::string& input) {
    MemoryOperands result;
    const std::size_t open = input.find('[');
    const std::size_t close = input.find(']', open == std::string::npos ? 0 : open);
    if (open == std::string::npos || close == std::string::npos || close < open) {
        throw std::invalid_argument("missing memory operand");
    }

    std::string prefix = trim(input.substr(0, open));
    if (!prefix.empty() && prefix.back() == ',') {
        prefix.pop_back();
    }
    result.registers = splitOnComma(prefix);
    if (result.registers.empty()) {
        throw std::invalid_argument("missing transfer register");
    }

    const std::string inside = trim(input.substr(open + 1, close - open - 1));
    const std::vector<std::string> insideParts = splitOnComma(inside);
    if (insideParts.empty()) {
        throw std::invalid_argument("missing memory base");
    }
    result.base = normalizeRegister(insideParts[0]);
    if (insideParts.size() >= 2) {
        result.offset =
            parseImmediateOrUnknown(insideParts[1], result.offset_known);
    }

    const std::string suffix = trim(input.substr(close + 1));
    if (suffix == "!") {
        result.mode = MemoryMode::PreIndex;
        result.writeback = result.offset;
        result.writeback_known = result.offset_known;
    } else if (!suffix.empty()) {
        if (suffix[0] != ',') {
            throw std::invalid_argument("invalid memory writeback");
        }
        result.writeback = parseImmediateOrUnknown(
            suffix.substr(1), result.writeback_known);
        result.mode = MemoryMode::PostIndex;
    }
    return result;
}

std::int64_t applyShift(std::int64_t value, const std::vector<std::string>& operands) {
    std::int64_t result = value;
    for (std::size_t index = 0; index < operands.size(); ++index) {
        const std::string token = lower(trim(operands[index]));
        if (!startsWith(token, "lsl")) {
            continue;
        }
        std::string amountText = trim(token.substr(3));
        if (amountText.empty() && index + 1 < operands.size()) {
            amountText = operands[index + 1];
        }
        const std::int64_t amount = parseImmediate(amountText);
        if (amount < 0 || amount > 30) {
            throw std::invalid_argument("unsupported shift amount");
        }
        for (std::int64_t step = 0; step < amount; ++step) {
            result *= 2;
        }
    }
    return result;
}

const char* pacKeyText(PacKey key) {
    switch (key) {
        case PacKey::None:
            return "none";
        case PacKey::IA:
            return "IA";
        case PacKey::IB:
            return "IB";
        case PacKey::DA:
            return "DA";
        case PacKey::DB:
            return "DB";
    }
    return "none";
}

enum class RegKind {
    Unknown,
    Signed,
    Authenticated,
    Stripped
};

const char* regKindText(RegKind kind) {
    switch (kind) {
        case RegKind::Unknown:
            return "unknown";
        case RegKind::Signed:
            return "signed";
        case RegKind::Authenticated:
            return "authenticated";
        case RegKind::Stripped:
            return "stripped";
    }
    return "unknown";
}

struct RegisterValue {
    RegKind kind = RegKind::Unknown;
    PacKey key = PacKey::None;
    std::string modifier_register;
    bool signer_sp_known = false;
    std::int64_t signer_sp_offset = 0;
};

struct SignRecord {
    PacKey key = PacKey::None;
    std::string modifier_register;
    bool sp_modifier = false;
    bool sp_known = false;
    std::int64_t sp_offset = 0;
};

struct State {
    std::int64_t sp_offset = 0;
    bool sp_known = true;
    bool frame_pointer_known = false;
    std::int64_t frame_pointer_sp_offset = 0;
    std::map<std::string, RegisterValue> registers;
    std::map<std::int64_t, RegisterValue> stack_slots;
    std::map<std::string, SignRecord> signatures;
    std::map<std::string, std::size_t> pending_authentications;
    bool attacker_branch_in_window = false;
};

void appendBool(std::string& output, bool value) {
    output += value ? '1' : '0';
}

void appendInt(std::string& output, std::int64_t value) {
    output += std::to_string(value);
}

std::string stateKey(const State& state) {
    std::string output;
    output.reserve(256);
    output += "s";
    appendInt(output, state.sp_offset);
    appendBool(output, state.sp_known);
    output += "f";
    appendBool(output, state.frame_pointer_known);
    appendInt(output, state.frame_pointer_sp_offset);
    output += "|r";
    for (const auto& entry : state.registers) {
        output += entry.first;
        output += ':';
        output += regKindText(entry.second.kind);
        output += ':';
        output += pacKeyText(entry.second.key);
        output += ':';
        output += entry.second.modifier_register;
        output += ':';
        appendBool(output, entry.second.signer_sp_known);
        appendInt(output, entry.second.signer_sp_offset);
        output += ';';
    }
    output += "|m";
    for (const auto& entry : state.stack_slots) {
        appendInt(output, entry.first);
        output += ':';
        output += regKindText(entry.second.kind);
        output += ':';
        output += pacKeyText(entry.second.key);
        output += ':';
        output += entry.second.modifier_register;
        output += ':';
        appendBool(output, entry.second.signer_sp_known);
        appendInt(output, entry.second.signer_sp_offset);
        output += ';';
    }
    output += "|g";
    for (const auto& entry : state.signatures) {
        output += entry.first;
        output += ':';
        output += pacKeyText(entry.second.key);
        output += ':';
        output += entry.second.modifier_register;
        output += ':';
        appendBool(output, entry.second.sp_modifier);
        appendBool(output, entry.second.sp_known);
        appendInt(output, entry.second.sp_offset);
        output += ';';
    }
    output += "|p";
    for (const auto& entry : state.pending_authentications) {
        output += entry.first;
        output += ';';
    }
    output += "|a";
    appendBool(output, state.attacker_branch_in_window);
    return output;
}

std::string siteKey(const std::string& function, const std::string& block,
                    std::size_t instructionIndex) {
    return function + "\x1f" + block + "\x1f" +
           std::to_string(instructionIndex);
}

std::string siteKey(const BranchSite& site) {
    return siteKey(site.function, site.block, site.instruction_index);
}

bool isDataKey(PacKey key) {
    return key == PacKey::DA || key == PacKey::DB;
}

bool isInstructionKey(PacKey key) {
    return key == PacKey::IA || key == PacKey::IB;
}

const RegisterValue& registerValue(const State& state, const std::string& reg) {
    static const RegisterValue unknown;
    const auto found = state.registers.find(normalizeRegister(reg));
    if (found == state.registers.end()) {
        return unknown;
    }
    return found->second;
}

void erasePendingAuthentication(State& state, const std::string& reg) {
    state.pending_authentications.erase(normalizeRegister(reg));
    if (state.pending_authentications.empty()) {
        state.attacker_branch_in_window = false;
    }
}

void assignRegister(State& state, const std::string& reg, const RegisterValue& value) {
    const std::string normalized = normalizeRegister(reg);
    if (normalized.empty() || normalized == "xzr") {
        return;
    }
    state.registers[normalized] = value;
    state.signatures.erase(normalized);
    erasePendingAuthentication(state, normalized);
}

void clearRegister(State& state, const std::string& reg) {
    const std::string normalized = normalizeRegister(reg);
    if (normalized.empty() || normalized == "xzr") {
        return;
    }
    state.registers.erase(normalized);
    state.signatures.erase(normalized);
    erasePendingAuthentication(state, normalized);
}

void clearCallerLinkRegister(State& state) {
    clearRegister(state, "x30");
}

class Collector {
public:
    void add(Diagnostic diagnostic) {
        const std::string key =
            std::string(toString(diagnostic.code)) + "\x1f" +
            diagnostic.function + "\x1f" + diagnostic.block + "\x1f" +
            std::to_string(diagnostic.instruction_index) + "\x1f" +
            diagnostic.message;
        const auto found = indices_.find(key);
        if (found == indices_.end()) {
            indices_[key] = diagnostics_.size();
            diagnostics_.push_back(std::move(diagnostic));
            return;
        }
        Diagnostic& existing = diagnostics_[found->second];
        if (diagnostic.path.size() < existing.path.size()) {
            existing = std::move(diagnostic);
        }
    }

    std::vector<Diagnostic> take() {
        return std::move(diagnostics_);
    }

private:
    std::vector<Diagnostic> diagnostics_;
    std::map<std::string, std::size_t> indices_;
};

struct ProgramIndex {
    std::map<std::string, const Function*> functions;
    std::map<std::string, std::map<std::string, const BasicBlock*>> blocks;

    explicit ProgramIndex(const Program& program) {
        for (const Function& function : program.functions) {
            functions[function.name] = &function;
            auto& functionBlocks = blocks[function.name];
            for (const BasicBlock& block : function.blocks) {
                functionBlocks[block.name] = &block;
            }
        }
    }

    const Function* function(const std::string& name) const {
        const auto found = functions.find(name);
        return found == functions.end() ? nullptr : found->second;
    }

    const BasicBlock* block(const std::string& functionName,
                            const std::string& blockName) const {
        const auto functionFound = blocks.find(functionName);
        if (functionFound == blocks.end()) {
            return nullptr;
        }
        const auto blockFound = functionFound->second.find(blockName);
        return blockFound == functionFound->second.end() ? nullptr
                                                         : blockFound->second;
    }

    const Instruction* instruction(const BranchSite& site) const {
        const BasicBlock* basicBlock = block(site.function, site.block);
        if (basicBlock == nullptr ||
            site.instruction_index >= basicBlock->instructions.size()) {
            return nullptr;
        }
        return &basicBlock->instructions[site.instruction_index];
    }
};

PathStep pathStep(const Function& function, const BasicBlock& block,
                  std::size_t index, const Instruction& instruction) {
    PathStep step;
    step.function = function.name;
    step.block = block.name;
    step.instruction_index = index;
    step.text = instruction.toString();
    return step;
}

std::vector<std::string> operandsAfterMnemonic(const std::string& rest) {
    return splitOnComma(rest);
}

Opcode genericPacOpcode(const std::string& mnemonic, bool& matched) {
    matched = true;
    if (mnemonic == "pacia") {
        return Opcode::Pacia;
    }
    if (mnemonic == "pacib") {
        return Opcode::Pacib;
    }
    if (mnemonic == "pacda") {
        return Opcode::Pacda;
    }
    if (mnemonic == "pacdb") {
        return Opcode::Pacdb;
    }
    if (mnemonic == "autia") {
        return Opcode::Autia;
    }
    if (mnemonic == "autib") {
        return Opcode::Autib;
    }
    if (mnemonic == "autda") {
        return Opcode::Autda;
    }
    if (mnemonic == "autdb") {
        return Opcode::Autdb;
    }
    matched = false;
    return Opcode::Other;
}

PacKey keyForSign(Opcode opcode) {
    switch (opcode) {
        case Opcode::Paciasp:
        case Opcode::Pacia:
        case Opcode::Autia:
            return PacKey::IA;
        case Opcode::Pacibsp:
        case Opcode::Pacib:
        case Opcode::Autib:
            return PacKey::IB;
        case Opcode::Pacda:
        case Opcode::Autda:
            return PacKey::DA;
        case Opcode::Pacdb:
        case Opcode::Autdb:
            return PacKey::DB;
        default:
            return PacKey::None;
    }
}

struct BranchObservation {
    const Function* function = nullptr;
    const BasicBlock* block = nullptr;
    std::size_t instruction_index = 0;
    const Instruction* instruction = nullptr;
    std::vector<PathStep> path;
};

struct WorkItem {
    std::string block;
    std::size_t instruction_index = 0;
    State state;
    std::vector<PathStep> path;
};

class Verifier {
public:
    Verifier(const Program& program, const Options& options)
        : program_(program), options_(options), index_(program) {}

    VerificationReport run() {
        validateExports();
        validateExceptionReturns();
        for (const Function& function : program_.functions) {
            analyzeFunction(function);
        }
        validateIndirectTargets();

        VerificationReport report;
        report.diagnostics = collector_.take();
        std::stable_sort(
            report.diagnostics.begin(), report.diagnostics.end(),
            [](const Diagnostic& left, const Diagnostic& right) {
                if (left.severity != right.severity) {
                    return left.severity == Severity::Error;
                }
                if (left.function != right.function) {
                    return left.function < right.function;
                }
                if (left.block != right.block) {
                    return left.block < right.block;
                }
                if (left.instruction_index != right.instruction_index) {
                    return left.instruction_index < right.instruction_index;
                }
                return std::string(toString(left.code)) <
                       std::string(toString(right.code));
            });
        return report;
    }

private:
    void addDiagnostic(DiagnosticCode code, Severity severity,
                       const std::string& function, const std::string& block,
                       std::size_t instructionIndex, const std::string& message,
                       const std::vector<PathStep>& path) {
        Diagnostic diagnostic;
        diagnostic.code = code;
        diagnostic.severity = severity;
        diagnostic.function = function;
        diagnostic.block = block;
        diagnostic.instruction_index = instructionIndex;
        diagnostic.message = message;
        diagnostic.path = path;
        collector_.add(std::move(diagnostic));
    }

    void analyzeFunction(const Function& function) {
        if (function.entry.empty()) {
            addDiagnostic(DiagnosticCode::InvalidReference, Severity::Error,
                          function.name, "", 0,
                          "function has no entry basic block", {});
            return;
        }
        if (index_.block(function.name, function.entry) == nullptr) {
            addDiagnostic(DiagnosticCode::InvalidReference, Severity::Error,
                          function.name, function.entry, 0,
                          "function entry basic block does not exist", {});
            return;
        }

        std::deque<WorkItem> work;
        WorkItem initial;
        initial.block = function.entry;
        work.push_back(std::move(initial));

        std::set<std::string> visited;
        visited.insert(initialKey(function.entry));
        std::size_t visitedCount = 1;
        bool stoppedForLimit = false;

        while (!work.empty() && !stoppedForLimit) {
            WorkItem item = std::move(work.front());
            work.pop_front();

            const BasicBlock* block =
                index_.block(function.name, item.block);
            if (block == nullptr ||
                item.instruction_index >= block->instructions.size()) {
                continue;
            }

            const Instruction& instruction =
                block->instructions[item.instruction_index];
            std::vector<PathStep> currentPath = item.path;
            currentPath.push_back(pathStep(function, *block,
                                           item.instruction_index,
                                           instruction));
            State state = item.state;

            if (isIndirectBranch(instruction.opcode)) {
                const std::string key =
                    siteKey(function.name, block->name,
                            item.instruction_index);
                const auto found = branches_.find(key);
                if (found == branches_.end() ||
                    currentPath.size() < found->second.path.size()) {
                    BranchObservation observation;
                    observation.function = &function;
                    observation.block = block;
                    observation.instruction_index = item.instruction_index;
                    observation.instruction = &instruction;
                    observation.path = currentPath;
                    branches_[key] = std::move(observation);
                }
            }

            if (isConditional(instruction.opcode) &&
                conditionIsAttackerControlled(instruction) &&
                !state.pending_authentications.empty()) {
                state.attacker_branch_in_window = true;
            }

            processInstruction(function, *block, item.instruction_index,
                               instruction, currentPath, state);

            if (currentPath.size() >= options_.max_path_instructions) {
                addDiagnostic(
                    DiagnosticCode::AnalysisLimit, Severity::Error,
                    function.name, block->name, item.instruction_index,
                    "path length limit reached; deeper paths were not explored",
                    currentPath);
                continue;
            }

            std::vector<std::pair<std::string, std::size_t>> successors =
                successorsFor(function, *block, item.instruction_index,
                              instruction);
            for (const auto& successor : successors) {
                if (successor.first.empty()) {
                    continue;
                }
                if (index_.block(function.name, successor.first) == nullptr) {
                    addDiagnostic(
                        DiagnosticCode::InvalidControlFlowEdge, Severity::Error,
                        function.name, block->name, item.instruction_index,
                        "control-flow successor '" + successor.first +
                            "' does not exist",
                        currentPath);
                    continue;
                }
                State successorState = state;
                const std::string key =
                    successor.first + "\x1f" +
                    std::to_string(successor.second) + "\x1f" +
                    stateKey(successorState);
                if (!visited.insert(key).second) {
                    continue;
                }
                ++visitedCount;
                if (visitedCount > options_.max_states) {
                    addDiagnostic(
                        DiagnosticCode::AnalysisLimit, Severity::Error,
                        function.name, block->name, item.instruction_index,
                        "state limit reached; verification is incomplete",
                        currentPath);
                    stoppedForLimit = true;
                    break;
                }
                WorkItem next;
                next.block = successor.first;
                next.instruction_index = successor.second;
                next.state = std::move(successorState);
                next.path = currentPath;
                work.push_back(std::move(next));
            }
        }
    }

    std::string initialKey(const std::string& entry) const {
        return entry + "\x1f" "0" "\x1f" + stateKey(State{});
    }

    bool conditionIsAttackerControlled(const Instruction& instruction) const {
        if (instruction.condition_trust == ConditionTrust::AttackerControlled) {
            return true;
        }
        if (instruction.condition_trust == ConditionTrust::Trusted) {
            return false;
        }
        return options_.treat_unknown_conditions_as_attacker_controlled;
    }

    void processInstruction(const Function& function, const BasicBlock& block,
                            std::size_t index, const Instruction& instruction,
                            const std::vector<PathStep>& path, State& state) {
        switch (instruction.opcode) {
            case Opcode::Paciasp:
                applySign(function, block, index, path, state,
                          normalizeRegister("x30"), PacKey::IA, "sp");
                break;
            case Opcode::Pacibsp:
                applySign(function, block, index, path, state,
                          normalizeRegister("x30"), PacKey::IB, "sp");
                break;
            case Opcode::Pacia:
            case Opcode::Pacib:
            case Opcode::Pacda:
            case Opcode::Pacdb:
                applySign(function, block, index, path, state,
                          instruction.rd, keyForSign(instruction.opcode),
                          instruction.rn);
                break;
            case Opcode::Autiasp:
                applyAuthentication(function, block, index, path,
                                    state, normalizeRegister("x30"), PacKey::IA,
                                    "sp");
                break;
            case Opcode::Autibsp:
                applyAuthentication(function, block, index, path,
                                    state, normalizeRegister("x30"), PacKey::IB,
                                    "sp");
                break;
            case Opcode::Autia:
            case Opcode::Autib:
            case Opcode::Autda:
            case Opcode::Autdb:
                applyAuthentication(function, block, index, path,
                                    state, instruction.rd,
                                    keyForSign(instruction.opcode),
                                    instruction.rn);
                break;
            case Opcode::Xpaci:
            case Opcode::Xpacd: {
                RegisterValue stripped;
                stripped.kind = RegKind::Stripped;
                assignRegister(state, instruction.rd, stripped);
                break;
            }
            case Opcode::Ret:
                applyPlainReturn(function, block, index, instruction, path,
                                 state);
                break;
            case Opcode::Retaa:
                applyAuthenticatingReturn(function, block, index, instruction,
                                          path, state, PacKey::IA);
                break;
            case Opcode::Retab:
                applyAuthenticatingReturn(function, block, index, instruction,
                                          path, state, PacKey::IB);
                break;
            case Opcode::Blr:
            case Opcode::Br:
                applyIndirectBranch(function, block, index, instruction, path,
                                    state);
                if (instruction.opcode == Opcode::Blr) {
                    clearCallerLinkRegister(state);
                }
                break;
            case Opcode::Bl:
                clearCallerLinkRegister(state);
                break;
            case Opcode::Str:
            case Opcode::Ldr:
            case Opcode::Stp:
            case Opcode::Ldp:
                applyMemory(function, block, index, instruction, path, state);
                break;
            case Opcode::Add:
            case Opcode::Sub:
                applyStackArithmetic(instruction, state);
                break;
            case Opcode::Mov:
                applyMove(instruction, state);
                break;
            default:
                break;
        }
    }

    void applySign(const Function& function, const BasicBlock& block,
                   std::size_t index, const std::vector<PathStep>& path,
                   State& state,
                   const std::string& destination, PacKey key,
                   const std::string& modifier) {
        const std::string destinationRegister = normalizeRegister(destination);
        if (destinationRegister.empty() || destinationRegister == "xzr") {
            return;
        }

        const std::string normalizedModifier = normalizeRegister(modifier);
        const bool spModifier = normalizedModifier == "sp";
        SignRecord record;
        record.key = key;
        record.modifier_register = normalizedModifier;
        record.sp_modifier = spModifier;
        record.sp_known = !spModifier || state.sp_known;
        record.sp_offset = state.sp_offset;

        RegisterValue value;
        value.kind = RegKind::Signed;
        value.key = key;
        value.modifier_register = normalizedModifier;
        value.signer_sp_known = record.sp_known;
        value.signer_sp_offset = record.sp_offset;

        state.registers[destinationRegister] = value;
        state.signatures[destinationRegister] = record;
        erasePendingAuthentication(state, destinationRegister);

        if (destinationRegister == "x30" && isDataKey(key)) {
            addDiagnostic(
                DiagnosticCode::InvalidReturnKeyDomain, Severity::Error,
                function.name, block.name, index,
                std::string("return address x30 was signed with data key ") +
                    pacKeyText(key),
                path);
        }
    }

    void applyAuthentication(const Function& function, const BasicBlock& block,
                             std::size_t index,
                             const std::vector<PathStep>& path, State& state,
                             const std::string& destination, PacKey expectedKey,
                             const std::string& modifier) {
        const std::string registerName = normalizeRegister(destination);
        if (registerName.empty() || registerName == "xzr") {
            return;
        }

        const std::string normalizedModifier = normalizeRegister(modifier);
        const bool spModifier = normalizedModifier == "sp";
        const auto signature = state.signatures.find(registerName);
        if (signature != state.signatures.end()) {
            if (signature->second.key != expectedKey) {
                addDiagnostic(
                    DiagnosticCode::PacModifierMismatch, Severity::Error,
                    function.name, block.name, index,
                    "authentication expects " +
                        std::string(pacKeyText(expectedKey)) +
                        " but the value was signed with " +
                        pacKeyText(signature->second.key),
                    path);
            }
            if (!signature->second.modifier_register.empty() &&
                normalizedModifier != signature->second.modifier_register) {
                addDiagnostic(
                    DiagnosticCode::PacModifierMismatch, Severity::Error,
                    function.name, block.name, index,
                    "authentication modifier " + normalizedModifier +
                        " does not match signing modifier " +
                        signature->second.modifier_register,
                    path);
            }
            if (signature->second.sp_modifier && spModifier) {
                if (!signature->second.sp_known || !state.sp_known) {
                    addDiagnostic(
                        DiagnosticCode::SpOffsetUnknown, Severity::Error,
                        function.name, block.name, index,
                        "SP offset is unknown at PAC authentication", path);
                } else if (signature->second.sp_offset != state.sp_offset) {
                    addDiagnostic(
                        DiagnosticCode::SpNotRestoredAtAuthentication,
                        Severity::Error, function.name, block.name, index,
                        "SP at authentication is " +
                            std::to_string(state.sp_offset) +
                            " bytes from entry, but PACIASP signed at " +
                            std::to_string(signature->second.sp_offset),
                        path);
                }
            }
        } else {
            const RegisterValue& value =
                registerValue(state, registerName);
            if (value.kind == RegKind::Signed) {
                if (value.key != expectedKey) {
                    addDiagnostic(
                        DiagnosticCode::PacModifierMismatch, Severity::Error,
                        function.name, block.name, index,
                        "authentication expects " +
                            std::string(pacKeyText(expectedKey)) +
                            " but the value was signed with " +
                            pacKeyText(value.key),
                        path);
                }
                if (!value.modifier_register.empty() &&
                    normalizedModifier != value.modifier_register) {
                    addDiagnostic(
                        DiagnosticCode::PacModifierMismatch, Severity::Error,
                        function.name, block.name, index,
                        "authentication modifier " + normalizedModifier +
                            " does not match signing modifier " +
                            value.modifier_register,
                        path);
                }
                if (spModifier && value.signer_sp_known &&
                    state.sp_known &&
                    value.signer_sp_offset != state.sp_offset) {
                    addDiagnostic(
                        DiagnosticCode::SpNotRestoredAtAuthentication,
                        Severity::Error, function.name, block.name, index,
                        "SP at authentication is " +
                            std::to_string(state.sp_offset) +
                            " bytes from entry, but the return address was "
                            "signed at " +
                            std::to_string(value.signer_sp_offset),
                        path);
                }
            }
            if (registerName == "x30" &&
                value.kind != RegKind::Signed) {
                addDiagnostic(
                    DiagnosticCode::MissingPacContext, Severity::Error,
                    function.name, block.name, index,
                    "AUTIASP/AUTIBSP reached without a tracked signed return "
                    "address",
                    path);
            }
        }

        RegisterValue authenticated;
        authenticated.kind = RegKind::Authenticated;
        authenticated.key = expectedKey;
        state.registers[registerName] = authenticated;
        state.signatures.erase(registerName);
        erasePendingAuthentication(state, registerName);
        state.pending_authentications[registerName] = path.size() - 1;
    }

    std::string returnRegister(const Instruction& instruction) const {
        if (instruction.opcode == Opcode::Retaa ||
            instruction.opcode == Opcode::Retab) {
            return "x30";
        }
        if (instruction.rn.empty()) {
            return "x30";
        }
        return normalizeRegister(instruction.rn);
    }

    void detectOracleUse(const Function& function, const BasicBlock& block,
                         std::size_t index,
                         const std::string& registerName,
                         const std::vector<PathStep>& path, State& state) {
        const std::string normalized = normalizeRegister(registerName);
        const auto pending =
            state.pending_authentications.find(normalized);
        if (pending == state.pending_authentications.end()) {
            return;
        }

        if (state.attacker_branch_in_window) {
            const std::size_t start =
                std::min(pending->second, path.size() - 1);
            std::vector<PathStep> window(path.begin() +
                                             static_cast<std::ptrdiff_t>(start),
                                         path.end());
            addDiagnostic(
                DiagnosticCode::PacmanOracleWindow, Severity::Error,
                function.name, block.name, index,
                "authenticated register " + normalized +
                    " is used after an attacker-controlled conditional "
                    "branch; the AUT-to-use window is a PACMAN oracle",
                window);
        }
        erasePendingAuthentication(state, normalized);
    }

    void applyPlainReturn(const Function& function, const BasicBlock& block,
                          std::size_t index,
                          const Instruction& instruction,
                          const std::vector<PathStep>& path, State& state) {
        const std::string registerName = returnRegister(instruction);
        detectOracleUse(function, block, index, registerName, path, state);

        if (!state.sp_known) {
            addDiagnostic(DiagnosticCode::SpOffsetUnknown, Severity::Error,
                          function.name, block.name, index,
                          "SP offset is unknown at return", path);
        } else if (state.sp_offset != 0) {
            addDiagnostic(
                DiagnosticCode::SpNotRestoredAtReturn, Severity::Error,
                function.name, block.name, index,
                "return leaves SP at " + std::to_string(state.sp_offset) +
                    " bytes instead of restoring it to the entry value",
                path);
        }

        const RegisterValue& value = registerValue(state, registerName);
        const bool signedValue =
            value.kind == RegKind::Signed ||
            state.signatures.find(registerName) != state.signatures.end();
        if (signedValue) {
            addDiagnostic(
                DiagnosticCode::SignedReturnNotAuthenticated, Severity::Error,
                function.name, block.name, index,
                "return address " + registerName +
                    " still carries a PAC signature at RET",
                path);
        } else if (value.kind == RegKind::Stripped) {
            addDiagnostic(
                DiagnosticCode::XpaciDowngrade, Severity::Error,
                function.name, block.name, index,
                "return address " + registerName +
                    " was stripped by XPACI/XPACD and is used by RET without "
                    "revalidation",
                path);
        } else if (value.kind == RegKind::Authenticated &&
                   isDataKey(value.key)) {
            addDiagnostic(
                DiagnosticCode::InvalidReturnKeyDomain, Severity::Error,
                function.name, block.name, index,
                "RET uses a return address authenticated with data key " +
                    std::string(pacKeyText(value.key)),
                path);
        }
    }

    void applyAuthenticatingReturn(
        const Function& function, const BasicBlock& block, std::size_t index,
        const Instruction& instruction, const std::vector<PathStep>& path,
        State& state, PacKey expectedKey) {
        detectOracleUse(function, block, index, "x30", path, state);

        if (!state.sp_known) {
            addDiagnostic(DiagnosticCode::SpOffsetUnknown, Severity::Error,
                          function.name, block.name, index,
                          "SP offset is unknown at authenticating return", path);
        } else if (state.sp_offset != 0) {
            addDiagnostic(
                DiagnosticCode::SpNotRestoredAtReturn, Severity::Error,
                function.name, block.name, index,
                "authenticating return leaves SP at " +
                    std::to_string(state.sp_offset) +
                    " bytes instead of restoring it to the entry value",
                path);
        }

        const auto signature = state.signatures.find("x30");
        if (signature != state.signatures.end()) {
            if (signature->second.key != expectedKey) {
                addDiagnostic(
                    DiagnosticCode::PacModifierMismatch, Severity::Error,
                    function.name, block.name, index,
                    std::string(toString(instruction.opcode)) +
                        " expects " + pacKeyText(expectedKey) +
                        " but the return address was signed with " +
                        pacKeyText(signature->second.key),
                    path);
            }
            if (!signature->second.modifier_register.empty() &&
                signature->second.modifier_register != "sp") {
                addDiagnostic(
                    DiagnosticCode::PacModifierMismatch, Severity::Error,
                    function.name, block.name, index,
                    std::string(toString(instruction.opcode)) +
                        " uses SP as the PAC modifier, but the return address "
                        "was signed with modifier " +
                        signature->second.modifier_register,
                    path);
            }
            if (signature->second.sp_modifier) {
                if (!signature->second.sp_known || !state.sp_known) {
                    addDiagnostic(
                        DiagnosticCode::SpOffsetUnknown, Severity::Error,
                        function.name, block.name, index,
                        "SP offset is unknown while authenticating the return "
                        "address",
                        path);
                } else if (signature->second.sp_offset != state.sp_offset) {
                    addDiagnostic(
                        DiagnosticCode::SpNotRestoredAtAuthentication,
                        Severity::Error, function.name, block.name, index,
                        "authenticating return uses SP " +
                            std::to_string(state.sp_offset) +
                            " but the return address was signed at SP " +
                            std::to_string(signature->second.sp_offset),
                        path);
                }
            }
        } else {
            const RegisterValue& value = registerValue(state, "x30");
            if (value.kind == RegKind::Signed && value.key != expectedKey) {
                addDiagnostic(
                    DiagnosticCode::PacModifierMismatch, Severity::Error,
                    function.name, block.name, index,
                    std::string(toString(instruction.opcode)) +
                        " expects " + pacKeyText(expectedKey) +
                        " but the return address was signed with " +
                        pacKeyText(value.key),
                    path);
            }
            if (value.kind == RegKind::Signed &&
                !value.modifier_register.empty() &&
                value.modifier_register != "sp") {
                addDiagnostic(
                    DiagnosticCode::PacModifierMismatch, Severity::Error,
                    function.name, block.name, index,
                    std::string(toString(instruction.opcode)) +
                        " uses SP as the PAC modifier, but the return address "
                        "was signed with modifier " +
                        value.modifier_register,
                    path);
            }
        }

        state.signatures.erase("x30");
        erasePendingAuthentication(state, "x30");
    }

    void applyIndirectBranch(
        const Function& function, const BasicBlock& block, std::size_t index,
        const Instruction& instruction, const std::vector<PathStep>& path,
        State& state) {
        const std::string registerName =
            instruction.rn.empty() ? "xzr" : normalizeRegister(instruction.rn);
        detectOracleUse(function, block, index, registerName, path, state);

        const RegisterValue& value = registerValue(state, registerName);
        if (value.kind == RegKind::Stripped) {
            addDiagnostic(
                DiagnosticCode::XpaciDowngrade, Severity::Error,
                function.name, block.name, index,
                "register " + registerName +
                    " was stripped by XPACI/XPACD and is branched through "
                    "without revalidation",
                path);
        } else if (value.kind == RegKind::Signed) {
            addDiagnostic(
                DiagnosticCode::UnvalidatedSignedPointerBranch, Severity::Error,
                function.name, block.name, index,
                "indirect branch uses signed register " + registerName +
                    " without a preceding authentication",
                path);
        }

        if (instruction.opcode == Opcode::Br) {
            bool sawTailCall = false;
            for (const IndirectBranchTarget& target :
                 program_.indirect_targets) {
                if (target.site.function != function.name ||
                    target.site.block != block.name ||
                    target.site.instruction_index != index) {
                    continue;
                }
                const BranchPurpose purpose =
                    effectivePurpose(target, function.name, Opcode::Br);
                if (purpose == BranchPurpose::TailCall) {
                    sawTailCall = true;
                }
            }
            if (sawTailCall) {
                if (!state.sp_known) {
                    addDiagnostic(
                        DiagnosticCode::SpOffsetUnknown, Severity::Error,
                        function.name, block.name, index,
                        "SP offset is unknown at indirect tail call", path);
                } else if (state.sp_offset != 0) {
                    addDiagnostic(
                        DiagnosticCode::SpNotRestoredAtTailCall,
                        Severity::Error, function.name, block.name, index,
                        "indirect tail call leaves SP at " +
                            std::to_string(state.sp_offset) +
                            " bytes instead of restoring the caller frame",
                        path);
                }
            }
        }
    }

    void applyMemory(const Function& function, const BasicBlock& block,
                     std::size_t index, const Instruction& instruction,
                     const std::vector<PathStep>& path, State& state) {
        detectOracleUse(function, block, index, instruction.rn, path, state);

        const bool stackBase =
            normalizeRegister(instruction.rn) == "sp" ||
            (normalizeRegister(instruction.rn) == "x29" &&
             state.frame_pointer_known);
        bool addressKnown = false;
        std::int64_t address = 0;
        std::int64_t secondAddress = 0;
        bool secondAddressKnown = false;
        if (stackBase && instruction.immediate_known) {
            if (normalizeRegister(instruction.rn) == "sp" && state.sp_known) {
                addressKnown = checkedAdd(state.sp_offset,
                                           instruction.immediate, address);
            } else if (normalizeRegister(instruction.rn) == "x29" &&
                       state.frame_pointer_known) {
                addressKnown = checkedAdd(
                    state.frame_pointer_sp_offset, instruction.immediate,
                    address);
            }
            secondAddressKnown =
                addressKnown && checkedAdd(address, 8, secondAddress);
        }

        const std::string firstRegister =
            normalizeRegister(instruction.rd);
        const std::string secondRegister =
            normalizeRegister(instruction.rt2);

        if (isStore(instruction.opcode)) {
            const bool spillsReturnAddress =
                firstRegister == "x30" || secondRegister == "x30";
            if (spillsReturnAddress && stackBase) {
                const std::string spilledRegister =
                    firstRegister == "x30" ? firstRegister : secondRegister;
                const RegisterValue& value =
                    registerValue(state, spilledRegister);
                if (value.kind != RegKind::Signed ||
                    !isInstructionKey(value.key)) {
                    addDiagnostic(
                        DiagnosticCode::ReturnAddressSpilledUnsigned,
                        Severity::Error, function.name, block.name, index,
                        "x30 is spilled to stack memory without a valid IA/IB "
                        "PAC signature",
                        path);
                }
            }

            if (addressKnown) {
                state.stack_slots[address] =
                    registerValue(state, firstRegister);
                if (isPair(instruction.opcode) && !secondRegister.empty() &&
                    secondAddressKnown) {
                    state.stack_slots[secondAddress] =
                        registerValue(state, secondRegister);
                }
            }
        } else {
            const RegisterValue firstValue =
                addressKnown
                    ? (state.stack_slots.find(address) ==
                               state.stack_slots.end()
                           ? RegisterValue{}
                           : state.stack_slots[address])
                    : RegisterValue{};
            assignRegister(state, firstRegister, firstValue);
            if (firstValue.kind == RegKind::Signed) {
                SignRecord record;
                record.key = firstValue.key;
                record.modifier_register = firstValue.modifier_register;
                record.sp_modifier =
                    firstValue.modifier_register == "sp";
                record.sp_known = firstValue.signer_sp_known;
                record.sp_offset = firstValue.signer_sp_offset;
                state.signatures[firstRegister] = record;
            }
            if (isPair(instruction.opcode) && !secondRegister.empty()) {
                const RegisterValue secondValue =
                    secondAddressKnown &&
                            state.stack_slots.find(secondAddress) !=
                                state.stack_slots.end()
                        ? state.stack_slots[secondAddress]
                        : RegisterValue{};
                assignRegister(state, secondRegister, secondValue);
                if (secondValue.kind == RegKind::Signed) {
                    SignRecord record;
                    record.key = secondValue.key;
                    record.modifier_register =
                        secondValue.modifier_register;
                    record.sp_modifier =
                        secondValue.modifier_register == "sp";
                    record.sp_known = secondValue.signer_sp_known;
                    record.sp_offset = secondValue.signer_sp_offset;
                    state.signatures[secondRegister] = record;
                }
            }
            if (firstRegister == "x29") {
                state.frame_pointer_known = false;
                state.frame_pointer_sp_offset = 0;
            }
        }

        if (instruction.memory_mode != MemoryMode::Offset &&
            normalizeRegister(instruction.rn) == "sp") {
            std::int64_t writeback = instruction.writeback_immediate;
            if (instruction.memory_mode == MemoryMode::PreIndex &&
                writeback == 0 && instruction.immediate != 0) {
                writeback = instruction.immediate;
            }
            const bool writebackKnown =
                instruction.writeback_known &&
                (instruction.memory_mode != MemoryMode::PreIndex ||
                 instruction.immediate_known);
            std::int64_t updatedSp = 0;
            if (state.sp_known && writebackKnown &&
                checkedAdd(state.sp_offset, writeback, updatedSp)) {
                state.sp_offset = updatedSp;
            } else {
                state.sp_known = false;
                state.sp_offset = 0;
            }
        }
    }

    void applyStackArithmetic(const Instruction& instruction, State& state) {
        const std::string destination = normalizeRegister(instruction.rd);
        const std::string source = normalizeRegister(instruction.rn);
        if (!instruction.immediate_known) {
            if (destination == "sp") {
                state.sp_known = false;
                state.sp_offset = 0;
            } else if (destination == "x29") {
                state.frame_pointer_known = false;
                state.frame_pointer_sp_offset = 0;
            }
            RegisterValue unknown;
            assignRegister(state, destination, unknown);
            return;
        }
        std::int64_t delta = instruction.immediate;
        if (instruction.opcode == Opcode::Sub &&
            !checkedNegate(instruction.immediate, delta)) {
            if (destination == "sp") {
                state.sp_known = false;
                state.sp_offset = 0;
            } else if (destination == "x29") {
                state.frame_pointer_known = false;
                state.frame_pointer_sp_offset = 0;
            }
            RegisterValue unknown;
            assignRegister(state, destination, unknown);
            return;
        }

        if (destination == "sp") {
            if (source == "sp" && state.sp_known) {
                std::int64_t updatedSp = 0;
                if (checkedAdd(state.sp_offset, delta, updatedSp)) {
                    state.sp_offset = updatedSp;
                } else {
                    state.sp_known = false;
                    state.sp_offset = 0;
                }
            } else if (source == "x29" && state.frame_pointer_known) {
                std::int64_t updatedSp = 0;
                if (checkedAdd(state.frame_pointer_sp_offset, delta,
                               updatedSp)) {
                    state.sp_offset = updatedSp;
                    state.sp_known = true;
                } else {
                    state.sp_known = false;
                    state.sp_offset = 0;
                }
            } else {
                state.sp_known = false;
                state.sp_offset = 0;
            }
            return;
        }

        if (destination == "x29" && source == "sp" && state.sp_known) {
            std::int64_t updatedFrame = 0;
            if (checkedAdd(state.sp_offset, delta, updatedFrame)) {
                state.frame_pointer_known = true;
                state.frame_pointer_sp_offset = updatedFrame;
            } else {
                state.frame_pointer_known = false;
                state.frame_pointer_sp_offset = 0;
            }
            return;
        }

        RegisterValue unknown;
        assignRegister(state, destination, unknown);
        if (destination == "x29") {
            state.frame_pointer_known = false;
        }
    }

    void applyMove(const Instruction& instruction, State& state) {
        const std::string destination = normalizeRegister(instruction.rd);
        const std::string source = normalizeRegister(instruction.rn);

        if (destination == "sp") {
            if (source == "x29" && state.frame_pointer_known) {
                state.sp_offset = state.frame_pointer_sp_offset;
                state.sp_known = true;
            } else if (source != "sp") {
                state.sp_known = false;
                state.sp_offset = 0;
            }
            return;
        }

        if (destination == "x29" && source == "sp") {
            state.frame_pointer_known = state.sp_known;
            state.frame_pointer_sp_offset = state.sp_offset;
            return;
        }

        if (source == "sp" || source == "xzr") {
            RegisterValue unknown;
            assignRegister(state, destination, unknown);
        } else {
            SignRecord copiedSignature;
            bool hasSignature = false;
            const auto signature = state.signatures.find(source);
            if (signature != state.signatures.end()) {
                copiedSignature = signature->second;
                hasSignature = true;
            }
            RegisterValue copied = registerValue(state, source);
            assignRegister(state, destination, copied);
            if (hasSignature) {
                state.signatures[destination] = copiedSignature;
            }
        }
        if (destination == "x29") {
            state.frame_pointer_known = false;
        }
    }

    BranchPurpose effectivePurpose(const IndirectBranchTarget& target,
                                   const std::string& functionName,
                                   Opcode opcode) const {
        if (opcode == Opcode::Blr) {
            return BranchPurpose::NormalCall;
        }
        if (target.purpose == BranchPurpose::Jump) {
            return BranchPurpose::Jump;
        }
        if (target.purpose == BranchPurpose::TailCall ||
            target.purpose == BranchPurpose::NormalCall) {
            return BranchPurpose::TailCall;
        }
        if (!target.external &&
            target.target_function == functionName) {
            return BranchPurpose::Jump;
        }
        return BranchPurpose::TailCall;
    }

    std::vector<std::pair<std::string, std::size_t>> successorsFor(
        const Function& function, const BasicBlock& block, std::size_t index,
        const Instruction& instruction) const {
        std::vector<std::pair<std::string, std::size_t>> result;
        const auto add = [&result](const std::string& name) {
            if (!name.empty()) {
                result.emplace_back(name, 0);
            }
        };
        const auto fallthrough = [&result, &block,
                                  index](std::size_t blockSize) {
            if (index + 1 < blockSize) {
                result.emplace_back(block.name, index + 1);
            } else if (!block.fallthrough.empty()) {
                result.emplace_back(block.fallthrough, 0);
            }
        };

        switch (instruction.opcode) {
            case Opcode::B:
                add(instruction.target);
                break;
            case Opcode::BCond:
            case Opcode::Cbz:
            case Opcode::Cbnz:
            case Opcode::Tbz:
            case Opcode::Tbnz:
                add(instruction.target);
                fallthrough(block.instructions.size());
                break;
            case Opcode::Bl:
            case Opcode::Blr:
                fallthrough(block.instructions.size());
                break;
            case Opcode::Br:
                for (const IndirectBranchTarget& target :
                     program_.indirect_targets) {
                    if (target.site.function != function.name ||
                        target.site.block != block.name ||
                        target.site.instruction_index != index ||
                        target.external) {
                        continue;
                    }
                    const BranchPurpose purpose =
                        effectivePurpose(target, function.name, Opcode::Br);
                    if (purpose == BranchPurpose::Jump &&
                        target.target_function == function.name) {
                        add(target.target_block);
                    }
                }
                break;
            case Opcode::Ret:
            case Opcode::Retaa:
            case Opcode::Retab:
            case Opcode::Eret:
            case Opcode::Brk:
                break;
            default:
                fallthrough(block.instructions.size());
                break;
        }

        std::vector<std::pair<std::string, std::size_t>> unique;
        for (const auto& successor : result) {
            if (std::find(unique.begin(), unique.end(), successor) ==
                unique.end()) {
                unique.push_back(successor);
            }
        }
        return unique;
    }

    const Instruction* firstInstructionOf(
        const std::string& functionName,
        const std::string& blockName) const {
        const BasicBlock* block = index_.block(functionName, blockName);
        if (block == nullptr || block->instructions.empty()) {
            return nullptr;
        }
        return &block->instructions.front();
    }

    bool isCallLandingPad(const Instruction* instruction) const {
        if (instruction == nullptr) {
            return false;
        }
        return instruction->opcode == Opcode::BtiC ||
               instruction->opcode == Opcode::BtiJC ||
               instruction->opcode == Opcode::Paciasp ||
               instruction->opcode == Opcode::Pacibsp;
    }

    bool isJumpLandingPad(const Instruction* instruction) const {
        if (instruction == nullptr) {
            return false;
        }
        return instruction->opcode == Opcode::BtiJ ||
               instruction->opcode == Opcode::BtiJC;
    }

    std::string landingPadText(const Instruction* instruction) const {
        return instruction == nullptr ? "missing instruction"
                                      : instruction->toString();
    }

    void validateIndirectTargets() {
        std::map<std::string, std::size_t> targetCounts;
        for (const IndirectBranchTarget& target : program_.indirect_targets) {
            const std::string key = siteKey(target.site);
            ++targetCounts[key];

            const Instruction* branch = index_.instruction(target.site);
            if (branch == nullptr) {
                addDiagnostic(
                    DiagnosticCode::InvalidReference, Severity::Error,
                    target.site.function, target.site.block,
                    target.site.instruction_index,
                    "indirect target entry refers to a missing instruction",
                    {});
                continue;
            }
            if (!isIndirectBranch(branch->opcode)) {
                addDiagnostic(
                    DiagnosticCode::MalformedIndirectBranchSite,
                    Severity::Error, target.site.function, target.site.block,
                    target.site.instruction_index,
                    "indirect target entry does not refer to BLR or BR",
                    {});
                continue;
            }

            const auto observation = branches_.find(key);
            if (observation == branches_.end()) {
                continue;
            }
            if (target.external) {
                continue;
            }

            const std::string targetRegister =
                !branch->rn.empty()
                    ? normalizeRegister(branch->rn)
                    : normalizeRegister(target.target_register);
            if (isAddressTrustedRegister(targetRegister)) {
                continue;
            }

            const BranchPurpose purpose =
                effectivePurpose(target, target.site.function,
                                 branch->opcode);
            const Instruction* first =
                firstInstructionOf(target.target_function,
                                   target.target_block);
            std::vector<PathStep> diagnosticPath =
                observation->second.path;
            const BasicBlock* targetBlock =
                index_.block(target.target_function, target.target_block);
            if (targetBlock != nullptr && !targetBlock->instructions.empty()) {
                diagnosticPath.push_back(pathStep(
                    *index_.function(target.target_function), *targetBlock, 0,
                    targetBlock->instructions.front()));
            }

            const bool allowed =
                purpose == BranchPurpose::NormalCall
                    ? isCallLandingPad(first)
                    : isJumpLandingPad(first);
            if (!allowed) {
                const bool anyLandingPad =
                    first != nullptr &&
                    (first->opcode == Opcode::BtiC ||
                     first->opcode == Opcode::BtiJ ||
                     first->opcode == Opcode::BtiJC ||
                     first->opcode == Opcode::Paciasp ||
                     first->opcode == Opcode::Pacibsp);
                addDiagnostic(
                    anyLandingPad
                        ? DiagnosticCode::BtiPermissionMismatch
                        : DiagnosticCode::MissingBtiLandingPad,
                    Severity::Error, target.target_function,
                    target.target_block, 0,
                    std::string("target of ") +
                        (purpose == BranchPurpose::NormalCall
                             ? "BLR requires BTI c/BTI jc or an implicit "
                               "PACIASP landing pad"
                             : "BR requires BTI j/BTI jc") +
                        "; found " + landingPadText(first) + " via " +
                        targetRegister,
                    diagnosticPath);
            }
        }

        for (const auto& observation : branches_) {
            const BranchObservation& branch = observation.second;
            if (targetCounts.find(observation.first) != targetCounts.end()) {
                continue;
            }
            const std::string targetRegister =
                normalizeRegister(branch.instruction->rn);
            if (isAddressTrustedRegister(targetRegister)) {
                continue;
            }
            addDiagnostic(
                DiagnosticCode::MissingIndirectBranchTarget, Severity::Error,
                branch.function->name, branch.block->name,
                branch.instruction_index,
                std::string(toString(branch.instruction->opcode)) +
                    " target " + targetRegister +
                    " has no entry in the indirect branch target table",
                branch.path);
        }
    }

    void validateExports() {
        if (!options_.check_exports) {
            return;
        }
        for (const ExportEntry& entry : program_.exports) {
            if (entry.kind == ExportKind::Data ||
                !entry.requires_landing_pad) {
                continue;
            }
            const BasicBlock* block =
                index_.block(entry.function, entry.block);
            if (block == nullptr) {
                addDiagnostic(
                    DiagnosticCode::InvalidReference, Severity::Error,
                    entry.function, entry.block, 0,
                    "export '" + entry.name +
                        "' refers to a missing basic block",
                    {});
                continue;
            }
            const Instruction* first =
                block->instructions.empty() ? nullptr
                                            : &block->instructions.front();
            if (!isCallLandingPad(first)) {
                std::vector<PathStep> path;
                if (first != nullptr) {
                    path.push_back(pathStep(
                        *index_.function(entry.function), *block, 0, *first));
                }
                addDiagnostic(
                    DiagnosticCode::ExportLandingPadMissing, Severity::Error,
                    entry.function, entry.block, 0,
                    "export '" + entry.name +
                        "' must begin with BTI c, BTI jc, PACIASP, or PACIBSP; "
                        "found " +
                        landingPadText(first),
                    path);
            }
        }
    }

    void validateExceptionReturns() {
        if (!options_.check_exception_targets) {
            return;
        }
        for (const ExceptionReturnPoint& point : program_.exception_returns) {
            std::vector<PathStep> path;
            if (!point.site.function.empty() || !point.site.block.empty()) {
                const Instruction* site =
                    index_.instruction(point.site);
                if (site == nullptr) {
                    addDiagnostic(
                        DiagnosticCode::InvalidExceptionReturnSite,
                        Severity::Error, point.site.function,
                        point.site.block, point.site.instruction_index,
                        "exception return site does not exist", {});
                    continue;
                }
                if (site->opcode != Opcode::Eret) {
                    addDiagnostic(
                        DiagnosticCode::InvalidExceptionReturnSite,
                        Severity::Error, point.site.function,
                        point.site.block, point.site.instruction_index,
                        "exception return site is not ERET", {});
                }
                const BasicBlock* siteBlock =
                    index_.block(point.site.function, point.site.block);
                if (siteBlock != nullptr) {
                    path.push_back(pathStep(
                        *index_.function(point.site.function), *siteBlock,
                        point.site.instruction_index, *site));
                }
            }

            if (!point.check_target_landing_pad ||
                point.target_function.empty() ||
                point.target_block.empty()) {
                continue;
            }
            const BasicBlock* targetBlock =
                index_.block(point.target_function, point.target_block);
            if (targetBlock == nullptr) {
                addDiagnostic(
                    DiagnosticCode::InvalidReference, Severity::Error,
                    point.target_function, point.target_block, 0,
                    "exception return target block does not exist", path);
                continue;
            }
            const Instruction* first =
                targetBlock->instructions.empty()
                    ? nullptr
                    : &targetBlock->instructions.front();
            if (!isJumpLandingPad(first)) {
                if (first != nullptr) {
                    path.push_back(pathStep(
                        *index_.function(point.target_function),
                        *targetBlock, 0, *first));
                }
                addDiagnostic(
                    DiagnosticCode::ExceptionTargetLandingPadMissing,
                    Severity::Error, point.target_function,
                    point.target_block, 0,
                    "exception return target must begin with BTI j or BTI jc; "
                    "found " +
                        landingPadText(first),
                    path);
            }
        }
    }

    const Program& program_;
    Options options_;
    ProgramIndex index_;
    Collector collector_;
    std::map<std::string, BranchObservation> branches_;
};

}  // namespace

const char* toString(PacKey key) noexcept {
    return pacKeyText(key);
}

const char* toString(Opcode opcode) noexcept {
    switch (opcode) {
        case Opcode::Other:
            return "other";
        case Opcode::Nop:
            return "nop";
        case Opcode::BtiC:
            return "bti.c";
        case Opcode::BtiJ:
            return "bti.j";
        case Opcode::BtiJC:
            return "bti.jc";
        case Opcode::Paciasp:
            return "paciasp";
        case Opcode::Pacibsp:
            return "pacibsp";
        case Opcode::Autiasp:
            return "autiasp";
        case Opcode::Autibsp:
            return "autibsp";
        case Opcode::Pacia:
            return "pacia";
        case Opcode::Pacib:
            return "pacib";
        case Opcode::Pacda:
            return "pacda";
        case Opcode::Pacdb:
            return "pacdb";
        case Opcode::Autia:
            return "autia";
        case Opcode::Autib:
            return "autib";
        case Opcode::Autda:
            return "autda";
        case Opcode::Autdb:
            return "autdb";
        case Opcode::Xpaci:
            return "xpaci";
        case Opcode::Xpacd:
            return "xpacd";
        case Opcode::Ret:
            return "ret";
        case Opcode::Retaa:
            return "retaa";
        case Opcode::Retab:
            return "retab";
        case Opcode::Blr:
            return "blr";
        case Opcode::Br:
            return "br";
        case Opcode::Bl:
            return "bl";
        case Opcode::B:
            return "b";
        case Opcode::BCond:
            return "b.cond";
        case Opcode::Cbz:
            return "cbz";
        case Opcode::Cbnz:
            return "cbnz";
        case Opcode::Tbz:
            return "tbz";
        case Opcode::Tbnz:
            return "tbnz";
        case Opcode::Eret:
            return "eret";
        case Opcode::Brk:
            return "brk";
        case Opcode::Str:
            return "str";
        case Opcode::Ldr:
            return "ldr";
        case Opcode::Stp:
            return "stp";
        case Opcode::Ldp:
            return "ldp";
        case Opcode::Add:
            return "add";
        case Opcode::Sub:
            return "sub";
        case Opcode::Mov:
            return "mov";
    }
    return "other";
}

const char* toString(BranchPurpose purpose) noexcept {
    switch (purpose) {
        case BranchPurpose::Auto:
            return "auto";
        case BranchPurpose::NormalCall:
            return "normal-call";
        case BranchPurpose::TailCall:
            return "tail-call";
        case BranchPurpose::Jump:
            return "jump";
    }
    return "auto";
}

const char* toString(ConditionTrust trust) noexcept {
    switch (trust) {
        case ConditionTrust::Unknown:
            return "unknown";
        case ConditionTrust::AttackerControlled:
            return "attacker-controlled";
        case ConditionTrust::Trusted:
            return "trusted";
    }
    return "unknown";
}

const char* toString(Severity severity) noexcept {
    switch (severity) {
        case Severity::Error:
            return "error";
        case Severity::Warning:
            return "warning";
    }
    return "error";
}

const char* toString(DiagnosticCode code) noexcept {
    switch (code) {
        case DiagnosticCode::InvalidReference:
            return "invalid_reference";
        case DiagnosticCode::InvalidControlFlowEdge:
            return "invalid_control_flow_edge";
        case DiagnosticCode::MalformedIndirectBranchSite:
            return "malformed_indirect_branch_site";
        case DiagnosticCode::MissingIndirectBranchTarget:
            return "missing_indirect_branch_target";
        case DiagnosticCode::SpNotRestoredAtAuthentication:
            return "sp_not_restored_at_authentication";
        case DiagnosticCode::SpNotRestoredAtReturn:
            return "sp_not_restored_at_return";
        case DiagnosticCode::SpNotRestoredAtTailCall:
            return "sp_not_restored_at_tail_call";
        case DiagnosticCode::SpOffsetUnknown:
            return "sp_offset_unknown";
        case DiagnosticCode::PacModifierMismatch:
            return "pac_modifier_mismatch";
        case DiagnosticCode::MissingPacContext:
            return "missing_pac_context";
        case DiagnosticCode::InvalidReturnKeyDomain:
            return "invalid_return_key_domain";
        case DiagnosticCode::ReturnAddressSpilledUnsigned:
            return "return_address_spilled_unsigned";
        case DiagnosticCode::SignedReturnNotAuthenticated:
            return "signed_return_not_authenticated";
        case DiagnosticCode::UnvalidatedSignedPointerBranch:
            return "unvalidated_signed_pointer_branch";
        case DiagnosticCode::XpaciDowngrade:
            return "xpaci_downgrade";
        case DiagnosticCode::MissingBtiLandingPad:
            return "missing_bti_landing_pad";
        case DiagnosticCode::BtiPermissionMismatch:
            return "bti_permission_mismatch";
        case DiagnosticCode::ExportLandingPadMissing:
            return "export_landing_pad_missing";
        case DiagnosticCode::InvalidExceptionReturnSite:
            return "invalid_exception_return_site";
        case DiagnosticCode::ExceptionTargetLandingPadMissing:
            return "exception_target_landing_pad_missing";
        case DiagnosticCode::PacmanOracleWindow:
            return "pacman_oracle_window";
        case DiagnosticCode::AnalysisLimit:
            return "analysis_limit";
    }
    return "invalid_reference";
}

Instruction Instruction::parse(const std::string& text) {
    std::optional<Instruction> parsed = tryParse(text);
    if (!parsed.has_value()) {
        throw std::invalid_argument("unsupported or empty instruction: " + text);
    }
    return std::move(*parsed);
}

std::optional<Instruction> Instruction::tryParse(const std::string& text) {
    const std::string cleaned = stripComment(text);
    if (cleaned.empty()) {
        return std::nullopt;
    }

    const std::size_t whitespace = cleaned.find_first_of(" \t");
    const std::string mnemonic =
        lower(trim(cleaned.substr(0, whitespace)));
    const std::string rest =
        whitespace == std::string::npos
            ? std::string()
            : trim(cleaned.substr(whitespace + 1));
    const std::vector<std::string> operands =
        operandsAfterMnemonic(rest);

    Instruction instruction;
    instruction.raw = cleaned;

    if (startsWith(mnemonic, "b.") && mnemonic.size() > 2) {
        instruction.opcode = Opcode::BCond;
        instruction.target = rest.empty() ? std::string() : trim(rest);
        return instruction;
    }
    if (mnemonic == "b") {
        instruction.opcode = Opcode::B;
        instruction.target = operands.empty() ? std::string() : operands[0];
        return instruction;
    }
    if (mnemonic == "bl") {
        instruction.opcode = Opcode::Bl;
        instruction.target = operands.empty() ? std::string() : operands[0];
        return instruction;
    }
    if (mnemonic == "br" || mnemonic == "blr") {
        if (operands.empty()) {
            return std::nullopt;
        }
        instruction.opcode =
            mnemonic == "br" ? Opcode::Br : Opcode::Blr;
        instruction.rn = normalizeRegister(operands[0]);
        return instruction;
    }
    if (mnemonic == "cbz" || mnemonic == "cbnz" || mnemonic == "tbz" ||
        mnemonic == "tbnz") {
        const bool bitTest = mnemonic == "tbz" || mnemonic == "tbnz";
        if (operands.size() < (bitTest ? 3U : 2U)) {
            return std::nullopt;
        }
        instruction.opcode =
            mnemonic == "cbz"
                ? Opcode::Cbz
                : (mnemonic == "cbnz"
                       ? Opcode::Cbnz
                       : (mnemonic == "tbz" ? Opcode::Tbz : Opcode::Tbnz));
        instruction.rn = normalizeRegister(operands[0]);
        if (bitTest) {
            try {
                instruction.immediate = parseImmediate(operands[1]);
            } catch (const std::exception&) {
                instruction.immediate = 0;
                instruction.immediate_known = false;
            }
            instruction.target = operands[2];
        } else {
            instruction.target = operands[1];
        }
        return instruction;
    }
    if (mnemonic == "ret" || mnemonic == "retaa" || mnemonic == "retab") {
        instruction.opcode = mnemonic == "ret"
                                 ? Opcode::Ret
                                 : (mnemonic == "retaa" ? Opcode::Retaa
                                                        : Opcode::Retab);
        instruction.rn = operands.empty() ? "x30"
                                          : normalizeRegister(operands[0]);
        return instruction;
    }
    if (mnemonic == "eret") {
        instruction.opcode = Opcode::Eret;
        return instruction;
    }
    if (mnemonic == "brk" || mnemonic == "hlt") {
        instruction.opcode = Opcode::Brk;
        return instruction;
    }
    if (mnemonic == "nop") {
        instruction.opcode = Opcode::Nop;
        return instruction;
    }
    if (mnemonic == "bti") {
        const std::string kind =
            operands.empty() ? "c" : lower(trim(operands[0]));
        if (kind == "c") {
            instruction.opcode = Opcode::BtiC;
        } else if (kind == "j") {
            instruction.opcode = Opcode::BtiJ;
        } else if (kind == "jc") {
            instruction.opcode = Opcode::BtiJC;
        } else {
            return std::nullopt;
        }
        return instruction;
    }
    if (mnemonic == "paciasp") {
        instruction.opcode = Opcode::Paciasp;
        return instruction;
    }
    if (mnemonic == "pacibsp") {
        instruction.opcode = Opcode::Pacibsp;
        return instruction;
    }
    if (mnemonic == "autiasp") {
        instruction.opcode = Opcode::Autiasp;
        return instruction;
    }
    if (mnemonic == "autibsp") {
        instruction.opcode = Opcode::Autibsp;
        return instruction;
    }

    bool genericPac = false;
    const Opcode pacOpcode = genericPacOpcode(mnemonic, genericPac);
    if (genericPac) {
        if (operands.size() < 2) {
            return std::nullopt;
        }
        instruction.opcode = pacOpcode;
        instruction.rd = normalizeRegister(operands[0]);
        instruction.rn = normalizeRegister(operands[1]);
        return instruction;
    }

    if (mnemonic == "xpaci" || mnemonic == "xpacd") {
        if (operands.empty()) {
            return std::nullopt;
        }
        instruction.opcode =
            mnemonic == "xpaci" ? Opcode::Xpaci : Opcode::Xpacd;
        instruction.rd = normalizeRegister(operands[0]);
        return instruction;
    }

    if (mnemonic == "add" || mnemonic == "sub") {
        if (operands.size() < 3) {
            return std::nullopt;
        }
        instruction.opcode =
            mnemonic == "add" ? Opcode::Add : Opcode::Sub;
        instruction.rd = normalizeRegister(operands[0]);
        instruction.rn = normalizeRegister(operands[1]);
        try {
            instruction.immediate =
                applyShift(parseImmediate(operands[2]), operands);
        } catch (const std::exception&) {
            instruction.immediate = 0;
            instruction.immediate_known = false;
        }
        return instruction;
    }

    if (mnemonic == "mov") {
        if (operands.size() < 2) {
            return std::nullopt;
        }
        instruction.opcode = Opcode::Mov;
        instruction.rd = normalizeRegister(operands[0]);
        instruction.rn = normalizeRegister(operands[1]);
        if (operands.size() > 2) {
            instruction.rn.clear();
        }
        return instruction;
    }

    if (mnemonic == "str" || mnemonic == "ldr" || mnemonic == "stp" ||
        mnemonic == "ldp") {
        MemoryOperands memory;
        try {
            memory = parseMemoryOperands(rest);
        } catch (const std::exception&) {
            return std::nullopt;
        }
        instruction.opcode =
            mnemonic == "str"
                ? Opcode::Str
                : (mnemonic == "ldr"
                       ? Opcode::Ldr
                       : (mnemonic == "stp" ? Opcode::Stp : Opcode::Ldp));
        instruction.rd = normalizeRegister(memory.registers[0]);
        if ((instruction.opcode == Opcode::Stp ||
             instruction.opcode == Opcode::Ldp) &&
            memory.registers.size() >= 2) {
            instruction.rt2 = normalizeRegister(memory.registers[1]);
        }
        instruction.rn = memory.base;
        instruction.immediate = memory.offset;
        instruction.immediate_known = memory.offset_known;
        instruction.writeback_immediate = memory.writeback;
        instruction.writeback_known = memory.writeback_known;
        instruction.memory_mode = memory.mode;
        return instruction;
    }

    instruction.opcode = Opcode::Other;
    return instruction;
}

std::string Instruction::toString() const {
    if (!raw.empty()) {
        return raw;
    }

    const auto reg = [](const std::string& value) {
        return value.empty() ? std::string("?") : value;
    };
    const auto memory = [this]() {
        std::string result = "[";
        result += rn.empty() ? "?" : rn;
        if (immediate != 0) {
            result += ", #" + std::to_string(immediate);
        }
        result += "]";
        if (memory_mode == MemoryMode::PreIndex) {
            result += "!";
        } else if (memory_mode == MemoryMode::PostIndex) {
            result += ", #" + std::to_string(writeback_immediate);
        }
        return result;
    };

    switch (opcode) {
        case Opcode::Other:
            return "other";
        case Opcode::Nop:
            return "nop";
        case Opcode::BtiC:
            return "bti c";
        case Opcode::BtiJ:
            return "bti j";
        case Opcode::BtiJC:
            return "bti jc";
        case Opcode::Paciasp:
        case Opcode::Pacibsp:
        case Opcode::Autiasp:
        case Opcode::Autibsp:
        case Opcode::Retaa:
        case Opcode::Retab:
        case Opcode::Eret:
        case Opcode::Brk:
            return pacbti::toString(opcode);
        case Opcode::Pacia:
        case Opcode::Pacib:
        case Opcode::Pacda:
        case Opcode::Pacdb:
        case Opcode::Autia:
        case Opcode::Autib:
        case Opcode::Autda:
        case Opcode::Autdb:
            return std::string(pacbti::toString(opcode)) + " " + reg(rd) + ", " +
                   reg(rn);
        case Opcode::Xpaci:
        case Opcode::Xpacd:
            return std::string(pacbti::toString(opcode)) + " " + reg(rd);
        case Opcode::Ret:
            return "ret " + reg(rn.empty() ? "x30" : rn);
        case Opcode::Blr:
        case Opcode::Br:
            return std::string(pacbti::toString(opcode)) + " " + reg(rn);
        case Opcode::Bl:
        case Opcode::B:
        case Opcode::BCond:
            return std::string(pacbti::toString(opcode)) + " " + target;
        case Opcode::Cbz:
        case Opcode::Cbnz:
        case Opcode::Tbz:
        case Opcode::Tbnz:
            return std::string(pacbti::toString(opcode)) + " " + reg(rn) + ", " +
                   target;
        case Opcode::Str:
        case Opcode::Ldr:
            return std::string(pacbti::toString(opcode)) + " " + reg(rd) + ", " +
                   memory();
        case Opcode::Stp:
        case Opcode::Ldp:
            return std::string(pacbti::toString(opcode)) + " " + reg(rd) + ", " +
                   reg(rt2) + ", " + memory();
        case Opcode::Add:
        case Opcode::Sub:
            return std::string(pacbti::toString(opcode)) + " " + reg(rd) + ", " +
                   reg(rn) + ", #" + std::to_string(immediate);
        case Opcode::Mov:
            return "mov " + reg(rd) + ", " + reg(rn);
    }
    return "other";
}

bool VerificationReport::ok() const noexcept {
    for (const Diagnostic& diagnostic : diagnostics) {
        if (diagnostic.severity == Severity::Error) {
            return false;
        }
    }
    return true;
}

std::string VerificationReport::format() const {
    if (diagnostics.empty()) {
        return "PAC/BTI verification passed\n";
    }
    std::ostringstream output;
    for (const Diagnostic& diagnostic : diagnostics) {
        output << "[" << toString(diagnostic.severity) << "] "
               << toString(diagnostic.code) << " at "
               << diagnostic.function << ":" << diagnostic.block << "["
               << diagnostic.instruction_index << "]: "
               << diagnostic.message << "\n";
        if (!diagnostic.path.empty()) {
            output << "  shortest witness path (" << diagnostic.path.size()
                   << " instructions):\n";
            for (const PathStep& step : diagnostic.path) {
                output << "    " << step.function << ":" << step.block << "["
                       << step.instruction_index << "] " << step.text << "\n";
            }
        }
    }
    return output.str();
}

VerificationReport verify(const Program& program, const Options& options) {
    Verifier verifier(program, options);
    return verifier.run();
}

}  // namespace pacbti
