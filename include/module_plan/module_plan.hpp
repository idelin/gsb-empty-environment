#pragma once

#include <compare>
#include <cstdint>
#include <limits>
#include <map>
#include <optional>
#include <set>
#include <string>
#include <string_view>
#include <utility>
#include <vector>

namespace mplan {

inline constexpr std::uint32_t kSchemaVersion = 1;

constexpr std::uint64_t saturatedPlus(std::uint64_t left,
                                      std::uint64_t right) {
    if (right > std::numeric_limits<std::uint64_t>::max() - left) {
        return std::numeric_limits<std::uint64_t>::max();
    }
    return left + right;
}

enum class StatusCode {
    Ok = 0,
    InvalidRequest,
    UnknownModule,
    InvalidModuleSignature,
    UnknownPublisher,
    UntrustedKeyGeneration,
    RevokedKeyGeneration,
    DependencyMissing,
    DependencyCycle,
    OutOfGenerationCycle,
    BridgeMissing,
    BridgeSignatureInvalid,
    BridgePlatformExcluded,
    BridgeBindingMissing,
    NarrowInterfaceRequired,
    BroadAbiSubstitution,
    NarrowContractMismatch,
    AbiMismatch,
    SymbolMissing,
    SymbolVersionMismatch,
    DiamondAbiDivergence,
    PermissionWidening,
    PermissionDenied,
    DestructorHookTargetMissing,
    DestructorPinningExceeded,
    RollbackBudgetExceeded,
    RevokedGenerationReload,
    InvalidWitness,
    ReviewViolation
};

const char* toString(StatusCode code) noexcept;

// Compatibility spellings for the domain language used in the specification.
inline constexpr StatusCode BridgeExcludedByPlatform =
    StatusCode::BridgePlatformExcluded;
inline constexpr StatusCode BridgeExcluded =
    StatusCode::BridgePlatformExcluded;
inline constexpr StatusCode BridgeDisabledByPlatform =
    StatusCode::BridgePlatformExcluded;
inline constexpr StatusCode BridgeNotFound = StatusCode::BridgeMissing;
inline constexpr StatusCode PermissionRelaxed =
    StatusCode::PermissionWidening;
inline constexpr StatusCode PrivilegeWidening =
    StatusCode::PermissionWidening;
inline constexpr StatusCode PermissionEscalation =
    StatusCode::PermissionWidening;
inline constexpr StatusCode PrivilegeEscalation =
    StatusCode::PermissionWidening;
inline constexpr StatusCode GenerationCycle =
    StatusCode::OutOfGenerationCycle;
inline constexpr StatusCode GenerationOutOfRangeCycle =
    StatusCode::OutOfGenerationCycle;
inline constexpr StatusCode GenerationOutsideCycle =
    StatusCode::OutOfGenerationCycle;
inline constexpr StatusCode OutOfKeyGenerationCycle =
    StatusCode::OutOfGenerationCycle;
inline constexpr StatusCode GenerationOutOfBoundsCycle =
    StatusCode::OutOfGenerationCycle;
inline constexpr StatusCode OutOfGenerationLoop =
    StatusCode::OutOfGenerationCycle;
inline constexpr StatusCode PinningBudgetExceeded =
    StatusCode::DestructorPinningExceeded;
inline constexpr StatusCode DestructorPinningOverLimit =
    StatusCode::DestructorPinningExceeded;
inline constexpr StatusCode PinningLimitExceeded =
    StatusCode::DestructorPinningExceeded;
inline constexpr StatusCode RollbackBudgetInsufficient =
    StatusCode::RollbackBudgetExceeded;
inline constexpr StatusCode WideAbiSubstitution =
    StatusCode::BroadAbiSubstitution;

struct Version {
    std::uint32_t major = 1;
    std::uint32_t minor = 0;
    std::uint32_t patch = 0;

    auto operator<=>(const Version&) const = default;
    std::string str() const;
};

struct ModuleId {
    std::string ns = "default";
    std::string name;

    auto operator<=>(const ModuleId&) const = default;
    std::string canonical() const;
};

struct ModuleCoordinates {
    ModuleId id;
    Version version{};

    auto operator<=>(const ModuleCoordinates&) const = default;
    std::string canonical() const;
};

ModuleId moduleId(std::string ns, std::string name);
ModuleCoordinates moduleAt(std::string ns, std::string name,
                           std::uint32_t major = 1,
                           std::uint32_t minor = 0,
                           std::uint32_t patch = 0);

struct Platform {
    std::string os;
    std::string arch;
    std::set<std::string> features;
};

struct PlatformCondition {
    std::optional<std::string> os;
    std::optional<std::string> arch;
    std::set<std::string> allFeatures;
    std::set<std::string> absentFeatures;

