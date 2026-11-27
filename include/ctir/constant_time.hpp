#pragma once

// Embeddable, zero-dependency constant-time taint verifier for an SSA /
// three-address-code style IR.
//
// The analysis lattice is { PUBLIC < SECRET }. A monotone fixed-point data
// flow analysis tracks both explicit (data) flow and implicit flow through a
// control-dependence graph built from post-dominators: every assignment in a
// block control-dependent on a secret branch is tainted until control reaches
// the post-dominator join point.

#include <algorithm>
#include <cstddef>
#include <map>
#include <set>
#include <sstream>
#include <string>
#include <vector>

namespace ctir {

enum class Label { Public, Secret };

enum class Op {
    Nop,
    Const,
    Copy,
    Add,
    Sub,
    Mul,
    And,
    Orr,
    Xor,
    Shl,
    Shr,
    CmpEq,
    CmpLt,
    Cmov,
    Select,
    Div,
    Rem,
    Load,
    Store,
    Br,
    Jmp,
    Declassify
};

struct Instruction {
    int id = 0;
    Op op = Op::Nop;
    std::string dest;
    std::vector<std::string> operands;
    std::string imm;
    std::vector<int> targets;

    static Instruction make(int instr_id, Op op_in, std::string dest_in,
                            std::vector<std::string> ops = {},
                            std::string imm_in = {},
                            std::vector<int> block_targets = {}) {
        Instruction ins;
        ins.id = instr_id;
        ins.op = op_in;
        ins.dest = std::move(dest_in);
        ins.operands = std::move(ops);
        ins.imm = std::move(imm_in);
        ins.targets = std::move(block_targets);
        return ins;
    }

    static Instruction constant(int instr_id, const std::string& dest_in,
                                const std::string& value) {
        return make(instr_id, Op::Const, dest_in, {}, value);
    }
    static Instruction unary(int instr_id, Op op_in,
                             const std::string& dest_in,
                             const std::string& src) {
        return make(instr_id, op_in, dest_in, {src});
    }
    static Instruction binary(int instr_id, Op op_in,
                              const std::string& dest_in,
                              const std::string& lhs,
                              const std::string& rhs) {
        return make(instr_id, op_in, dest_in, {lhs, rhs});
    }
    static Instruction cmov(int instr_id, const std::string& dest_in,
                            const std::string& cond,
                            const std::string& true_value,
                            const std::string& false_value) {
        return make(instr_id, Op::Cmov, dest_in,
                    {cond, true_value, false_value});
    }
    static Instruction select(int instr_id, const std::string& dest_in,
                              const std::string& cond,
                              const std::string& true_value,
                              const std::string& false_value) {
        return make(instr_id, Op::Select, dest_in,
                    {cond, true_value, false_value});
    }
    static Instruction load(int instr_id, const std::string& dest_in,
                            const std::string& address) {
        return make(instr_id, Op::Load, dest_in, {address});
    }
    static Instruction store(int instr_id, const std::string& address,
                             const std::string& value) {
        return make(instr_id, Op::Store, {}, {address, value});
    }
    static Instruction br(int instr_id, const std::string& cond,
                          int true_block, int false_block) {
        return make(instr_id, Op::Br, {}, {cond}, {},
                    {true_block, false_block});
    }
    static Instruction jmp(int instr_id, int target_block) {
        return make(instr_id, Op::Jmp, {}, {}, {}, {target_block});
    }
    static Instruction declassify(int instr_id, const std::string& value) {
        return make(instr_id, Op::Declassify, {}, {value});
    }
    static Instruction nop(int instr_id) {
        return make(instr_id, Op::Nop, {});
    }
};

struct Block {
    int id = 0;
    std::string name;
    std::vector<Instruction> instrs;
};

struct Parameter {
    std::string name;
    Label label = Label::Public;
};

struct Function {
    std::string name;
    std::vector<Parameter> params;
    std::vector<Block> blocks;
};

inline const char* labelName(Label label) {
    return label == Label::Secret ? "SECRET" : "PUBLIC";
}

inline const char* opName(Op op) {
    switch (op) {
        case Op::Nop: return "NOP";
        case Op::Const: return "CONST";
        case Op::Copy: return "COPY";
        case Op::Add: return "ADD";
        case Op::Sub: return "SUB";
        case Op::Mul: return "MUL";
        case Op::And: return "AND";
        case Op::Orr: return "ORR";
        case Op::Xor: return "XOR";
        case Op::Shl: return "SHL";
        case Op::Shr: return "SHR";
        case Op::CmpEq: return "CMPEQ";
        case Op::CmpLt: return "CMPLT";
        case Op::Cmov: return "CMOV";
        case Op::Select: return "SELECT";
        case Op::Div: return "DIV";
        case Op::Rem: return "REM";
        case Op::Load: return "LOAD";
        case Op::Store: return "STORE";
        case Op::Br: return "BR";
        case Op::Jmp: return "JMP";
        case Op::Declassify: return "DECLASSIFY";
    }
    return "UNKNOWN";
}

enum class ViolationKind {
    BranchOnSecret,
    SecretAddressLoad,
    SecretAddressStore,
    SecretDivRem,
    UnmaskedDeclassify
};

inline const char* violationName(ViolationKind kind) {
    switch (kind) {
        case ViolationKind::BranchOnSecret: return "BRANCH_ON_SECRET";
        case ViolationKind::SecretAddressLoad: return "SECRET_ADDRESS_LOAD";
        case ViolationKind::SecretAddressStore: return "SECRET_ADDRESS_STORE";
        case ViolationKind::SecretDivRem: return "SECRET_DIV_REM";
        case ViolationKind::UnmaskedDeclassify:
            return "UNMASKED_DECLASSIFY";
    }
    return "UNKNOWN";
}

struct TaintInfoView {
    Label label = Label::Public;
    std::vector<std::string> sources;
    std::vector<int> witness;
    bool safeToDeclassify = false;
};

struct Violation {
    ViolationKind kind = ViolationKind::BranchOnSecret;
    int instructionId = 0;
    std::vector<std::string> sources;
    std::vector<int> witness;
    std::string message;
};

struct DeclassEvent {
    int instructionId = 0;
    std::string value;
    bool approved = false;
    std::vector<std::string> sources;
    std::vector<int> witness;
};

struct Report {
    bool ok = false;
    std::vector<std::string> errors;
    std::vector<Violation> violations;
    std::vector<DeclassEvent> declassifications;
    std::map<std::string, TaintInfoView> values;
};

namespace detail {

struct ValueInfo {
    Label label = Label::Public;
    std::set<std::string> sources;
    std::vector<int> witness;
    bool safeToDeclassify = false;

