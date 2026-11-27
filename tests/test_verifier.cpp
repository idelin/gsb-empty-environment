#include "pacbti/verifier.hpp"

#include <initializer_list>
#include <iostream>
#include <string>
#include <vector>

namespace {

using pacbti::BasicBlock;
using pacbti::BranchPurpose;
using pacbti::BranchSite;
using pacbti::ConditionTrust;
using pacbti::Diagnostic;
using pacbti::DiagnosticCode;
using pacbti::ExceptionReturnPoint;
using pacbti::ExportEntry;
using pacbti::Function;
using pacbti::IndirectBranchTarget;
using pacbti::Instruction;
using pacbti::Program;
using pacbti::VerificationReport;

int failures = 0;

#define CHECK(condition)                                                      \
    do {                                                                      \
        if (!(condition)) {                                                   \
            std::cerr << "CHECK failed at " << __FILE__ << ":" << __LINE__    \
                      << ": " #condition "\n";                                \
            ++failures;                                                       \
        }                                                                     \
    } while (false)

BasicBlock block(const std::string& name,
                 std::initializer_list<const char*> instructions,
                 const std::string& fallthrough = std::string()) {
    BasicBlock result;
    result.name = name;
    result.fallthrough = fallthrough;
    for (const char* text : instructions) {
        result.instructions.push_back(Instruction::parse(text));
    }
    return result;
}

Function function(const std::string& name,
                  std::initializer_list<BasicBlock> blocks) {
    Function result;
    result.name = name;
    result.entry = blocks.size() == 0 ? std::string() : blocks.begin()->name;
    for (const BasicBlock& basicBlock : blocks) {
        result.blocks.push_back(basicBlock);
    }
    return result;
}

ExportEntry exportEntry(const std::string& name,
                        const std::string& functionName,
                        const std::string& blockName) {
    ExportEntry entry;
    entry.name = name;
    entry.function = functionName;
    entry.block = blockName;
    return entry;
}

IndirectBranchTarget target(const std::string& functionName,
                            const std::string& blockName,
                            std::size_t instructionIndex,
                            const std::string& targetFunction,
                            const std::string& targetBlock,
                            BranchPurpose purpose = BranchPurpose::Auto,
                            const std::string& targetRegister = std::string()) {
    IndirectBranchTarget result;
    result.site.function = functionName;
    result.site.block = blockName;
    result.site.instruction_index = instructionIndex;
    result.target_function = targetFunction;
    result.target_block = targetBlock;
    result.purpose = purpose;
    result.target_register = targetRegister;
    return result;
}

bool hasCode(const VerificationReport& report, DiagnosticCode code) {
    for (const Diagnostic& diagnostic : report.diagnostics) {
        if (diagnostic.code == code) {
            return true;
        }
    }
    return false;
}

std::size_t countCode(const VerificationReport& report, DiagnosticCode code) {
    std::size_t count = 0;
    for (const Diagnostic& diagnostic : report.diagnostics) {
        if (diagnostic.code == code) {
            ++count;
        }
    }
    return count;
}

const Diagnostic* firstWithCode(const VerificationReport& report,
                                DiagnosticCode code) {
    for (const Diagnostic& diagnostic : report.diagnostics) {
        if (diagnostic.code == code) {
            return &diagnostic;
        }
    }
    return nullptr;
}

void printReportOnFailure(const VerificationReport& report) {
    if (!report.diagnostics.empty()) {
        std::cerr << report.format();
    }
}

void testValidPrologueEpilogueAndCallLandingPad() {
    Program program;
    program.functions.push_back(function(
        "caller",
        {block("entry",
               {"bti c",
                "paciasp",
                "stp x29, x30, [sp, #-16]!",
                "mov x29, sp",
                "blr x3",
                "ldp x29, x30, [sp], #16",
                "autiasp",
                "ret"})}));
    program.functions.push_back(function(
        "callee",
        {block("entry",
               {"bti c", "paciasp", "add x0, x0, #1", "autiasp", "ret"})}));
    program.exports.push_back(exportEntry("caller", "caller", "entry"));
    program.exports.push_back(exportEntry("callee", "callee", "entry"));
    program.indirect_targets.push_back(
        target("caller", "entry", 4, "callee", "entry"));

    const VerificationReport report = pacbti::verify(program);
    printReportOnFailure(report);
    CHECK(report.ok());
    CHECK(report.diagnostics.empty());
}

void testImplicitPaciaspCallLandingPad() {
    Program program;
    program.functions.push_back(function(
        "caller",
        {block("entry", {"bti c", "blr x4", "ret"})}));
    program.functions.push_back(function(
        "callee",
        {block("entry", {"paciasp", "autiasp", "ret"})}));
    program.exports.push_back(exportEntry("caller", "caller", "entry"));
    program.indirect_targets.push_back(
        target("caller", "entry", 1, "callee", "entry"));

    const VerificationReport report = pacbti::verify(program);
    printReportOnFailure(report);
    CHECK(report.ok());
}

void testX16AndX17AreExemptFromBtiTargetCheck() {
    Program program;
    program.functions.push_back(function(
        "veneer_user_x16",
        {block("entry", {"bti c", "br x16"})}));
    program.functions.push_back(function(
        "veneer_user_x17",
        {block("entry", {"bti c", "br x17"})}));
    program.exports.push_back(
        exportEntry("veneer_user_x16", "veneer_user_x16", "entry"));
    program.exports.push_back(
        exportEntry("veneer_user_x17", "veneer_user_x17", "entry"));

    const VerificationReport report = pacbti::verify(program);
    printReportOnFailure(report);
    CHECK(report.ok());
}

void testSpDriftAtAuthentication() {
    Program program;
    program.functions.push_back(function(
        "drift",
        {block("entry",
               {"paciasp",
                "sub sp, sp, #16",
                "autiasp",
                "add sp, sp, #16",
                "ret"})}));

    const VerificationReport report = pacbti::verify(program);
    printReportOnFailure(report);
    CHECK(hasCode(report, DiagnosticCode::SpNotRestoredAtAuthentication));
    CHECK(!report.ok());
}

void testSpNotRestoredAtReturn() {
    Program program;
    program.functions.push_back(function(
        "leaky_frame",
        {block("entry",
               {"paciasp", "autiasp", "sub sp, sp, #32", "ret"})}));

    const VerificationReport report = pacbti::verify(program);
    printReportOnFailure(report);
    CHECK(hasCode(report, DiagnosticCode::SpNotRestoredAtReturn));
}

void testUnsignedReturnAddressSpill() {
    Program program;
    program.functions.push_back(function(
        "bad_prologue",
        {block("entry",
               {"stp x29, x30, [sp, #-16]!",
                "paciasp",
                "ldp x29, x30, [sp], #16",
                "autiasp",
                "ret"})}));

    const VerificationReport report = pacbti::verify(program);
    printReportOnFailure(report);
    CHECK(hasCode(report, DiagnosticCode::ReturnAddressSpilledUnsigned));
}

void testPacModifierMismatch() {
    Program program;
    program.functions.push_back(function(
        "wrong_key",
        {block("entry", {"pacibsp", "autiasp", "ret"})}));

    const VerificationReport report = pacbti::verify(program);
    printReportOnFailure(report);
    CHECK(hasCode(report, DiagnosticCode::PacModifierMismatch));
}

void testDataKeyDomainMismatch() {
    Program program;
    program.functions.push_back(function(
        "data_key_mismatch",
        {block("entry", {"pacda x0, sp", "autdb x0, sp", "ret"})}));

    const VerificationReport report = pacbti::verify(program);
    printReportOnFailure(report);
    CHECK(hasCode(report, DiagnosticCode::PacModifierMismatch));
}

void testReturnAddressUsesInstructionKeyDomain() {
    Program program;
    program.functions.push_back(function(
        "data_key_return",
        {block("entry", {"pacda x30, sp", "autda x30, sp", "ret"})}));

    const VerificationReport report = pacbti::verify(program);
    printReportOnFailure(report);
    CHECK(hasCode(report, DiagnosticCode::InvalidReturnKeyDomain));
}

void testGenericPacModifierMismatch() {
    Program program;
    program.functions.push_back(function(
        "wrong_modifier",
        {block("entry", {"pacia x0, x1", "autia x0, x2", "ret"})}));

    const VerificationReport report = pacbti::verify(program);
    printReportOnFailure(report);
    CHECK(hasCode(report, DiagnosticCode::PacModifierMismatch));
}

void testAuthenticationWithoutSigningContext() {
    Program program;
    program.functions.push_back(function(
        "no_signer",
        {block("entry", {"autiasp", "ret"})}));

    const VerificationReport report = pacbti::verify(program);
    printReportOnFailure(report);
    CHECK(hasCode(report, DiagnosticCode::MissingPacContext));
}

void testSignedReturnMustBeAuthenticated() {
    Program program;
    program.functions.push_back(function(
        "unsigned_return",
        {block("entry", {"paciasp", "ret"})}));

    const VerificationReport report = pacbti::verify(program);
    printReportOnFailure(report);
    CHECK(hasCode(report, DiagnosticCode::SignedReturnNotAuthenticated));
}

void testAtomicRetaaIsValid() {
    Program program;
    program.functions.push_back(function(
        "atomic",
        {block("entry", {"bti c", "paciasp", "retaa"})}));
    program.exports.push_back(exportEntry("atomic", "atomic", "entry"));

    const VerificationReport report = pacbti::verify(program);
    printReportOnFailure(report);
    CHECK(report.ok());
}

void testXpaciDowngradeBeforeIndirectBranch() {
    Program program;
    program.functions.push_back(function(
        "stripper",
        {block("entry", {"bti c", "xpaci x3", "br x3"})}));
    program.functions.push_back(function(
        "landing",
        {block("entry", {"bti j", "ret"})}));
    program.exports.push_back(exportEntry("stripper", "stripper", "entry"));
    program.indirect_targets.push_back(
        target("stripper", "entry", 2, "landing", "entry",
               BranchPurpose::Jump));

    const VerificationReport report = pacbti::verify(program);
    printReportOnFailure(report);
    CHECK(hasCode(report, DiagnosticCode::XpaciDowngrade));
}

void testMissingBtiLandingPad() {
    Program program;
    program.functions.push_back(function(
        "caller",
        {block("entry", {"bti c", "blr x3", "ret"})}));
    program.functions.push_back(function(
        "no_pad",
        {block("entry", {"nop", "ret"})}));
    program.exports.push_back(exportEntry("caller", "caller", "entry"));
    program.indirect_targets.push_back(
        target("caller", "entry", 1, "no_pad", "entry"));

    const VerificationReport report = pacbti::verify(program);
    printReportOnFailure(report);
    CHECK(hasCode(report, DiagnosticCode::MissingBtiLandingPad));
}

void testExportRequiresCallLandingPad() {
    Program program;
    program.functions.push_back(function(
        "exported_without_pad",
        {block("entry", {"nop", "ret"})}));
    program.exports.push_back(
        exportEntry("exported_without_pad", "exported_without_pad", "entry"));

    const VerificationReport report = pacbti::verify(program);
    printReportOnFailure(report);
    CHECK(hasCode(report, DiagnosticCode::ExportLandingPadMissing));
}

void testBtiPermissionMismatchForTailCall() {
    Program program;
    program.functions.push_back(function(
        "tail",
        {block("entry", {"bti c", "br x3"})}));
    program.functions.push_back(function(
        "call_only",
        {block("entry", {"bti c", "ret"})}));
    program.exports.push_back(exportEntry("tail", "tail", "entry"));
    program.indirect_targets.push_back(
        target("tail", "entry", 1, "call_only", "entry",
               BranchPurpose::TailCall));

    const VerificationReport report = pacbti::verify(program);
    printReportOnFailure(report);
    CHECK(hasCode(report, DiagnosticCode::BtiPermissionMismatch));
}

void testPacmanOracleReportsShortestWindow() {
    Program program;
    program.functions.push_back(function(
        "oracle",
        {block("entry",
               {"bti c", "paciasp", "autiasp", "cbz x0, use"},
               "fallthrough"),
         block("fallthrough", {"ret"}),
         block("use", {"ldr x1, [x30]", "ret"})}));
    program.exports.push_back(exportEntry("oracle", "oracle", "entry"));

    const VerificationReport report = pacbti::verify(program);
    printReportOnFailure(report);
    const Diagnostic* diagnostic =
        firstWithCode(report, DiagnosticCode::PacmanOracleWindow);
    CHECK(diagnostic != nullptr);
    if (diagnostic != nullptr) {
        CHECK(diagnostic->path.size() == 3);
        CHECK(!diagnostic->path.empty());
        if (!diagnostic->path.empty()) {
            CHECK(diagnostic->path.front().text == "autiasp");
            CHECK(diagnostic->path.front().instruction_index == 2);
            CHECK(diagnostic->path.size() >= 2);
            if (diagnostic->path.size() >= 2) {
                CHECK(diagnostic->path[1].text == "cbz x0, use");
            }
        }
    }
}

void testTrustedConditionClosesPacmanOracle() {
    Program program;
    BasicBlock entry =
        block("entry", {"bti c", "paciasp", "autiasp", "cbz x0, use"},
              "fallthrough");
    entry.instructions[3].condition_trust = ConditionTrust::Trusted;
    program.functions.push_back(function(
        "trusted_branch",
        {entry,
         block("fallthrough", {"ret"}),
         block("use", {"ldr x1, [x30]", "ret"})}));
    program.exports.push_back(
        exportEntry("trusted_branch", "trusted_branch", "entry"));

    const VerificationReport report = pacbti::verify(program);
    printReportOnFailure(report);
    CHECK(!hasCode(report, DiagnosticCode::PacmanOracleWindow));
    CHECK(report.ok());
}

void testPacmanWindowStateDoesNotLeakIntoNextAuthentication() {
    Program program;
    program.functions.push_back(function(
        "window_reset",
        {block("entry",
               {"bti c",
                "pacia x1, sp",
                "autia x1, sp",
                "cbz x0, use_one"},
               "fallthrough"),
         block("fallthrough", {"b use_two"}),
         block("use_one", {"b use_two"}),
         block("use_two",
               {"ldr x2, [x1]",
                "autia x1, sp",
                "ldr x3, [x1]",
                "ret"})}));
    program.exports.push_back(
        exportEntry("window_reset", "window_reset", "entry"));

    const VerificationReport report = pacbti::verify(program);
    printReportOnFailure(report);
    CHECK(countCode(report, DiagnosticCode::PacmanOracleWindow) == 1);
}

void testExceptionReturnLandingPad() {
    Program program;
    program.functions.push_back(function(
        "eret_site",
        {block("entry", {"bti c", "eret"})}));
    program.functions.push_back(function(
        "handler",
        {block("entry", {"bti jc", "ret"})}));
    program.exports.push_back(exportEntry("eret_site", "eret_site", "entry"));

    ExceptionReturnPoint point;
    point.site.function = "eret_site";
    point.site.block = "entry";
    point.site.instruction_index = 1;
    point.target_function = "handler";
    point.target_block = "entry";
    program.exception_returns.push_back(point);

    const VerificationReport report = pacbti::verify(program);
    printReportOnFailure(report);
    CHECK(report.ok());
}

void testExceptionReturnRequiresJumpPermission() {
    Program program;
    program.functions.push_back(function(
        "eret_site",
        {block("entry", {"bti c", "eret"})}));
    program.functions.push_back(function(
        "handler",
        {block("entry", {"bti c", "ret"})}));
    program.exports.push_back(exportEntry("eret_site", "eret_site", "entry"));

    ExceptionReturnPoint point;
    point.site.function = "eret_site";
    point.site.block = "entry";
    point.site.instruction_index = 1;
    point.target_function = "handler";
    point.target_block = "entry";
    program.exception_returns.push_back(point);

    const VerificationReport report = pacbti::verify(program);
    printReportOnFailure(report);
    CHECK(hasCode(report, DiagnosticCode::ExceptionTargetLandingPadMissing));
}

void testTailCallMustRestoreSp() {
    Program program;
    program.functions.push_back(function(
        "tail",
        {block("entry", {"bti c", "sub sp, sp, #16", "br x3"})}));
    program.functions.push_back(function(
        "landing",
        {block("entry", {"bti j", "ret"})}));
    program.exports.push_back(exportEntry("tail", "tail", "entry"));
    program.indirect_targets.push_back(
        target("tail", "entry", 2, "landing", "entry",
               BranchPurpose::TailCall));

    const VerificationReport report = pacbti::verify(program);
    printReportOnFailure(report);
    CHECK(hasCode(report, DiagnosticCode::SpNotRestoredAtTailCall));
}

void testDynamicSpArithmeticBecomesUnknown() {
    const std::optional<Instruction> arithmetic =
        Instruction::tryParse("add sp, sp, x0, lsl #3");
    CHECK(arithmetic.has_value());
    if (arithmetic.has_value()) {
        CHECK(arithmetic->opcode == pacbti::Opcode::Add);
        CHECK(!arithmetic->immediate_known);
    }

    const std::optional<Instruction> memory =
        Instruction::tryParse("ldr x0, [x1, x2]");
    CHECK(memory.has_value());
    if (memory.has_value()) {
        CHECK(!memory->immediate_known);
    }

    Program program;
    program.functions.push_back(function(
        "dynamic_sp",
        {block("entry", {"add sp, sp, x0", "ret"})}));
    const VerificationReport report = pacbti::verify(program);
    printReportOnFailure(report);
    CHECK(hasCode(report, DiagnosticCode::SpOffsetUnknown));
}

void testTbzUsesThirdOperandAsTarget() {
    const Instruction instruction =
        Instruction::parse("tbz x0, #1, slow_path");
    CHECK(instruction.opcode == pacbti::Opcode::Tbz);
    CHECK(instruction.target == "slow_path");
}

}  // namespace