    static PlatformCondition always();
    static PlatformCondition onOs(std::string osName);
    static PlatformCondition onArch(std::string archName);

    PlatformCondition& requireFeature(std::string feature);
    PlatformCondition& excludeFeature(std::string feature);
    PlatformCondition& withOs(std::string osName);
    PlatformCondition& withArch(std::string archName);

    bool matches(const Platform& platform) const;
    std::string canonical() const;
};

using Permissions = std::set<std::string>;

bool isSubset(const Permissions& subset, const Permissions& superset);
Permissions permissionDifference(const Permissions& left,
                                 const Permissions& right);

std::string fingerprint64(std::string_view text);
std::string fingerprintLines(const std::vector<std::string>& lines);

struct Signature {
    std::string algorithm = "test-oracle-fnv1a-v1";
    std::string signer;
    std::uint32_t keyGeneration = 1;
    std::string value;
};

class ISignatureVerifier {
public:
    virtual ~ISignatureVerifier() = default;
    virtual bool verifyModule(const struct SignedModule& module) const = 0;
    virtual bool verifyBridge(const struct Bridge& bridge) const = 0;
};

class TestSignatureOracle final : public ISignatureVerifier {
public:
    Signature sign(const struct ModuleManifest& manifest) const;
    Signature sign(const struct Bridge& bridge) const;
    bool verifyModule(const SignedModule& module) const override;
    bool verifyBridge(const Bridge& bridge) const override;
};

struct PublisherTrust {
    std::set<std::uint32_t> acceptedGenerations{1};
    std::set<std::uint32_t> revokedGenerations;
};

struct KeyRing {
    std::map<std::string, PublisherTrust> publishers;

    const PublisherTrust* find(std::string_view publisher) const;
    PublisherTrust& operator[](std::string publisher);
};

enum class GenerationTrust {
    Accepted,
    Revoked,
    OutsideWindow,
    UnknownPublisher
};

GenerationTrust generationTrust(const KeyRing& keyRing,
                                std::string_view publisher,
                                std::uint32_t generation);

struct ReexportEdge {
    ModuleId dependency;
    std::string sourceSymbol;
};

enum class InterfaceKind {
    Narrow,
    BroadAbi
};

struct ExportedSymbol {
    std::string name;
    std::string version = "1";
    std::string abiDigest;
    std::string narrowContract;
    InterfaceKind kind = InterfaceKind::Narrow;
    Permissions requiredPermissions;
    std::vector<ReexportEdge> reexports;
    std::uint64_t shimBytes = 0;
};

struct SymbolRequirement {
    std::string name;
    std::string version;
    std::string narrowContract;
};

struct Dependency {
    ModuleId module;
    Version version{};
    PlatformCondition when = PlatformCondition::always();
    bool required = true;
    std::optional<std::string> bridgeId;
    std::vector<SymbolRequirement> importedSymbols;
};

struct ModuleManifest {
    ModuleCoordinates coordinates;
    std::string publisher;
    std::uint32_t keyGeneration = 1;
    std::string abiDigest;
    std::uint64_t residentBytes = 0;
    std::uint64_t transitionShimBytes = 0;
    Permissions permissions;
    std::vector<Dependency> dependencies;
    std::vector<ExportedSymbol> exports;
};

struct SignedModule {
    ModuleManifest manifest;
    Signature signature;
};

struct BridgeBinding {
    std::string importedSymbol;
    std::string targetSymbol;
    std::string narrowContract;
    std::uint64_t shimBytes = 0;
    Permissions requiredPermissions;
};

struct Bridge {
    std::string bridgeId;
    std::string fromNamespace;
    std::string toNamespace;
    std::string publisher;
    std::uint32_t keyGeneration = 1;
    PlatformCondition condition = PlatformCondition::always();
    std::vector<BridgeBinding> bindings;
    Permissions grantedPermissions;
    bool allowsBroadAbi = false;
    Signature signature;

    Bridge& when(PlatformCondition platformCondition) & {
        condition = std::move(platformCondition);
        return *this;
    }

    Bridge&& when(PlatformCondition platformCondition) && {
        condition = std::move(platformCondition);
        return std::move(*this);
    }

    std::uint64_t bindingShimBytes() const;
};

class Catalog {
public:
    KeyRing keys;