    bool isSecret() const { return label == Label::Secret; }
};

inline bool sameInfo(const ValueInfo& a, const ValueInfo& b) {
    return a.label == b.label && a.sources == b.sources &&
           a.witness == b.witness &&
           a.safeToDeclassify == b.safeToDeclassify;
}

// Lattice join. Witnesses are kept deterministic: the longer propagation
// chain is retained as the representative path.
inline ValueInfo joinInfo(const ValueInfo& a, const ValueInfo& b) {
    if (a.label == Label::Public) return b;
    if (b.label == Label::Public) return a;
    ValueInfo result = a;
    result.sources.insert(b.sources.begin(), b.sources.end());
    if (b.witness.size() > result.witness.size()) {
        result.witness = b.witness;
    }
    result.safeToDeclassify =
        a.safeToDeclassify && b.safeToDeclassify;
    return result;
}

inline bool hasDestination(Op op) {
    switch (op) {
        case Op::Store:
        case Op::Br:
        case Op::Jmp:
        case Op::Declassify:
        case Op::Nop:
            return false;
        default:
            return true;
    }
}

inline bool isBinaryAlu(Op op) {
    switch (op) {
        case Op::Add:
        case Op::Sub:
        case Op::Mul:
        case Op::And:
        case Op::Orr:
        case Op::Xor:
        case Op::Shl:
        case Op::Shr:
        case Op::CmpEq:
        case Op::CmpLt:
        case Op::Div:
        case Op::Rem:
            return true;
        default:
            return false;
    }
}

inline bool isComparison(Op op) {
    return op == Op::CmpEq || op == Op::CmpLt;
}

}  // namespace detail

class Analyzer {
public:
    explicit Analyzer(Function function) : function_(std::move(function)) {}