int main() {
    testValidPrologueEpilogueAndCallLandingPad();
    testImplicitPaciaspCallLandingPad();
    testX16AndX17AreExemptFromBtiTargetCheck();
    testSpDriftAtAuthentication();
    testSpNotRestoredAtReturn();
    testUnsignedReturnAddressSpill();
    testPacModifierMismatch();
    testDataKeyDomainMismatch();
    testReturnAddressUsesInstructionKeyDomain();
    testGenericPacModifierMismatch();
    testAuthenticationWithoutSigningContext();
    testSignedReturnMustBeAuthenticated();
    testAtomicRetaaIsValid();
    testXpaciDowngradeBeforeIndirectBranch();
    testMissingBtiLandingPad();
    testExportRequiresCallLandingPad();
    testBtiPermissionMismatchForTailCall();
    testPacmanOracleReportsShortestWindow();
    testTrustedConditionClosesPacmanOracle();
    testPacmanWindowStateDoesNotLeakIntoNextAuthentication();
    testExceptionReturnLandingPad();
    testExceptionReturnRequiresJumpPermission();
    testTailCallMustRestoreSp();
    testDynamicSpArithmeticBecomesUnknown();
    testTbzUsesThirdOperandAsTarget();

    if (failures != 0) {
        std::cerr << failures << " test check(s) failed\n";
        return 1;
    }
    std::cout << "all PAC/BTI verifier tests passed\n";
    return 0;
}