    void addModule(SignedModule module);
    void addBridge(Bridge bridge);

    const SignedModule* module(const ModuleCoordinates& coordinates) const;
    const SignedModule* moduleById(const ModuleId& id,
                                   const Version& version) const;
    const Bridge* bridgeById(std::string_view bridgeId) const;
    std::vector<const Bridge*> bridgesBetween(
        std::string_view fromNamespace,
        std::string_view toNamespace) const;

    const std::map<ModuleCoordinates, SignedModule>& modules() const;
    const std::vector<Bridge>& bridges() const;

private:
    std::map<ModuleCoordinates, SignedModule> modules_;
    std::vector<Bridge> bridges_;
};

enum class WitnessKind {
    ModuleSignature,
    BridgeSignature,
    PlatformCondition,
    KeyGeneration,
    AbiDigest,
    SymbolVersion,
    NarrowInterface,
    DiamondPath,
    PermissionBoundary,
    MemoryResidency,
    Rollback,
    Pinning,
    Binding
};

const char* toString(WitnessKind kind) noexcept;

struct BindingWitness {
    std::string witnessId;
    WitnessKind kind = WitnessKind::Binding;
    std::string subject;
    std::string publisher;
    std::uint32_t keyGeneration = 0;
    std::string abiDigest;
    std::string bindingDigest;
    bool satisfied = true;
    StatusCode code = StatusCode::Ok;
    std::vector<std::string> details;
    std::string fingerprint;
};

BindingWitness makeModuleWitness(const SignedModule& module,
                                 const KeyRing* keyRing,
                                 const ISignatureVerifier* verifier);
BindingWitness makeBridgeWitness(const Bridge& bridge,
                                 const Platform& platform,
                                 const KeyRing* keyRing,
                                 const ISignatureVerifier* verifier);
std::string witnessFingerprint(const BindingWitness& witness);
bool verifyWitnessFingerprint(const BindingWitness& witness);

struct Closure {
    StatusCode code = StatusCode::Ok;
    std::string message;
    std::vector<ModuleCoordinates> order;
    std::set<ModuleCoordinates> members;
    std::uint64_t residentBytes = 0;
    std::vector<BindingWitness> witnesses;
};

struct ClosureOptions {
    const ISignatureVerifier* verifier = nullptr;
    bool enforceCurrentTrust = true;
    bool initialPinnedSnapshot = false;
};

Closure computeClosure(const Catalog& catalog,
                       const Platform& platform,
                       const ModuleCoordinates& root,
                       const ClosureOptions& options = {});

struct AbiTerminal {
    ModuleCoordinates definingModule;
    std::string symbol;
    std::string version;
    std::string abiDigest;
    std::vector<ModuleCoordinates> path;
};

struct DiamondReport {
    StatusCode code = StatusCode::Ok;
    std::string message;
    std::string subject;
    std::vector<AbiTerminal> terminals;
    std::vector<BindingWitness> witnesses;
};

DiamondReport checkReexportDiamonds(const Catalog& catalog,
                                    const Platform& platform,
                                    const ModuleCoordinates& root);

struct DestructorHook {
    std::string hookId;
    std::vector<ModuleCoordinates> bridgeTargets;
    std::vector<std::string> bridgeIds;
    std::uint64_t scratchBytes = 0;
};

struct OpenHandle {
    std::string handleId;
    ModuleCoordinates snapshotRoot;
    std::vector<DestructorHook> hooks;
};

enum class PinPhase {
    Open,
    DestructionArmed,
    DestructorCompleted
};

struct PinLease {
    std::string handleId;
    ModuleCoordinates snapshotRoot;
    PinPhase phase = PinPhase::Open;
    std::set<ModuleCoordinates> pinnedModules;
    std::set<std::string> pinnedBridgeIds;
    std::uint64_t scratchBytes = 0;
    std::uint64_t pinnedBytes = 0;
    BindingWitness witness;
};

class HandlePinRegistry {
public:
    HandlePinRegistry(const Catalog& catalog, const Platform& platform,
                      const ISignatureVerifier* verifier);

    StatusCode pin(const OpenHandle& handle);
    StatusCode beginDestruction(std::string_view handleId);
    StatusCode completeDestruction(std::string_view handleId);