    Report run() {
        Report report;
        report.errors = validate();
        if (!report.errors.empty()) {
            report.ok = false;
            return report;
        }

        buildGraph();
        computePostDominators();
        buildControlDependencies();
        analyzeFixedPoint();
        collectSinks(report);

        for (const auto& entry : env_) {
            TaintInfoView view;
            view.label = entry.second.label;
            view.sources.assign(entry.second.sources.begin(),
                                entry.second.sources.end());
            view.witness = entry.second.witness;
            view.safeToDeclassify = entry.second.safeToDeclassify;
            report.values.emplace(entry.first, view);
        }
        report.ok = report.violations.empty();
        return report;
    }

    static std::string renderReport(const Report& report) {
        std::ostringstream out;
        for (const std::string& error : report.errors) {
            out << "ERROR: " << error << '\n';
        }
        for (const Violation& violation : report.violations) {
            out << "VIOLATION[" << violationName(violation.kind)
                << "] instruction=" << violation.instructionId << " sources={";
            appendStringList(out, violation.sources);
            out << "} witness=";
            appendWitness(out, violation.witness);
            out << " : " << violation.message << '\n';
        }
        for (const DeclassEvent& event : report.declassifications) {
            out << "DECLASSIFY instruction=" << event.instructionId
                << (event.approved ? " APPROVED" : " REJECTED")
                << " sources={";
            appendStringList(out, event.sources);
            out << "} witness=";
            appendWitness(out, event.witness);
            out << '\n';
        }
        for (const auto& entry : report.values) {
            const TaintInfoView& info = entry.second;
            out << "VALUE " << entry.first << " : " << labelName(info.label);
            if (info.label == Label::Secret) {
                out << " sources={";
                appendStringList(out, info.sources);
                out << "} witness=";
                appendWitness(out, info.witness);
                out << (info.safeToDeclassify ? " release=safe"
                                              : " release=unmasked");
            }
            out << '\n';
        }
        out << "RESULT: " << (report.ok ? "CONSTANT_TIME_OK"
                                        : "CONSTANT_TIME_VIOLATION")
            << '\n';
        return out.str();
    }

private:
    using ValueInfo = detail::ValueInfo;

    static constexpr int kMaxIterations = 10000;

    Function function_;
    int blockCount_ = 0;
    int virtualExit_ = 0;
    std::vector<std::vector<int>> successors_;
    std::vector<std::vector<int>> predecessors_;
    std::vector<std::vector<char>> postDominates_;
    std::vector<int> immediatePostDom_;
    std::vector<std::vector<int>> controlDependence_;
    std::vector<int> forwardOrder_;
    std::map<std::string, ValueInfo> env_;

    static void appendStringList(
        std::ostringstream& out, const std::vector<std::string>& values) {
        for (std::size_t i = 0; i < values.size(); ++i) {
            if (i != 0U) out << ',';
            out << values[i];
        }
    }

    static void appendWitness(std::ostringstream& out,
                              const std::vector<int>& witness) {
        out << '[';
        for (std::size_t i = 0; i < witness.size(); ++i) {
            if (i != 0U) out << "->";
            out << witness[i];
        }
        out << ']';
    }

    const Block* findBlock(int block_id) const {
        if (block_id < 0 || block_id >= blockCount_) return nullptr;
        return &function_.blocks[static_cast<std::size_t>(block_id)];
    }

    const Instruction* terminatorOf(int block_id) const {
        const Block* block = findBlock(block_id);
        if (block == nullptr || block->instrs.empty()) return nullptr;
        const Instruction& last = block->instrs.back();
        if (last.op != Op::Br && last.op != Op::Jmp) return nullptr;
        return &last;
    }

