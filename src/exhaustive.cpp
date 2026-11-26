#include "module_plan/exhaustive.hpp"

#include <utility>

namespace mplan {
namespace {

struct Graph {
    TestSignatureOracle oracle;
    Catalog catalog;
    Platform platform{"darwin", "arm64", {}};

    Graph() {
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

    PlanResult plan(const ModuleCoordinates& initial,
                    const ModuleCoordinates& target,
                    std::uint64_t budget,
                    std::vector<OpenHandle> handles = {}) const {
        PlanRequest request;
        request.platform = platform;
        request.initialRoot = initial;
        request.targetRoot = target;
        request.budget.peakResidentBytes = budget;
        request.verifier = &oracle;
        request.handles = std::move(handles);
        return planMigration(catalog, request);
    }
};

ModuleManifest manifest(ModuleCoordinates coordinates,
                        std::uint64_t residentBytes) {
    ModuleManifest result;
    result.coordinates = std::move(coordinates);
    result.publisher = "p.test";
    result.keyGeneration = 1;
    result.abiDigest = "abi:" + result.coordinates.canonical();
    result.residentBytes = residentBytes;
    return result;
}

Dependency dependency(const ModuleCoordinates& coordinates) {
    Dependency result;
    result.module = coordinates.id;
    result.version = coordinates.version;
    return result;
}

ExportedSymbol narrowSymbol(std::string name, std::string abiDigest,
                            std::string contract) {
    ExportedSymbol symbol;
    symbol.name = std::move(name);
    symbol.version = "1";
    symbol.abiDigest = std::move(abiDigest);
    symbol.narrowContract = std::move(contract);
    symbol.kind = InterfaceKind::Narrow;
    return symbol;
}

SymbolRequirement requireSymbol(const ExportedSymbol& symbol) {
    return SymbolRequirement{symbol.name, symbol.version,
                             symbol.narrowContract};
}

MatrixCase evaluate(std::string id, std::string title,
                    StatusCode expected, const Graph& graph,
                    const ModuleCoordinates& initial,
                    const ModuleCoordinates& target,
                    std::uint64_t budget,
                    std::vector<OpenHandle> handles = {}) {
    MatrixCase result;
    result.id = std::move(id);
    result.title = std::move(title);
    result.expected = expected;
    result.budgetBytes = budget;

    std::vector<OpenHandle> reviewHandles = handles;
    PlanResult planned = graph.plan(initial, target, budget,
                                    std::move(handles));
    result.actual = planned.code;
    result.matched = planned.code == expected;
    result.peakBytes = planned.plan.peakBytes;
    result.detail = planned.message;

    if (planned.ok()) {
        PlanRequest request;
        request.platform = graph.platform;
        request.initialRoot = initial;
        request.targetRoot = target;
        request.budget.peakResidentBytes = budget;
        request.verifier = &graph.oracle;
        request.handles = std::move(reviewHandles);
        ReviewReport review = reviewPlan(graph.catalog, request,
                                         planned.plan);
        result.reviewOk = review.ok;
        if (!review.ok) {
            result.matched = false;
            result.detail = review.violations.front().message;
        }
    }
    return result;
}

OpenHandle singleHandle(const ModuleCoordinates& snapshot) {
    OpenHandle handle;
    handle.handleId = "handle:" + snapshot.canonical();
    handle.snapshotRoot = snapshot;
    return handle;
}

MatrixCase bridgeCase(const std::string& id, StatusCode expected,
                      PlatformCondition condition,
                      Permissions bridgePermissions,
                      Permissions importerPermissions,
                      bool broadTarget = false) {
    Graph graph;
    auto appCoordinate = moduleAt("spacea", "app", 1);
    auto codecCoordinate = moduleAt("spaceb", "codec", 1);

    ExportedSymbol encode = narrowSymbol(
        "encode", "abi-encode-v1", "contract.codec.encode.v1");
    encode.requiredPermissions = {"codec.invoke"};
    if (broadTarget) {
        encode.kind = InterfaceKind::BroadAbi;
        encode.narrowContract.clear();
    }

    ModuleManifest codec = manifest(codecCoordinate, 20);
    codec.permissions = {"codec.invoke"};
    codec.exports = {encode};
    graph.add(codec);

    Dependency onCodec = dependency(codecCoordinate);
    onCodec.importedSymbols = {requireSymbol(encode)};

    ModuleManifest app = manifest(appCoordinate, 10);
    app.permissions = std::move(importerPermissions);
    app.dependencies = {onCodec};
    graph.add(app);

    if (expected != StatusCode::BridgeMissing) {
        Bridge bridge;
        bridge.bridgeId = "bridge.spacea.spaceb.codec";
        bridge.fromNamespace = "spacea";
        bridge.toNamespace = "spaceb";
        bridge.publisher = "p.test";
        bridge.keyGeneration = 1;
        bridge.condition = std::move(condition);
        bridge.grantedPermissions = std::move(bridgePermissions);
        bridge.bindings.push_back(BridgeBinding{
            "encode", "encode", "contract.codec.encode.v1", 4,
            {"codec.invoke"}});
        graph.add(bridge);
    }

    return evaluate(id, "cross-space signed bridge matrix", expected, graph,
                    appCoordinate, appCoordinate, 1024,
                    {singleHandle(appCoordinate)});
}

MatrixCase diamondCase(bool divergent) {
    Graph graph;
    auto bottom1 = moduleAt("dia", "bottom", 1);
    auto bottom2 = moduleAt("dia", "bottom", 2);
    auto left = moduleAt("dia", "left", 1);
    auto right = moduleAt("dia", "right", 1);
    auto hub = moduleAt("dia", "hub", 1);

    const std::string rightAbi = divergent ? "abi-calc-v2" : "abi-calc-v1";

    ModuleManifest bottomOne = manifest(bottom1, 4);
    bottomOne.exports = {narrowSymbol("calc", "abi-calc-v1", "calc-narrow")};
    graph.add(bottomOne);

    ModuleManifest bottomTwo = manifest(bottom2, 5);
    bottomTwo.exports = {narrowSymbol("calc", rightAbi, "calc-narrow")};
    graph.add(bottomTwo);

    ModuleManifest leftModule = manifest(left, 6);
    leftModule.dependencies = {dependency(bottom1)};
    ExportedSymbol leftCalc = narrowSymbol("calc", "", "");
    leftCalc.reexports = {{bottom1.id, "calc"}};
    leftModule.exports = {leftCalc};
    graph.add(leftModule);

    ModuleManifest rightModule = manifest(right, 7);
    rightModule.dependencies = {dependency(bottom2)};
    ExportedSymbol rightCalc = narrowSymbol("calc", "", "");
    rightCalc.reexports = {{bottom2.id, "calc"}};
    rightModule.exports = {rightCalc};
    graph.add(rightModule);

    ModuleManifest hubModule = manifest(hub, 8);
    hubModule.dependencies = {dependency(left), dependency(right)};
    ExportedSymbol hubCalc = narrowSymbol("calc", "", "");
    hubCalc.reexports = {{left.id, "calc"}, {right.id, "calc"}};
    hubModule.exports = {hubCalc};
    graph.add(hubModule);

    const StatusCode expected = divergent
        ? StatusCode::DiamondAbiDivergence
        : StatusCode::Ok;
    return evaluate(divergent ? "diamond_abi_divergence"
                              : "diamond_abi_converged_control",
                    "transitive re-export diamond", expected, graph, hub, hub,
                    1024, {singleHandle(hub)});
}

MatrixCase pinningCase(bool exceeded) {
    Graph graph;
    auto oldApp = moduleAt("old", "app", 1);
    auto oldCore = moduleAt("old", "core", 1);
    auto hookTarget = moduleAt("hookspace", "bridge_target", 1);
    auto newApp = moduleAt("new", "app", 1);

    ModuleManifest core = manifest(oldCore, 20);
    graph.add(core);

    ModuleManifest oldApplication = manifest(oldApp, 40);
    oldApplication.dependencies = {dependency(oldCore)};
    graph.add(oldApplication);

    graph.add(manifest(hookTarget, 40));
    graph.add(manifest(newApp, 10));

    OpenHandle handle = singleHandle(oldApp);
    DestructorHook hook;
    hook.hookId = "destructor-bridge-hook";
    hook.bridgeTargets = {hookTarget};
    handle.hooks = {hook};

    const std::uint64_t budget = exceeded ? 80 : 128;
    const StatusCode expected = exceeded
        ? StatusCode::DestructorPinningExceeded
        : StatusCode::Ok;
    return evaluate(exceeded ? "destructor_pinning_exceeded"
                             : "destructor_pinning_control",
                    "two-phase destructor pinning", expected, graph, oldApp,
                    newApp, budget, {handle});
}

MatrixCase rollbackCase(bool insufficient) {
    Graph graph;
    auto oldRoot = moduleAt("rb", "old_snapshot", 1);
    auto base = moduleAt("rb", "new_base", 1);
    auto middle = moduleAt("rb", "new_middle", 1);
    auto app = moduleAt("rb", "new_app", 1);

    graph.add(manifest(oldRoot, 50));
    graph.add(manifest(base, 20));

    ModuleManifest middleManifest = manifest(middle, 20);
    middleManifest.dependencies = {dependency(base)};
    middleManifest.transitionShimBytes = 15;
    graph.add(middleManifest);

    ModuleManifest appManifest = manifest(app, 20);
    appManifest.dependencies = {dependency(middle)};
    appManifest.transitionShimBytes = 10;
    graph.add(appManifest);

    const std::uint64_t budget = insufficient ? 100 : 160;
    const StatusCode expected = insufficient
        ? StatusCode::RollbackBudgetExceeded
        : StatusCode::Ok;
    return evaluate(insufficient ? "rollback_budget_insufficient"
                                 : "rollback_budget_control",
                    "per-step rollback residency", expected, graph, oldRoot,
                    app, budget, {singleHandle(oldRoot)});
}

MatrixCase cycleCase(bool outOfGeneration) {
    Graph graph;
    graph.catalog.keys["p.test"].acceptedGenerations = {1};

    auto a = moduleAt("cyc", "a", 1);
    auto b = moduleAt("cyc", "b", 1);

    ModuleManifest manifestA = manifest(a, 1);
    manifestA.dependencies = {dependency(b)};
    graph.add(manifestA);

    ModuleManifest manifestB = manifest(b, 2);
    manifestB.keyGeneration = outOfGeneration ? 2 : 1;
    manifestB.dependencies = {dependency(a)};
    graph.add(manifestB);

    const StatusCode expected = outOfGeneration
        ? StatusCode::OutOfGenerationCycle
        : StatusCode::DependencyCycle;
    return evaluate(outOfGeneration ? "out_of_generation_cycle"
                                    : "same_generation_cycle_control",
                    "dependency cycle classification", expected, graph, a, a,
                    1024);
}

MatrixCase revokedRetentionCase() {
    Graph graph;
    graph.catalog.keys["p.test"].acceptedGenerations = {1, 2};
    graph.catalog.keys["p.test"].revokedGenerations = {2};

    auto oldRoot = moduleAt("rev", "old", 1);
    auto newRoot = moduleAt("rev", "new", 1);

    ModuleManifest oldManifest = manifest(oldRoot, 10);
    oldManifest.keyGeneration = 2;
    graph.add(oldManifest);
    graph.add(manifest(newRoot, 10));

    MatrixCase result = evaluate(
        "revoked_generation_retained_without_reload",
        "rollback uses pinned memory instead of revoked key generation",
        StatusCode::Ok, graph, oldRoot, newRoot, 128,
        {singleHandle(oldRoot)});
    return result;
}

MatrixCase platformDependencyCase(bool excluded) {
    Graph graph;
    graph.platform = excluded ? Platform{"darwin", "arm64", {}}
                              : Platform{"linux", "arm64", {}};

    auto root = moduleAt("plat", "app", 1);
    ModuleManifest application = manifest(root, 10);

    Dependency linuxOnly;
    linuxOnly.module = ModuleId{"plat", "linux_only"};
    linuxOnly.version = Version{1, 0, 0};
    linuxOnly.when = PlatformCondition::onOs("linux");
    application.dependencies = {linuxOnly};
    graph.add(application);

    return evaluate(excluded ? "platform_dependency_excluded"
                             : "platform_dependency_enabled_missing",
                    "conditional direct dependency",
                    excluded ? StatusCode::Ok : StatusCode::UnknownModule,
                    graph, root, root, 128, {singleHandle(root)});
}

} // namespace

std::vector<MatrixCase> runExhaustiveMatrix() {
    std::vector<MatrixCase> cases;

    cases.push_back(bridgeCase(
        "bridge_missing", StatusCode::BridgeMissing,
        PlatformCondition::always(), {}, {}));
    cases.push_back(bridgeCase(
        "bridge_platform_excluded", StatusCode::BridgePlatformExcluded,
        PlatformCondition::onOs("linux"), {"codec.invoke"},
        {"codec.invoke"}));
    cases.push_back(bridgeCase(
        "bridge_valid_control", StatusCode::Ok,
        PlatformCondition::onOs("darwin"), {"codec.invoke"},
        {"codec.invoke"}));
    cases.push_back(bridgeCase(
        "permission_widening", StatusCode::PermissionWidening,
        PlatformCondition::onOs("darwin"),
        {"codec.invoke", "security.admin"}, {"codec.invoke"}));
    cases.push_back(bridgeCase(
        "broad_abi_substitution", StatusCode::BroadAbiSubstitution,
        PlatformCondition::onOs("darwin"), {"codec.invoke"},
        {"codec.invoke"}, true));

    cases.push_back(diamondCase(true));
    cases.push_back(diamondCase(false));
    cases.push_back(pinningCase(true));
    cases.push_back(pinningCase(false));
    cases.push_back(rollbackCase(true));
    cases.push_back(rollbackCase(false));
    cases.push_back(cycleCase(true));
    cases.push_back(cycleCase(false));
    cases.push_back(revokedRetentionCase());
    cases.push_back(platformDependencyCase(true));
    cases.push_back(platformDependencyCase(false));

    return cases;
}

} // namespace mplan