    const PinLease* lease(std::string_view handleId) const;
    const std::map<std::string, PinLease>& leases() const;
    std::vector<const PinLease*> activeLeases() const;
    std::uint64_t activePinnedBytesUnion() const;

private:
    const Catalog& catalog_;
    const Platform& platform_;
    const ISignatureVerifier* verifier_;
    std::map<std::string, PinLease> leases_;
};

struct Budget {
    std::uint64_t peakResidentBytes = 0;
};

struct PlanRequest {
    Platform platform;
    ModuleCoordinates initialRoot;
    ModuleCoordinates targetRoot;
    std::vector<OpenHandle> handles;
    Budget budget;
    const ISignatureVerifier* verifier = nullptr;
};

struct RollbackAction {
    std::string kind;
    ModuleCoordinates module{};
    std::string subject;
    std::uint64_t bytes = 0;
    bool reloadsRevokedGeneration = false;
};

struct RollbackWitness {
    ModuleCoordinates initialRoot;
    bool initialClosureRetained = true;
    std::uint64_t retainedBytes = 0;
    std::uint64_t reloadBytes = 0;
    std::uint64_t peakRollbackBytes = 0;
    std::vector<ModuleCoordinates> retainedModules;
    std::vector<ModuleCoordinates> reloadRequired;
    std::vector<std::string> retainedRevokedGenerations;
    std::vector<RollbackAction> actions;
    std::vector<std::string> forbiddenRevokedReloads;
    std::string fingerprint;

    bool safeWithinBudget(std::uint64_t budgetBytes) const;
};

struct ResidencyRegion {
    std::string name;
    std::string kind;
    std::uint64_t bytes = 0;
    std::vector<ModuleCoordinates> modules;
    std::vector<std::string> bridgeIds;
    std::vector<BindingWitness> witnesses;
    std::string fingerprint;
};

struct ResidencyStep {
    std::size_t index = 0;
    std::string phase;
    std::string action;
    ModuleCoordinates module{};
    std::vector<ResidencyRegion> regions;
    std::uint64_t oldPinnedBytes = 0;
    std::uint64_t newClosureBytes = 0;
    std::uint64_t shimBytes = 0;
    std::uint64_t forwardPeakBytes = 0;
    std::uint64_t rollbackPeakBytes = 0;
    std::uint64_t budgetBytes = 0;
    bool rollbackInvariantSatisfied = false;
    RollbackWitness rollback;
    std::vector<BindingWitness> bindings;
    std::string fingerprint;
};

struct MigrationPlan {
    ModuleCoordinates initialRoot;
    ModuleCoordinates targetRoot;
    Platform platform;
    std::uint64_t peakBytes = 0;
    std::uint64_t budgetBytes = 0;
    std::vector<ResidencyStep> steps;
    std::vector<PinLease> pinLeases;
    std::vector<BindingWitness> witnesses;
    std::string planFingerprint;
};

struct PlanResult {
    StatusCode code = StatusCode::Ok;
    std::string message;
    MigrationPlan plan;

    bool ok() const noexcept {
        return code == StatusCode::Ok;
    }

    explicit operator bool() const noexcept {
        return ok();
    }
};

PlanResult planMigration(const Catalog& catalog, const PlanRequest& request);

struct ReviewViolation {
    StatusCode code = StatusCode::ReviewViolation;
    std::string subject;
    std::string message;
};

struct ReviewReport {
    bool ok = true;
    std::size_t checkedWitnesses = 0;
    std::uint64_t observedPeakBytes = 0;
    std::vector<ReviewViolation> violations;
};

ReviewReport reviewPlan(const Catalog& catalog,
                        const PlanRequest& request,
                        const MigrationPlan& plan);

class ReadOnlyReviewer {
public:
    ReadOnlyReviewer(const Catalog& catalog, const PlanRequest& request,
                     const MigrationPlan& plan)
        : catalog_(catalog), request_(request), plan_(plan) {}

    ReviewReport verify() const {
        return reviewPlan(catalog_, request_, plan_);
    }

private:
    const Catalog& catalog_;
    const PlanRequest& request_;
    const MigrationPlan& plan_;
};

class ModuleLoadPlanner {
public:
    explicit ModuleLoadPlanner(Catalog catalog)
        : catalog_(std::move(catalog)) {}

    PlanResult plan(const PlanRequest& request) const {
        return planMigration(catalog_, request);
    }

    ReviewReport review(const PlanRequest& request,
                        const MigrationPlan& plan) const {
        return reviewPlan(catalog_, request, plan);
    }

    const Catalog& catalog() const {
        return catalog_;
    }

private:
    Catalog catalog_;
};

using LoadingPlanner = ModuleLoadPlanner;
using Planner = ModuleLoadPlanner;

} // namespace mplan

namespace module_plan = mplan;