    std::vector<std::string> validate() {
        std::vector<std::string> errors;
        blockCount_ = static_cast<int>(function_.blocks.size());
        if (blockCount_ == 0) {
            errors.emplace_back("function has no basic blocks");
            return errors;
        }

        std::set<std::string> block_names;
        std::set<int> block_ids;
        std::set<std::string> defined;
        std::set<std::string> parameter_names;
        std::set<int> instruction_ids;

        for (const Parameter& parameter : function_.params) {
            if (parameter.name.empty()) {
                errors.emplace_back("parameter with empty name");
                continue;
            }
            if (!parameter_names.insert(parameter.name).second) {
                errors.emplace_back("duplicate parameter name: " +
                                    parameter.name);
            }
            defined.insert(parameter.name);
        }

        for (const Block& block : function_.blocks) {
            if (!block_ids.insert(block.id).second) {
                errors.emplace_back("duplicate block id: " +
                                    std::to_string(block.id));
            }
            if (!block.name.empty() &&
                !block_names.insert(block.name).second) {
                errors.emplace_back("duplicate block name: " + block.name);
            }
            for (const Instruction& instr : block.instrs) {
                if (instr.id < 0) {
                    errors.emplace_back("instruction id must be non-negative");
                }
                if (!instruction_ids.insert(instr.id).second) {
                    errors.emplace_back("duplicate instruction id: " +
                                        std::to_string(instr.id));
                }
                if (detail::hasDestination(instr.op)) {
                    if (instr.dest.empty()) {
                        errors.emplace_back(
                            "instruction " + std::to_string(instr.id) +
                            " is missing a destination SSA name");
                    } else if (!defined.insert(instr.dest).second) {
                        errors.emplace_back(
                            "SSA name defined more than once: " + instr.dest +
                            " (instruction " +
                            std::to_string(instr.id) + ")");
                    }
                }
                checkArity(instr, errors);
            }
        }

        for (const Block& block : function_.blocks) {
            if (block.id < 0 || block.id >= blockCount_) {
                errors.emplace_back("block id out of range: " +
                                    std::to_string(block.id));
                continue;
            }
            for (const Instruction& instr : block.instrs) {
                for (const int target : instr.targets) {
                    if (target < 0 || target >= blockCount_) {
                        errors.emplace_back(
                            "instruction " + std::to_string(instr.id) +
                            " targets unknown block " +
                            std::to_string(target));
                    }
                }
                if (instr.op == Op::Br && instr.targets.size() != 2U) {
                    errors.emplace_back(
                        "BR instruction " + std::to_string(instr.id) +
                        " must have two block targets");
                }
                if (instr.op == Op::Jmp && instr.targets.size() != 1U) {
                    errors.emplace_back(
                        "JMP instruction " + std::to_string(instr.id) +
                        " must have one block target");
                }
            }
        }

        for (int expected = 0; expected < blockCount_; ++expected) {
            if (block_ids.find(expected) == block_ids.end()) {
                errors.emplace_back(
                    "block ids must be contiguous starting at 0; missing " +
                    std::to_string(expected));
            }
        }

        // Use-def checks are performed after all definitions are known.
        for (const Block& block : function_.blocks) {
            for (const Instruction& instr : block.instrs) {
                for (const std::string& operand : instr.operands) {
                    if (defined.find(operand) == defined.end()) {
                        errors.emplace_back(
                            "instruction " + std::to_string(instr.id) +
                            " uses undefined SSA name: " + operand);
                    }
                }
            }
        }
        return errors;
    }

    static void checkArity(const Instruction& instr,
                           std::vector<std::string>& errors) {
        auto fail = [&](std::size_t expected) {
            errors.emplace_back(
                "instruction " + std::to_string(instr.id) + " (" +
                opName(instr.op) + ") expects " +
                std::to_string(expected) + " operand(s), got " +
                std::to_string(instr.operands.size()));
        };
        switch (instr.op) {
            case Op::Const:
            case Op::Nop:
            case Op::Jmp:
                break;
            case Op::Copy:
            case Op::Load:
            case Op::Declassify:
                if (instr.operands.size() != 1U) fail(1);
                break;
            case Op::Store:
                if (instr.operands.size() != 2U) fail(2);
                break;
            case Op::Br:
                if (instr.operands.size() != 1U) fail(1);
                break;
            case Op::Cmov:
            case Op::Select:
                if (instr.operands.size() != 3U) fail(3);
                break;
            default:
                if (detail::isBinaryAlu(instr.op) &&
                    instr.operands.size() != 2U) {
                    fail(2);
                }
                break;
        }
    }

