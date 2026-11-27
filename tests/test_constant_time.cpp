#include "ctir/constant_time.hpp"

#include <cassert>
#include <cstdio>
#include <string>
#include <vector>

namespace {

using ctir::Analyzer;
using ctir::Block;
using ctir::DeclassEvent;
using ctir::Function;
using ctir::Instruction;
using ctir::Label;
using ctir::Op;
using ctir::Parameter;
using ctir::Report;
using ctir::TaintInfoView;
using ctir::Violation;
using ctir::ViolationKind;

int g_failures = 0;

void check(bool condition, const std::string& message) {
    if (!condition) {
        std::printf("FAIL: %s\n", message.c_str());
        ++g_failures;
    }
}

const Violation* findViolation(const Report& report, int instruction_id,
                               ViolationKind kind) {
    for (const Violation& violation : report.violations) {
        if (violation.instructionId == instruction_id &&
            violation.kind == kind) {
            return &violation;
        }
    }
    return nullptr;
}

const DeclassEvent* findEvent(const Report& report, int instruction_id) {
    for (const DeclassEvent& event : report.declassifications) {
        if (event.instructionId == instruction_id) return &event;
    }
    return nullptr;
}

// Standard constant-time masked equality plus conditional swap. Every secret
// branch is compiled into branch-free CMOV/SELECT and arithmetic, so the
// verifier must report zero violations (no false positives).
void testMaskedCompareAndSwap() {
    Function function;
    function.name = "ct_compare_swap";
    function.params = {
        Parameter{"a", Label::Secret},
        Parameter{"b", Label::Secret},
        Parameter{"base", Label::Public},
    };

    Block entry;
    entry.id = 0;
    entry.name = "entry";
    entry.instrs = {
        Instruction::constant(0, "zero", "0"),
        // masked = (a XOR b) == 0 : a boolean reduced from secrets
        Instruction::binary(1, Op::Xor, "diff", "a", "b"),
        Instruction::binary(2, Op::CmpEq, "equal", "diff", "zero"),
        // constant-time conditional swap through SELECT
        Instruction::select(3, "new_a", "equal", "a", "b"),
        Instruction::select(4, "new_b", "equal", "b", "a"),
        // public-address store of a secret value is allowed (no timing sink)
        Instruction::store(5, "base", "new_a"),
        // release only the one-bit comparison result, not the key material
        Instruction::declassify(6, "equal"),
    };
    function.blocks.push_back(entry);

    Report report = Analyzer(std::move(function)).run();
    check(report.errors.empty(), "masked case: valid IR");
    check(report.violations.empty(),
          "masked compare/swap must have zero violations");
    check(report.ok, "masked compare/swap report is constant-time");

    const DeclassEvent* event = findEvent(report, 6);
    check(event != nullptr && event->approved,
          "reduced equality bit may be declassified");
    const auto equal_it = report.values.find("equal");
    check(equal_it != report.values.end() &&
              equal_it->second.label == Label::Secret &&
              equal_it->second.safeToDeclassify,
          "equality bit is secret but release-safe");
    const auto key_it = report.values.find("new_a");
    check(key_it != report.values.end() &&
              key_it->second.label == Label::Secret &&
              !key_it->second.safeToDeclassify,
          "swapped key material stays unmasked");
    check(equal_it->second.sources.size() == 2U,
          "witness tracks both secret source parameters");
}

// S-box style table lookup: table[secret] makes the LOAD address secret.
void testSboxLookup() {
    Function function;
    function.name = "sbox";
    function.params = {
        Parameter{"key", Label::Secret},
        Parameter{"table", Label::Public},
        Parameter{"stride", Label::Public},
    };

    Block entry;
    entry.id = 0;
    entry.name = "entry";
    entry.instrs = {
        Instruction::binary(1, Op::Mul, "offset", "key", "stride"),
        Instruction::binary(2, Op::Add, "address", "table", "offset"),
        Instruction::load(3, "sbox_value", "address"),
    };
    function.blocks.push_back(entry);

    Report report = Analyzer(std::move(function)).run();
    check(report.errors.empty(), "sbox case: valid IR");
    const Violation* violation =
        findViolation(report, 3, ViolationKind::SecretAddressLoad);
    check(violation != nullptr, "S-box secret-index LOAD is detected");
    if (violation != nullptr) {
        check(violation->sources.size() == 1U &&
                  violation->sources.front() == "key",
              "S-box violation names the secret parameter key");
        bool has_path = violation->witness.size() >= 2U &&
                        violation->witness.front() != 3;
        check(has_path, "S-box violation carries propagation witness");
    }
    const auto value_it = report.values.find("sbox_value");
    check(value_it != report.values.end() &&
              value_it->second.label == Label::Secret,
          "loaded S-box value is tainted");
}

// Implicit flow: a secret branch controls assignments in dominated blocks
// until the post-dominator join, and the branch itself is a policy sink.
void testImplicitFlowLeak() {
    Function function;
    function.name = "implicit";
    function.params = {
        Parameter{"secret_cond", Label::Secret},
        Parameter{"public_value", Label::Public},
    };

    Block entry;
    entry.id = 0;
    entry.name = "entry";
    entry.instrs.push_back(
        Instruction::br(1, "secret_cond", 1, 2));

    Block left;
    left.id = 1;
    left.name = "left";
    left.instrs = {
        Instruction::constant(2, "one", "1"),
        Instruction::binary(3, Op::Add, "left_value", "public_value", "one"),
        Instruction::jmp(4, 3),
    };

    Block right;
    right.id = 2;
    right.name = "right";
    right.instrs = {
        Instruction::constant(5, "two", "2"),
        Instruction::binary(6, Op::Mul, "right_value", "public_value", "two"),
        Instruction::jmp(7, 3),
    };

    Block join;
    join.id = 3;
    join.name = "join";
    join.instrs = {
        Instruction::constant(10, "fresh_public", "7"),
        // Branch-free merge: the select result carries implicit taint but is
        // not itself a timing violation.
        Instruction::select(8, "merged", "secret_cond", "left_value",
                            "right_value"),
        Instruction::declassify(9, "left_value"),
    };

    function.blocks = {entry, left, right, join};
    Report report = Analyzer(std::move(function)).run();
    check(report.errors.empty(), "implicit case: valid IR");

    check(findViolation(report, 1, ViolationKind::BranchOnSecret) != nullptr,
          "secret BR is reported");
    check(findViolation(report, 9,
                        ViolationKind::UnmaskedDeclassify) != nullptr,
          "implicitly tainted assignment cannot be declassified early");

    const auto left_it = report.values.find("left_value");
    check(left_it != report.values.end() &&
              left_it->second.label == Label::Secret,
          "assignment under secret branch gets PC taint");
    check(left_it != report.values.end() &&
              !left_it->second.safeToDeclassify,
          "PC-tainted value is not release-safe");

    // After the post-dominator join, a fresh public value defined in the join
    // block must not remain implicitly tainted (the join block is not control
    // dependent on the secret branch).
    const auto fresh_it = report.values.find("fresh_public");
    check(fresh_it == report.values.end() ||
              fresh_it->second.label == Label::Public,
          "post-dominator join clears implicit PC taint");
}

// Secret DIV and REM operands trigger variable-latency instruction sinks.
void testSecretDivision() {
    Function function;
    function.name = "division";
    function.params = {
        Parameter{"numerator", Label::Secret},
        Parameter{"divisor", Label::Public},
    };

    Block entry;
    entry.id = 0;
    entry.name = "entry";
    entry.instrs = {
        Instruction::binary(1, Op::Div, "quotient", "numerator", "divisor"),
        Instruction::binary(2, Op::Rem, "remainder", "numerator", "divisor"),
        Instruction::binary(3, Op::Add, "safe_sum", "divisor", "divisor"),
    };
    function.blocks.push_back(entry);

    Report report = Analyzer(std::move(function)).run();
    check(report.errors.empty(), "division case: valid IR");
    check(findViolation(report, 1, ViolationKind::SecretDivRem) != nullptr,
          "secret DIV is detected");
    check(findViolation(report, 2, ViolationKind::SecretDivRem) != nullptr,
          "secret REM is detected");
    check(report.violations.size() == 2U,
          "public-only arithmetic produces no false positive");
    const Violation* violation =
        findViolation(report, 1, ViolationKind::SecretDivRem);
    check(violation != nullptr &&
              violation->sources.front() == "numerator",
          "division violation names source secret");
}

// A secret-derived STORE address is just as observable through the cache as a
// secret-index load.
void testSecretStoreAddress() {
    Function function;
    function.name = "secret_store";
    function.params = {
        Parameter{"index", Label::Secret},
        Parameter{"buffer", Label::Public},
        Parameter{"payload", Label::Secret},
    };

    Block entry;
    entry.id = 0;
    entry.name = "entry";
    entry.instrs = {
        Instruction::binary(1, Op::Add, "address", "buffer", "index"),
        Instruction::store(2, "address", "payload"),
    };
    function.blocks.push_back(entry);

    Report report = Analyzer(std::move(function)).run();
    check(findViolation(report, 2,
                        ViolationKind::SecretAddressStore) != nullptr,
          "secret STORE address is detected");
}

// A raw key value crossing DECLASSIFY without masking/reduction is rejected.
void testPrematureDeclassify() {
    Function function;
    function.name = "premature_release";
    function.params = {Parameter{"key", Label::Secret}};

    Block entry;
    entry.id = 0;
    entry.name = "entry";
    entry.instrs = {
        Instruction::unary(1, Op::Copy, "key_copy", "key"),
        Instruction::declassify(2, "key_copy"),
    };
    function.blocks.push_back(entry);

    Report report = Analyzer(std::move(function)).run();
    check(findViolation(report, 2,
                        ViolationKind::UnmaskedDeclassify) != nullptr,
          "unmasked key DECLASSIFY is rejected");
    const DeclassEvent* event = findEvent(report, 2);
    check(event != nullptr && !event->approved,
          "declass event is marked rejected");
}

void testMalformedIrIsRejected() {
    Function function;
    function.name = "bad";
    function.params = {Parameter{"x", Label::Public}};
    Block entry;
    entry.id = 0;
    entry.instrs.push_back(
        Instruction::br(1, "missing", 0, 1));
    function.blocks.push_back(entry);

    Report report = Analyzer(std::move(function)).run();
    check(!report.errors.empty() && !report.ok,
          "undefined SSA operand is a hard validation error");
}

// Natural loop: public loop condition must stay clean; a secretly-tainted
// divide inside the loop must still be found after fixed-point propagation
// over the back edge.
void testLoopFixedPoint() {
    Function function;
    function.name = "loop";
    function.params = {
        Parameter{"secret_data", Label::Secret},
        Parameter{"limit", Label::Public},
    };

    Block header;
    header.id = 0;
    header.name = "header";
    header.instrs = {
        Instruction::constant(1, "zero", "0"),
        Instruction::binary(2, Op::CmpLt, "loop_cond", "zero", "limit"),
        Instruction::br(3, "loop_cond", 1, 2),
    };

    Block body;
    body.id = 1;
    body.name = "body";
    body.instrs = {
        Instruction::binary(4, Op::Div, "scaled", "secret_data", "limit"),
        Instruction::binary(5, Op::Add, "trace_value", "limit", "scaled"),
        Instruction::jmp(6, 0),
    };

    Block exit_block;
    exit_block.id = 2;
    exit_block.name = "exit";
    exit_block.instrs = {
        Instruction::declassify(7, "zero"),
    };

    function.blocks = {header, body, exit_block};
    Report report = Analyzer(std::move(function)).run();
    check(report.errors.empty(), "loop case: valid IR");
    check(findViolation(report, 3, ViolationKind::BranchOnSecret) == nullptr,
          "public loop branch is not a violation");
    check(findViolation(report, 4, ViolationKind::SecretDivRem) != nullptr,
          "secret DIV inside loop body is detected");
    const DeclassEvent* event = findEvent(report, 7);
    check(event != nullptr && event->approved,
          "public loop-invariant value is safe to release");
}

}  // namespace

int main() {
    testMaskedCompareAndSwap();
    testSboxLookup();
    testImplicitFlowLeak();
    testSecretDivision();
    testSecretStoreAddress();
    testPrematureDeclassify();
    testMalformedIrIsRejected();
    testLoopFixedPoint();

    if (g_failures == 0) {
        std::printf("All constant-time verifier tests passed.\n");
        return 0;
    }
    std::printf("%d test check(s) failed.\n", g_failures);
    return 1;
}
