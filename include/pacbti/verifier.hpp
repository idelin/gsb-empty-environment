#ifndef PACBTI_VERIFIER_HPP
#define PACBTI_VERIFIER_HPP

#include <cstddef>
#include <cstdint>
#include <optional>
#include <string>
#include <vector>

namespace pacbti {

enum class PacKey {
    None,
    IA,
    IB,
    DA,
    DB
};

enum class Opcode {
    Other,
    Nop,
    BtiC,
    BtiJ,
    BtiJC,
    Paciasp,
    Pacibsp,
    Autiasp,
    Autibsp,
    Pacia,
    Pacib,
    Pacda,
    Pacdb,
    Autia,
    Autib,
    Autda,
    Autdb,
    Xpaci,
    Xpacd,
    Ret,
    Retaa,
    Retab,
    Blr,
    Br,
    Bl,
    B,
    BCond,
    Cbz,
    Cbnz,
    Tbz,
    Tbnz,
    Eret,
    Brk,
    Str,
    Ldr,
    Stp,
    Ldp,
    Add,
    Sub,
    Mov
};

enum class MemoryMode {
    Offset,
    PreIndex,
    PostIndex
};

enum class BranchPurpose {
    Auto,
    NormalCall,
    TailCall,
    Jump
};

enum class ConditionTrust {
    Unknown,
    AttackerControlled,
    Trusted
};

enum class ExportKind {
    Function,
    Trampoline,
    Data
};

enum class Severity {
    Error,
    Warning
};

enum class DiagnosticCode {
    InvalidReference,
    InvalidControlFlowEdge,
    MalformedIndirectBranchSite,
    MissingIndirectBranchTarget,
    SpNotRestoredAtAuthentication,
    SpNotRestoredAtReturn,
    SpNotRestoredAtTailCall,
    SpOffsetUnknown,
    PacModifierMismatch,
    MissingPacContext,
    InvalidReturnKeyDomain,
    ReturnAddressSpilledUnsigned,
    SignedReturnNotAuthenticated,
    UnvalidatedSignedPointerBranch,
    XpaciDowngrade,
    MissingBtiLandingPad,
    BtiPermissionMismatch,
    ExportLandingPadMissing,
    InvalidExceptionReturnSite,
    ExceptionTargetLandingPadMissing,
    PacmanOracleWindow,
    AnalysisLimit
};

struct Instruction {
    Opcode opcode = Opcode::Other;

    // rd is the destination, or the transfer register for loads/stores.
    // rn is the first source, memory base, or indirect branch register.
    // rt2 is the second transfer register for LDP/STP.
    // rm is an optional second source/modifier register.
    // target is a direct branch destination block name.
    std::string rd;
    std::string rn;
    std::string rt2;
    std::string rm;
    std::string target;
    std::int64_t immediate = 0;
    bool immediate_known = true;
    std::int64_t writeback_immediate = 0;
    bool writeback_known = true;
    MemoryMode memory_mode = MemoryMode::Offset;
    ConditionTrust condition_trust = ConditionTrust::Unknown;
    std::string raw;

    static Instruction parse(const std::string& text);
    static std::optional<Instruction> tryParse(const std::string& text);
    std::string toString() const;
};

struct BasicBlock {
    std::string name;
    std::uint64_t address = 0;
    std::vector<Instruction> instructions;
    std::string fallthrough;
};

struct Function {
    std::string name;
    std::string entry;
    std::vector<BasicBlock> blocks;
};

struct BranchSite {
    std::string function;
    std::string block;
    std::size_t instruction_index = 0;
};

struct IndirectBranchTarget {
    BranchSite site;
    std::string target_function;
    std::string target_block;
    BranchPurpose purpose = BranchPurpose::Auto;
    std::string target_register;
    bool external = false;
};

struct ExportEntry {
    std::string name;
    std::string function;
    std::string block;
    ExportKind kind = ExportKind::Function;
    bool requires_landing_pad = true;
};

struct ExceptionReturnPoint {
    BranchSite site;
    std::string target_function;
    std::string target_block;
    bool check_target_landing_pad = true;
};

struct Program {
    std::vector<Function> functions;
    std::vector<IndirectBranchTarget> indirect_targets;
    std::vector<ExportEntry> exports;
    std::vector<ExceptionReturnPoint> exception_returns;
};

struct Options {
    bool treat_unknown_conditions_as_attacker_controlled = true;
    bool check_exports = true;
    bool check_exception_targets = true;
    std::size_t max_states = 200000;
    std::size_t max_path_instructions = 16384;
};

struct PathStep {
    std::string function;
    std::string block;
    std::size_t instruction_index = 0;
    std::string text;
};

struct Diagnostic {
    DiagnosticCode code = DiagnosticCode::InvalidReference;
    Severity severity = Severity::Error;
    std::string message;
    std::string function;
    std::string block;
    std::size_t instruction_index = 0;
    std::vector<PathStep> path;
};

struct VerificationReport {
    std::vector<Diagnostic> diagnostics;

    bool ok() const noexcept;
    std::string format() const;
};

const char* toString(PacKey key) noexcept;
const char* toString(Opcode opcode) noexcept;
const char* toString(BranchPurpose purpose) noexcept;
const char* toString(ConditionTrust trust) noexcept;
const char* toString(Severity severity) noexcept;
const char* toString(DiagnosticCode code) noexcept;

VerificationReport verify(const Program& program, const Options& options = Options{});

}  // namespace pacbti

#endif