    void buildGraph() {
        blockCount_ = static_cast<int>(function_.blocks.size());
        virtualExit_ = blockCount_;
        int nodes = blockCount_ + 1;
        successors_.assign(static_cast<std::size_t>(nodes), {});
        predecessors_.assign(static_cast<std::size_t>(nodes), {});

        for (int block_id = 0; block_id < blockCount_; ++block_id) {
            const Instruction* terminator = terminatorOf(block_id);
            if (terminator == nullptr) {
                addEdge(block_id, virtualExit_);
                continue;
            }
            for (int target : terminator->targets) {
                addEdge(block_id, target);
            }
        }

        std::vector<char> reached(static_cast<std::size_t>(nodes), 0);
        std::vector<int> post_order;
        reversePostDfs(0, reached, post_order);
        forwardOrder_.clear();
        for (auto it = post_order.rbegin(); it != post_order.rend(); ++it) {
            forwardOrder_.push_back(*it);
        }
    }

    void addEdge(int from, int to) {
        successors_[static_cast<std::size_t>(from)].push_back(to);
        predecessors_[static_cast<std::size_t>(to)].push_back(from);
    }

    void reversePostDfs(int node, std::vector<char>& reached,
                        std::vector<int>& post_order) const {
        reached[static_cast<std::size_t>(node)] = 1;
        for (int next : successors_[static_cast<std::size_t>(node)]) {
            if (reached[static_cast<std::size_t>(next)] == 0) {
                reversePostDfs(next, reached, post_order);
            }
        }
        post_order.push_back(node);
    }

    void computePostDominators() {
        int nodes = blockCount_ + 1;
        std::vector<char> reached(static_cast<std::size_t>(nodes), 0);
        std::vector<int> reverse_post_order;
        reverseGraphRpo(virtualExit_, reached, reverse_post_order);

        std::vector<std::vector<char>> dom(
            static_cast<std::size_t>(nodes),
            std::vector<char>(static_cast<std::size_t>(nodes), 1));
        dom[static_cast<std::size_t>(virtualExit_)]
            .assign(static_cast<std::size_t>(nodes), 0);
        dom[static_cast<std::size_t>(virtualExit_)]
             [static_cast<std::size_t>(virtualExit_)] = 1;

        bool changed = true;
        while (changed) {
            changed = false;
            for (int node : reverse_post_order) {
                if (node == virtualExit_) continue;
                std::vector<char> incoming(
                    static_cast<std::size_t>(nodes), -1);
                bool have_predecessor = false;
                // In the reversed graph the predecessors of node are the
                // successors of node in the original CFG.
                for (int reverse_pred :
                     successors_[static_cast<std::size_t>(node)]) {
                    if (reached[static_cast<std::size_t>(reverse_pred)] == 0) {
                        continue;
                    }
                    have_predecessor = true;
                    for (int candidate = 0; candidate < nodes; ++candidate) {
                        char bit =
                            dom[static_cast<std::size_t>(reverse_pred)]
                               [static_cast<std::size_t>(candidate)];
                        if (incoming[static_cast<std::size_t>(candidate)] ==
                            -1) {
                            incoming[static_cast<std::size_t>(candidate)] =
                                bit;
                        } else {
                            incoming[static_cast<std::size_t>(candidate)] =
                                static_cast<char>(
                                    incoming[static_cast<std::size_t>(
                                        candidate)] &
                                    bit);
                        }
                    }
                }
                if (!have_predecessor) continue;

                std::vector<char> updated = incoming;
                updated[static_cast<std::size_t>(node)] = 1;
                if (updated != dom[static_cast<std::size_t>(node)]) {
                    dom[static_cast<std::size_t>(node)] = updated;
                    changed = true;
                }
            }
        }

        postDominates_ = std::move(dom);
        immediatePostDom_.assign(static_cast<std::size_t>(nodes), -1);
        for (int node = 0; node < nodes; ++node) {
            if (node == virtualExit_) continue;
            int deepest = -1;
            int deepest_size = -1;
            for (int candidate = 0; candidate < nodes; ++candidate) {
                if (candidate == node) continue;
                if (postDominates_[static_cast<std::size_t>(candidate)]
                                  [static_cast<std::size_t>(node)] == 0) {
                    continue;
                }
                int candidate_size = countSet(
                    postDominates_[static_cast<std::size_t>(candidate)]);
                if (candidate_size > deepest_size) {
                    deepest_size = candidate_size;
                    deepest = candidate;
                }
            }
            immediatePostDom_[static_cast<std::size_t>(node)] = deepest;
        }
    }

