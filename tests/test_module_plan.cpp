#include "module_plan/exhaustive.hpp"
#include "module_plan/module_plan.hpp"

#include <iostream>
#include <string>

using namespace mplan;

namespace {

int g_failures = 0;
int g_checks = 0;

void check(bool condition, const std::string& expression,
           const char* file, int line) {
    ++g_checks;
    if (!condition) {
        ++g_failures;
        std::cerr << file << ':' << line << ": CHECK failed: "
                  << expression << '\n';
    }
}

template <typename Lhs, typename Rhs>
void checkEq(const Lhs& lhs, const Rhs& rhs, const std::string& lhsText,
             const std::string& rhsText, const char* file, int line) {
    ++g_checks;
    if (!(lhs == static_cast<Lhs>(rhs))) {
        ++g_failures;
        std::cerr << file << ':' << line << ": CHECK_EQ failed: " << lhsText
                  << " == " << rhsText << '\n';
    }
}

#define CHECK(condition) check((condition), #condition, __FILE__, __LINE__)
#define CHECK_EQ(lhs, rhs)                                                     \
    checkEq((lhs), (rhs), #lhs, #rhs, __FILE__, __LINE__)
#define CHECK_LE(lhs, rhs) CHECK((lhs) <= (rhs))

struct Fixture {
    TestSignatureOracle oracle;
    Catalog catalog;
    Platform platform{"darwin", "arm64", {}};

    Fixture() {
        catalog.keys["p.test"].acceptedGenerations = {1, 2};
    }

    void add(ModuleManifest manifest) {
        SignedModule module;
        module.manifest = std::move(manifest);
        module.signature = oracle.sign(module.manifest);
        catalog.addModule(std::move(module));
    }

    void add(Bridge bridge) {
        bridge.signature = oracle.sign(bridge);
        catalog.addBridge(std::move(bridge));
    }

    ModuleManifest makeModule(ModuleCoordinates coordinates,
                              std::uint64_t bytes) {
        ModuleManifest manifest;
        manifest.coordinates = coordinates;
        manifest.publisher = "p.test";
        manifest.keyGeneration = 1;
        manifest.abiDigest = "abi:" + coordinates.canonical();
        manifest.residentBytes = bytes;
        return manifest;
    }

    PlanResult plan(const ModuleCoordinates& initial,
                    const ModuleCoordinates& target,
                    std::uint64_t budget,
                    std::vector<OpenHandle> handles = {}) {
        PlanRequest request;
        request.platform = platform;
        request.initialRoot = initial;
        request.targetRoot = target;
        request.budget.peakResidentBytes = budget;
        request.verifier = &oracle;
        request.handles = std::move(handles);
        return planMigration(catalog, request);
    }

    ReviewReport review(const PlanRequest& request,
                        const MigrationPlan& plan) {
        return reviewPlan(catalog, request, plan);
    }
};

Dependency dep(const ModuleCoordinates& coordinates) {
    Dependency dependency;
    dependency.module = coordinates.id;
    dependency.version = coordinates.version;
    return dependency;
}

OpenHandle makeHandle(const ModuleCoordinates& coordinate) {
    OpenHandle handle;
    handle.handleId = "handle:" + coordinate.canonical();
    handle.snapshotRoot = coordinate;
    return handle;
}

void test_exhaustive_matrix() {
    const std::vector<MatrixCase> cases = runExhaustiveMatrix();
    CHECK(cases.size() >= 14);
    for (const MatrixCase& testCase : cases) {
        std::cout << "matrix " << testCase.id << ": expected="
                  << toString(testCase.expected)
                  << " actual=" << toString(testCase.actual) << '\n';
        CHECK(testCase.matched);
        if (!testCase.matched) {
            std::cerr << "  detail: " << testCase.detail << '\n';
        }
        if (testCase.expected == StatusCode::Ok) {
            CHECK(testCase.reviewOk);
        }
    }
}

void test_stepwise_residency_and_reviewer() {
    Fixture fixture;
    auto oldRoot = moduleAt("step", "old", 1);
    auto base = moduleAt("step", "base", 1);
    auto middle = moduleAt("step", "middle", 1);
    auto app = moduleAt("step", "app", 1);

    fixture.add(fixture.makeModule(oldRoot, 50));
    fixture.add(fixture.makeModule(base, 20));

    ModuleManifest middleManifest = fixture.makeModule(middle, 20);
    middleManifest.dependencies = {dep(base)};
    middleManifest.transitionShimBytes = 15;
    fixture.add(middleManifest);

    ModuleManifest appManifest = fixture.makeModule(app, 20);
    appManifest.dependencies = {dep(middle)};
    appManifest.transitionShimBytes = 10;
    fixture.add(appManifest);

    OpenHandle handle;
    handle.handleId = "open-handle";
    handle.snapshotRoot = oldRoot;

    PlanResult result = fixture.plan(oldRoot, app, 160, {handle});
    CHECK(result.ok());
    CHECK_EQ(result.code, StatusCode::Ok);
    CHECK_EQ(result.plan.steps.size(), static_cast<std::size_t>(4));
    CHECK_EQ(result.plan.peakBytes, static_cast<std::uint64_t>(135));

    PlanRequest request;
    request.platform = fixture.platform;
    request.initialRoot = oldRoot;
    request.targetRoot = app;
    request.budget.peakResidentBytes = 160;
    request.verifier = &fixture.oracle;
    request.handles = {handle};

    for (const ResidencyStep& step : result.plan.steps) {
        CHECK(step.rollbackInvariantSatisfied);
        CHECK_LE(step.forwardPeakBytes, request.budget.peakResidentBytes);
        CHECK_EQ(step.forwardPeakBytes, step.rollbackPeakBytes);
        CHECK(!step.fingerprint.empty());
    }

    ReviewReport report =
        ReadOnlyReviewer(fixture.catalog, request, result.plan).verify();
    CHECK(report.ok);
    CHECK(report.checkedWitnesses > 10);
    CHECK_EQ(report.observedPeakBytes, static_cast<std::uint64_t>(135));

    MigrationPlan tampered = result.plan;
    tampered.steps.back().oldPinnedBytes += 999;
    ReviewReport tamperedReport = fixture.review(request, tampered);
    CHECK(!tamperedReport.ok);
}

void test_intermediate_rollback_failure() {
    Fixture fixture;
    auto oldRoot = moduleAt("roll", "old", 1);
    auto base = moduleAt("roll", "base", 1);
    auto middle = moduleAt("roll", "middle", 1);

    fixture.add(fixture.makeModule(oldRoot, 50));
    fixture.add(fixture.makeModule(base, 20));
    ModuleManifest middleManifest = fixture.makeModule(middle, 20);
    middleManifest.dependencies = {dep(base)};
    middleManifest.transitionShimBytes = 15;
    fixture.add(middleManifest);

    PlanResult result = fixture.plan(oldRoot, middle, 100,
                                     {makeHandle(oldRoot)});
    CHECK_EQ(result.code, StatusCode::RollbackBudgetExceeded);
    CHECK_EQ(result.plan.steps.size(), static_cast<std::size_t>(3));
    CHECK(!result.plan.steps.back().rollbackInvariantSatisfied);
    CHECK_EQ(result.plan.steps.back().forwardPeakBytes,
             static_cast<std::uint64_t>(105));
}

void test_pinning_witness_bytes() {
    Fixture fixture;
    auto oldApp = moduleAt("pin", "app", 1);
    auto oldCore = moduleAt("pin", "core", 1);
    auto hookTarget = moduleAt("pinbridge", "target", 1);

    ModuleManifest app = fixture.makeModule(oldApp, 40);
    app.dependencies = {dep(oldCore)};
    fixture.add(app);
    fixture.add(fixture.makeModule(oldCore, 20));
    fixture.add(fixture.makeModule(hookTarget, 40));

    OpenHandle handle;
    handle.handleId = "destructor-handle";
    handle.snapshotRoot = oldApp;
    DestructorHook hook;
    hook.hookId = "hook";
    hook.bridgeTargets = {hookTarget};
    handle.hooks = {hook};

    PlanResult tooSmall = fixture.plan(oldApp, oldApp, 80, {handle});
    CHECK_EQ(tooSmall.code, StatusCode::DestructorPinningExceeded);
    CHECK_EQ(tooSmall.plan.steps.size(), static_cast<std::size_t>(1));
    CHECK_EQ(tooSmall.plan.steps[0].forwardPeakBytes,
             static_cast<std::uint64_t>(100));

    PlanResult enough = fixture.plan(oldApp, oldApp, 200, {handle});
    CHECK(enough.ok());
    CHECK_EQ(enough.plan.steps[0].forwardPeakBytes,
             static_cast<std::uint64_t>(100));
    CHECK_EQ(enough.plan.pinLeases.size(), static_cast<std::size_t>(1));
    CHECK(verifyWitnessFingerprint(enough.plan.pinLeases[0].witness));

    HandlePinRegistry pins(fixture.catalog, fixture.platform,
                           &fixture.oracle);
    CHECK_EQ(pins.pin(handle), StatusCode::Ok);
    CHECK_EQ(pins.activePinnedBytesUnion(),
             static_cast<std::uint64_t>(100));
    CHECK_EQ(pins.completeDestruction(handle.handleId),
             StatusCode::InvalidRequest);
    CHECK_EQ(pins.beginDestruction(handle.handleId), StatusCode::Ok);
    CHECK_EQ(pins.activePinnedBytesUnion(),
             static_cast<std::uint64_t>(100));
    CHECK_EQ(pins.completeDestruction(handle.handleId), StatusCode::Ok);
    CHECK_EQ(pins.activePinnedBytesUnion(), static_cast<std::uint64_t>(0));
}

void test_reviewer_detects_revoked_reload_action() {
    Fixture fixture;
    MigrationPlan plan;
    plan.budgetBytes = 10;
    plan.initialRoot = moduleAt("rev", "old", 1);
    plan.targetRoot = plan.initialRoot;

    ResidencyStep step;
    step.index = 0;
    step.oldPinnedBytes = 1;
    step.newClosureBytes = 0;
    step.shimBytes = 0;
    step.forwardPeakBytes = 1;
    step.rollbackPeakBytes = 1;
    step.budgetBytes = 10;
    step.rollbackInvariantSatisfied = true;
    step.rollback.initialRoot = plan.initialRoot;
    step.rollback.initialClosureRetained = false;
    step.rollback.peakRollbackBytes = 1;
    step.rollback.actions.push_back(RollbackAction{
        "illegal-reload", plan.initialRoot, "rev/old@1.0.0", 1, true});
    step.fingerprint = "not-recomputed";
    plan.steps = {step};

    PlanRequest request;
    request.budget.peakResidentBytes = 10;
    request.initialRoot = plan.initialRoot;
    request.targetRoot = plan.targetRoot;
    ReviewReport report = reviewPlan(fixture.catalog, request, plan);
    bool foundRevokedReload = false;
    for (const ReviewViolation& violation : report.violations) {
        if (violation.code == StatusCode::RevokedGenerationReload) {
            foundRevokedReload = true;
        }
    }
    CHECK(foundRevokedReload);
    CHECK(!report.ok);
}

void test_namespace_identity_and_bridge_when_compiles() {
    ModuleId a = moduleId("spacea", "codec");
    ModuleId b = moduleId("spaceb", "codec");
    CHECK(a != b);
    CHECK_EQ(a.name, b.name);

    Bridge bridge;
    bridge.bridgeId = "b";
    bridge.when(PlatformCondition::onOs("linux").withArch("arm64"));
    Platform linuxArm{"linux", "arm64", {}};
    Platform darwinArm{"darwin", "arm64", {}};
    CHECK(bridge.condition.matches(linuxArm));
    CHECK(!bridge.condition.matches(darwinArm));

    ModuleLoadPlanner planner(Catalog{});
    PlanRequest emptyRequest;
    CHECK_EQ(planner.plan(emptyRequest).code,
             StatusCode::InvalidRequest);
}

} // namespace

int main() {
    test_exhaustive_matrix();
    test_namespace_identity_and_bridge_when_compiles();
    test_stepwise_residency_and_reviewer();
    test_intermediate_rollback_failure();
    test_pinning_witness_bytes();
    test_reviewer_detects_revoked_reload_action();

    std::cout << "\nchecks=" << g_checks << " failures=" << g_failures
              << '\n';
    return g_failures == 0 ? 0 : 1;
}