    void reverseGraphRpo(int node, std::vector<char>& reached,
                         std::vector<int>& post_order) const {
        reached[static_cast<std::size_t>(node)] = 1;
        for (int next : predecessors_[static_cast<std::size_t>(node)]) {
            if (reached[static_cast<std::size_t>(next)] == 0) {
                reverseGraphRpo(next, reached, post_order);
            }
        }
        post_order.push_back(node);
    }

    static int countSet(const std::vector<char>& bits) {
        int total = 0;
        for (char bit : bits) {
            if (bit != 0) ++total;
        }
        return total;
    }

    void buildControlDependencies() {
        int nodes = blockCount_ + 1;
        controlDependence_.assign(static_cast<std::size_t>(blockCount_), {});
        for (int branch = 0; branch < blockCount_; ++branch) {
            const Instruction* terminator = terminatorOf(branch);
            if (terminator == nullptr || terminator->op != Op::Br) continue;
            for (int successor :
                 successors_[static_cast<std::size_t>(branch)]) {
                int walker = successor;
                int guard = 0;
                while (walker != -1 && guard <= nodes &&
                       !postDominates(walker, branch)) {
                    addControlDependence(walker, branch);
                    walker =
                        immediatePostDom_[static_cast<std::size_t>(walker)];
                    ++guard;
                }
            }
        }
        for (auto& dependencies : controlDependence_) {
            std::sort(dependencies.begin(), dependencies.end());
            dependencies.erase(
                std::unique(dependencies.begin(), dependencies.end()),
                dependencies.end());
        }
    }

    void addControlDependence(int block_id, int branch_block) {
        std::vector<int>& dependencies =
            controlDependence_[static_cast<std::size_t>(block_id)];
        if (std::find(dependencies.begin(), dependencies.end(),
                      branch_block) == dependencies.end()) {
            dependencies.push_back(branch_block);
        }
    }

    bool postDominates(int candidate, int block_id) const {
        return postDominates_[static_cast<std::size_t>(candidate)]
                             [static_cast<std::size_t>(block_id)] != 0;
    }

    void analyzeFixedPoint() {
        env_.clear();
        for (const Parameter& parameter : function_.params) {
            ValueInfo info;
            info.label = parameter.label;
            if (parameter.label == Label::Secret) {
                info.sources.insert(parameter.name);
                info.witness = {};
                info.safeToDeclassify = false;
            }
            env_.emplace(parameter.name, info);
        }

        for (int iteration = 0; iteration < kMaxIterations; ++iteration) {
            bool changed = false;
            for (int block_id : forwardOrder_) {
                if (block_id == virtualExit_) continue;
                transferBlock(block_id, &changed);
            }
            if (!changed) return;
        }
    }

    const ValueInfo& lookup(const std::string& name) const {
        static const ValueInfo empty_info;
        auto found = env_.find(name);
        if (found == env_.end()) return empty_info;
        return found->second;
    }

    // PC taint of a block: the block is control-dependent on branches; if
    // any branch condition is secret, assignments inside the block receive
    // implicit-flow taint up to the post-dominator join.
    ValueInfo pcInfo(int block_id) const {
        ValueInfo pc;
        for (int branch_block :
             controlDependence_[static_cast<std::size_t>(block_id)]) {
            const Instruction* terminator = terminatorOf(branch_block);
            if (terminator == nullptr) continue;
            if (terminator->operands.empty()) continue;
            const ValueInfo& condition =
                lookup(terminator->operands.front());
            if (condition.isSecret()) {
                ValueInfo branch_pc = condition;
                branch_pc.witness =
                    extendWitness(condition.witness, terminator->id);
                pc = detail::joinInfo(pc, branch_pc);
            }
        }
        return pc;
    }

    static std::vector<int> extendWitness(const std::vector<int>& witness,
                                          int instruction_id) {
        std::vector<int> extended = witness;
        extended.push_back(instruction_id);
        return extended;
    }

    void transferBlock(int block_id, bool* changed) {
        const Block& block =
            function_.blocks[static_cast<std::size_t>(block_id)];
        ValueInfo pc = pcInfo(block_id);
        for (const Instruction& instr : block.instrs) {
            if (instr.op == Op::Nop || instr.op == Op::Br ||
                instr.op == Op::Jmp || instr.op == Op::Store ||
                instr.op == Op::Declassify) {
                continue;
            }

            ValueInfo candidate = computeValue(instr, pc);
            if (instr.dest.empty()) continue;
            auto found = env_.find(instr.dest);
            if (found == env_.end()) {
                env_.emplace(instr.dest, candidate);
                *changed = true;
            } else {
                ValueInfo merged = detail::joinInfo(found->second, candidate);
                if (!detail::sameInfo(merged, found->second)) {
                    found->second = merged;
                    *changed = true;
                }
            }
        }
    }

    ValueInfo computeValue(const Instruction& instr, const ValueInfo& pc) {
        switch (instr.op) {
            case Op::Const:
                return ValueInfo{};
            case Op::Copy:
                return applyPc(instr, lookup(instr.operands.front()), pc);
            case Op::Cmov:
            case Op::Select:
                return computeCmov(instr, pc);
            case Op::Load:
                return computeLoad(instr, pc);
            case Op::Div:
            case Op::Rem:
                return computeBinary(instr, pc, false);
            default:
                if (detail::isBinaryAlu(instr.op)) {
                    bool safe_reduction = detail::isComparison(instr.op);
                    return computeBinary(instr, pc, safe_reduction);
                }
                return applyPc(instr, ValueInfo{}, pc);
        }
    }

    ValueInfo applyPc(const Instruction& instr, ValueInfo value,
                      const ValueInfo& pc) {
        if (!pc.isSecret()) return value;
        ValueInfo tainted = pc;
        if (value.isSecret()) {
            tainted = detail::joinInfo(tainted, value);
        }
        tainted.witness = extendWitness(tainted.witness, instr.id);
        tainted.safeToDeclassify = false;
        return tainted;
    }

    ValueInfo mergeOperands(const Instruction& instr, const ValueInfo& pc,
                            bool safe_reduction) {
        ValueInfo merged;
        for (const std::string& operand : instr.operands) {
            merged = detail::joinInfo(merged, lookup(operand));
        }
        if (merged.isSecret()) {
            merged.witness = extendWitness(merged.witness, instr.id);
            merged.safeToDeclassify = safe_reduction;
        }
        return applyPc(instr, merged, pc);
    }

    ValueInfo computeBinary(const Instruction& instr, const ValueInfo& pc,
                            bool safe_reduction) {
        return mergeOperands(instr, pc, safe_reduction);
    }

    ValueInfo computeCmov(const Instruction& instr, const ValueInfo& pc) {
        const ValueInfo& cond = lookup(instr.operands[0]);
        const ValueInfo& true_value = lookup(instr.operands[1]);
        const ValueInfo& false_value = lookup(instr.operands[2]);

        ValueInfo merged =
            detail::joinInfo(detail::joinInfo(cond, true_value), false_value);
        if (!merged.isSecret()) {
            return applyPc(instr, ValueInfo{}, pc);
        }

        // A branch-free select is an approved release primitive when it
        // reduces a secret predicate to a public-domain value: either both
        // data arms are public, or every secret arm is itself an approved
        // reduced/masked value.
        bool secret_arms_masked = true;
        for (std::size_t i = 1; i < instr.operands.size(); ++i) {
            const ValueInfo& arm = lookup(instr.operands[i]);
            if (arm.isSecret() && !arm.safeToDeclassify) {
                secret_arms_masked = false;
            }
        }
        merged.witness = extendWitness(merged.witness, instr.id);
        merged.safeToDeclassify = secret_arms_masked;
        return applyPc(instr, merged, pc);
    }

    ValueInfo computeLoad(const Instruction& instr, const ValueInfo& pc) {
        const ValueInfo& address = lookup(instr.operands.front());
        ValueInfo loaded = address;
        if (loaded.isSecret()) {
            loaded.witness = extendWitness(loaded.witness, instr.id);
            loaded.safeToDeclassify = false;
        }
        // Even a public-address load inside a secret-dependent region leaks
        // through the program counter; conservatively mark it tainted.
        return applyPc(instr, loaded, pc);
    }

    void collectSinks(Report& report) {
        for (int block_id : forwardOrder_) {
            if (block_id == virtualExit_) continue;
            const Block& block =
                function_.blocks[static_cast<std::size_t>(block_id)];
            for (const Instruction& instr : block.instrs) {
                switch (instr.op) {
                    case Op::Br:
                        collectBranch(instr, report);
                        break;
                    case Op::Load:
                        if (lookup(instr.operands.front()).isSecret()) {
                            addViolation(report, instr,
                                         ViolationKind::SecretAddressLoad,
                                         lookup(instr.operands.front()),
                                         "LOAD address is derived from a "
                                         "secret value (cache-timed table "
                                         "lookup)");
                        }
                        break;
                    case Op::Store:
                        if (lookup(instr.operands.front()).isSecret()) {
                            addViolation(report, instr,
                                         ViolationKind::SecretAddressStore,
                                         lookup(instr.operands.front()),
                                         "STORE address is derived from a "
                                         "secret value (cache-timed write)");
                        }
                        break;
                    case Op::Div:
                    case Op::Rem:
                        collectDivRem(instr, report);
                        break;
                    case Op::Declassify:
                        collectDeclassify(instr, report);
                        break;
                    default:
                        break;
                }
            }
        }

        std::sort(report.violations.begin(), report.violations.end(),
                  [](const Violation& a, const Violation& b) {
                      if (a.instructionId != b.instructionId) {
                          return a.instructionId < b.instructionId;
                      }
                      return violationName(a.kind) < violationName(b.kind);
                  });
        report.violations.erase(
            std::unique(report.violations.begin(), report.violations.end(),
                        [](const Violation& a, const Violation& b) {
                            return a.instructionId == b.instructionId &&
                                   a.kind == b.kind;
                        }),
            report.violations.end());
        std::sort(report.declassifications.begin(),
                  report.declassifications.end(),
                  [](const DeclassEvent& a, const DeclassEvent& b) {
                      return a.instructionId < b.instructionId;
                  });
    }

    void collectBranch(const Instruction& instr, Report& report) {
        const ValueInfo& condition = lookup(instr.operands.front());
        if (condition.isSecret()) {
            addViolation(report, instr, ViolationKind::BranchOnSecret,
                         condition,
                         "BR condition is secret (branch predictor / "
                         "control-flow timing leak)");
        }
    }

    void collectDivRem(const Instruction& instr, Report& report) {
        for (const std::string& operand : instr.operands) {
            const ValueInfo& info = lookup(operand);
            if (info.isSecret()) {
                addViolation(report, instr, ViolationKind::SecretDivRem,
                             info,
                             std::string(opName(instr.op)) +
                                 " operand is secret (variable-latency "
                                 "integer divide/remainder)");
                break;
            }
        }
    }

    void collectDeclassify(const Instruction& instr, Report& report) {
        const ValueInfo& info = lookup(instr.operands.front());
        DeclassEvent event;
        event.instructionId = instr.id;
        event.value = instr.operands.front();
        event.sources.assign(info.sources.begin(), info.sources.end());
        event.witness = info.witness;
        event.approved =
            info.label == Label::Public || info.safeToDeclassify;
        report.declassifications.push_back(event);

        if (info.isSecret() && !info.safeToDeclassify) {
            addViolation(report, instr,
                         ViolationKind::UnmaskedDeclassify, info,
                         "DECLASSIFY releases an unmasked secret before a "
                         "constant-time reduction/masking boundary");
        }
    }

    static void addViolation(Report& report, const Instruction& instr,
                             ViolationKind kind, const ValueInfo& info,
                             const std::string& message) {
        Violation violation;
        violation.kind = kind;
        violation.instructionId = instr.id;
        violation.sources.assign(info.sources.begin(), info.sources.end());
        violation.witness = info.witness;
        violation.message = message;
        report.violations.push_back(violation);
    }
};

}  // namespace ctir
