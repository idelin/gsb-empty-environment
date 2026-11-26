#include "module_plan/module_plan.hpp"

#include <algorithm>
#include <iomanip>
#include <numeric>
#include <sstream>
#include <unordered_set>

namespace mplan {
namespace {

std::string join(const std::vector<std::string>& values,
                 std::string_view separator = ",") {
    std::ostringstream out;
    for (std::size_t i = 0; i < values.size(); ++i) {
        if (i != 0) out << separator;
        out << values[i];
    }
    return out.str();
}

std::string setCanonical(const std::set<std::string>& values) {
    std::vector<std::string> ordered(values.begin(), values.end());
    return "{" + join(ordered) + "}";
}

std::string conditionCanonical(const PlatformCondition& condition) {
    std::vector<std::string> parts;
    parts.push_back("os=" + condition.os.value_or("*"));
    parts.push_back("arch=" + condition.arch.value_or("*"));
    parts.push_back("all=" + setCanonical(condition.allFeatures));
    parts.push_back("absent=" + setCanonical(condition.absentFeatures));
    return join(parts, ";");
}

std::string manifestCanonical(const ModuleManifest& manifest) {
    std::vector<std::string> lines;
    lines.push_back("schema=1");
    lines.push_back("module=" + manifest.coordinates.canonical());
    lines.push_back("publisher=" + manifest.publisher);
    lines.push_back("generation=" + std::to_string(manifest.keyGeneration));
    lines.push_back("abi=" + manifest.abiDigest);
    lines.push_back("resident=" + std::to_string(manifest.residentBytes));
    lines.push_back("transition-shim=" +
                    std::to_string(manifest.transitionShimBytes));
    lines.push_back("permissions=" + setCanonical(manifest.permissions));

    for (const Dependency& dependency : manifest.dependencies) {
        lines.push_back("dependency=" + dependency.module.canonical() + "@" +
                        dependency.version.str() +
                        ";when=" + conditionCanonical(dependency.when) +
                        ";required=" + (dependency.required ? "1" : "0") +
                        ";bridge=" + dependency.bridgeId.value_or("*"));
        for (const SymbolRequirement& symbol : dependency.importedSymbols) {
            lines.push_back("import=" + symbol.name + ";v=" + symbol.version +
                            ";contract=" + symbol.narrowContract);
        }
    }

    for (const ExportedSymbol& symbol : manifest.exports) {
        lines.push_back("export=" + symbol.name + ";v=" + symbol.version +
                        ";abi=" + symbol.abiDigest +
                        ";contract=" + symbol.narrowContract +
                        ";kind=" + (symbol.kind == InterfaceKind::Narrow
                                        ? "narrow"
                                        : "broad-abi") +
                        ";permissions=" +
                        setCanonical(symbol.requiredPermissions));
        for (const ReexportEdge& edge : symbol.reexports) {
            lines.push_back("reexport=" + edge.dependency.canonical() + ":" +
                            edge.sourceSymbol);
        }
    }
    return join(lines, "\n");
}

std::string bridgeCanonical(const Bridge& bridge) {
    std::vector<std::string> lines;
    lines.push_back("bridge=" + bridge.bridgeId);
    lines.push_back("route=" + bridge.fromNamespace + "->" +
                    bridge.toNamespace);
    lines.push_back("publisher=" + bridge.publisher);
    lines.push_back("generation=" + std::to_string(bridge.keyGeneration));
    lines.push_back("when=" + conditionCanonical(bridge.condition));
    lines.push_back("granted=" + setCanonical(bridge.grantedPermissions));
    lines.push_back("allow-broad-abi=" +
                    std::string(bridge.allowsBroadAbi ? "1" : "0"));
    for (const BridgeBinding& binding : bridge.bindings) {
        lines.push_back("binding=" + binding.importedSymbol + "->" +
                        binding.targetSymbol +
                        ";contract=" + binding.narrowContract +
                        ";shim=" + std::to_string(binding.shimBytes) +
                        ";permissions=" +
                        setCanonical(binding.requiredPermissions));
    }
    return join(lines, "\n");
}

BindingWitness moduleWitness(const SignedModule& module,
                             const KeyRing* keyRing,
                             const ISignatureVerifier* verifier) {
    BindingWitness witness;
    witness.witnessId = "module:" + module.manifest.coordinates.canonical();
    witness.kind = WitnessKind::ModuleSignature;
    witness.subject = module.manifest.coordinates.canonical();
    witness.publisher = module.manifest.publisher;
    witness.keyGeneration = module.manifest.keyGeneration;
    witness.abiDigest = module.manifest.abiDigest;
    witness.bindingDigest = fingerprint64(manifestCanonical(module.manifest));

    bool signatureOk = true;
    if (verifier != nullptr) {
        signatureOk = verifier->verifyModule(module);
    }

    GenerationTrust trust = GenerationTrust::UnknownPublisher;
    if (keyRing != nullptr) {
        trust = generationTrust(*keyRing, module.manifest.publisher,
                                module.manifest.keyGeneration);
    }

    witness.satisfied = signatureOk;
    if (!signatureOk) {
        witness.satisfied = false;
        witness.code = StatusCode::InvalidModuleSignature;
        witness.details.push_back("signature verification failed");
    } else if (keyRing != nullptr && trust == GenerationTrust::Revoked) {
        witness.code = StatusCode::RevokedKeyGeneration;
        witness.details.push_back("public-key generation revoked; retained "
                                  "pinned copy must not be reloaded");
    } else if (keyRing != nullptr && trust != GenerationTrust::Accepted) {
        witness.code = StatusCode::UntrustedKeyGeneration;
        witness.satisfied = false;
        witness.details.push_back("public-key generation outside trusted "
                                  "window");
    } else {
        witness.code = StatusCode::Ok;
    }

    witness.fingerprint = witnessFingerprint(witness);
    return witness;
}

BindingWitness bridgeWitness(const Bridge& bridge,
                             const Platform& platform,
                             const KeyRing* keyRing,
                             const ISignatureVerifier* verifier) {
    BindingWitness witness;
    witness.witnessId = "bridge:" + bridge.bridgeId;
    witness.kind = WitnessKind::BridgeSignature;
    witness.subject = bridge.bridgeId;
    witness.publisher = bridge.publisher;
    witness.keyGeneration = bridge.keyGeneration;
    witness.bindingDigest = fingerprint64(bridgeCanonical(bridge));

    bool signatureOk = true;
    if (verifier != nullptr) {
        signatureOk = verifier->verifyBridge(bridge);
    }
    const bool platformOk = bridge.condition.matches(platform);

    witness.satisfied = signatureOk && platformOk;
    if (!signatureOk) {
        witness.code = StatusCode::BridgeSignatureInvalid;
        witness.details.push_back("bridge signature verification failed");
    } else if (!platformOk) {
        witness.code = StatusCode::BridgePlatformExcluded;
        witness.details.push_back("Bridge::when excludes this platform");
    } else if (keyRing != nullptr) {
        const GenerationTrust trust = generationTrust(
            *keyRing, bridge.publisher, bridge.keyGeneration);
        if (trust == GenerationTrust::Revoked) {
            witness.code = StatusCode::RevokedKeyGeneration;
            witness.satisfied = false;
        } else if (trust != GenerationTrust::Accepted) {
            witness.code = StatusCode::UntrustedKeyGeneration;
            witness.satisfied = false;
        }
    }
    witness.fingerprint = witnessFingerprint(witness);
    return witness;
}

const ExportedSymbol* findExport(const SignedModule& module,
                                 std::string_view symbolName) {
    for (const ExportedSymbol& symbol : module.manifest.exports) {
        if (symbol.name == symbolName) return &symbol;
    }
    return nullptr;
}

const Dependency* findDependency(const ModuleManifest& manifest,
                                 const ModuleId& id) {
    for (const Dependency& dependency : manifest.dependencies) {
        if (dependency.module == id) return &dependency;
    }
    return nullptr;
}

struct BridgeSelection {
    StatusCode code = StatusCode::Ok;
    std::string message;
    const Bridge* bridge = nullptr;
    std::vector<BindingWitness> witnesses;
};

StatusCode evaluateBridgeBinding(const Catalog& catalog,
                                 const Bridge& bridge,
                                 const SignedModule& importer,
                                 const ModuleCoordinates& targetCoordinates,
                                 const SymbolRequirement& requirement) {
    const BridgeBinding* binding = nullptr;
    for (const BridgeBinding& candidate : bridge.bindings) {
        if (candidate.importedSymbol == requirement.name) {
            binding = &candidate;
            break;
        }
    }
    if (binding == nullptr) {
        return StatusCode::BridgeBindingMissing;
    }

    if (bridge.allowsBroadAbi) {
        return StatusCode::BroadAbiSubstitution;
    }
    if (!requirement.narrowContract.empty() &&
        binding->narrowContract != requirement.narrowContract) {
        return StatusCode::NarrowContractMismatch;
    }

    const SignedModule* target = catalog.module(targetCoordinates);
    if (target == nullptr) {
        return StatusCode::UnknownModule;
    }

    const ExportedSymbol* exported = findExport(*target, binding->targetSymbol);
    if (exported == nullptr) {
        return StatusCode::SymbolMissing;
    }
    if (exported->kind == InterfaceKind::BroadAbi ||
        exported->narrowContract.empty()) {
        return StatusCode::BroadAbiSubstitution;
    }
    if (!requirement.narrowContract.empty() &&
        exported->narrowContract != requirement.narrowContract) {
        return StatusCode::NarrowContractMismatch;
    }
    if (!requirement.version.empty() &&
        exported->version != requirement.version) {
        return StatusCode::SymbolVersionMismatch;
    }
    if (!isSubset(exported->requiredPermissions, bridge.grantedPermissions)) {
        return StatusCode::PermissionDenied;
    }
    (void)importer;
    return StatusCode::Ok;
}

BridgeSelection selectBridge(const Catalog& catalog,
                             const Platform& platform,
                             const ISignatureVerifier* verifier,
                             const SignedModule& importer,
                             const Dependency& dependency) {
    BridgeSelection result;
    const std::string fromNamespace = importer.manifest.coordinates.id.ns;
    const std::string toNamespace = dependency.module.ns;
    const ModuleCoordinates targetCoordinates{dependency.module,
                                              dependency.version};

    std::vector<const Bridge*> candidates;
    if (dependency.bridgeId.has_value()) {
        const Bridge* exact = catalog.bridgeById(*dependency.bridgeId);
        if (exact == nullptr) {
            result.code = StatusCode::BridgeMissing;
            result.message = "explicit bridge not found: " +
                             *dependency.bridgeId;
            return result;
        }
        if (exact->fromNamespace != fromNamespace ||
            exact->toNamespace != toNamespace) {
            result.code = StatusCode::BridgeMissing;
            result.message = "bridge route does not match import";
            return result;
        }
        candidates.push_back(exact);
    } else {
        candidates = catalog.bridgesBetween(fromNamespace, toNamespace);
    }

    if (candidates.empty()) {
        result.code = StatusCode::BridgeMissing;
        result.message = "no signed bridge for " + fromNamespace + " -> " +
                         toNamespace;
        return result;
    }

    std::vector<const Bridge*> platformEnabled;
    for (const Bridge* candidate : candidates) {
        if (candidate->condition.matches(platform)) {
            platformEnabled.push_back(candidate);
        }
    }
    if (platformEnabled.empty()) {
        result.code = StatusCode::BridgePlatformExcluded;
        result.message = "all " + std::to_string(candidates.size()) +
                         " bridge(s) are excluded by Bridge::when";
        result.witnesses.push_back(bridgeWitness(
            *candidates.front(), platform, &catalog.keys, verifier));
        result.witnesses.back().satisfied = false;
        result.witnesses.back().code = StatusCode::BridgePlatformExcluded;
        result.witnesses.back().fingerprint =
            witnessFingerprint(result.witnesses.back());
        return result;
    }

    StatusCode firstFailure = StatusCode::Ok;
    std::string firstMessage;
    for (const Bridge* candidate : platformEnabled) {
        StatusCode candidateCode = StatusCode::Ok;
        std::string candidateMessage;

        if (verifier != nullptr && !verifier->verifyBridge(*candidate)) {
            candidateCode = StatusCode::BridgeSignatureInvalid;
            candidateMessage = "invalid signature on bridge " +
                               candidate->bridgeId;
        }

        const Permissions excess = permissionDifference(
            candidate->grantedPermissions, importer.manifest.permissions);
        if (candidateCode == StatusCode::Ok && !excess.empty()) {
            candidateCode = StatusCode::PermissionWidening;
            std::vector<std::string> extra(excess.begin(), excess.end());
            candidateMessage = "bridge grants permissions outside importer "
                               "ceiling: " +
                               join(extra);
        }

        if (candidateCode == StatusCode::Ok &&
            candidate->allowsBroadAbi) {
            candidateCode = StatusCode::BroadAbiSubstitution;
            candidateMessage = "bridge attempts to replace a narrow interface "
                               "with broad ABI export";
        }

        if (candidateCode == StatusCode::Ok) {
            for (const SymbolRequirement& requirement :
                 dependency.importedSymbols) {
                candidateCode = evaluateBridgeBinding(
                    catalog, *candidate, importer, targetCoordinates,
                    requirement);
                if (candidateCode != StatusCode::Ok) {
                    candidateMessage = "bridge binding failed for symbol " +
                                       requirement.name;
                    break;
                }
            }
        }

        result.witnesses.push_back(bridgeWitness(
            *candidate, platform, &catalog.keys, verifier));

        if (candidateCode == StatusCode::Ok) {
            result.bridge = candidate;
            result.code = StatusCode::Ok;
            return result;
        }
        if (firstFailure == StatusCode::Ok) {
            firstFailure = candidateCode;
            firstMessage = candidateMessage;
        }
        result.witnesses.back().satisfied = false;
        result.witnesses.back().code = candidateCode;
        result.witnesses.back().details.push_back(candidateMessage);
        result.witnesses.back().fingerprint =
            witnessFingerprint(result.witnesses.back());
    }

    result.code = firstFailure;
    result.message = firstMessage;
    if (result.code == StatusCode::Ok) {
        result.code = StatusCode::BridgeMissing;
        result.message = "no usable bridge binding";
    }
    return result;
}

class ClosureBuilder {
public:
    ClosureBuilder(const Catalog& catalog, const Platform& platform,
                   ClosureOptions options)
        : catalog_(catalog), platform_(platform), options_(options) {}

    Closure build(const ModuleCoordinates& root) {
        visit(root);
        closure_.residentBytes = 0;
        for (const ModuleCoordinates& coordinate : closure_.order) {
            const SignedModule* module = catalog_.module(coordinate);
            if (module != nullptr) {
                closure_.residentBytes = saturatedPlus(
                    closure_.residentBytes, module->manifest.residentBytes);
            }
        }
        return std::move(closure_);
    }

private:
    enum class VisitState : std::uint8_t { Unseen, Active, Done };

    const Catalog& catalog_;
    const Platform& platform_;
    ClosureOptions options_;
    Closure closure_;
    std::map<ModuleCoordinates, VisitState> states_;
    StatusCode trustIssue_ = StatusCode::Ok;
    std::string trustMessage_;

    StatusCode fail(StatusCode code, std::string message) {
        closure_.code = code;
        closure_.message = std::move(message);
        return code;
    }

    StatusCode visit(const ModuleCoordinates& coordinate) {
        if (closure_.code != StatusCode::Ok &&
            closure_.code != StatusCode::UntrustedKeyGeneration &&
            closure_.code != StatusCode::RevokedKeyGeneration &&
            closure_.code != StatusCode::UnknownPublisher) {
            return closure_.code;
        }

        const VisitState state = states_[coordinate];
        if (state == VisitState::Done) return StatusCode::Ok;
        if (state == VisitState::Active) {
            return reportCycle(coordinate);
        }
        states_[coordinate] = VisitState::Active;
        stack_.push_back(coordinate);

        const SignedModule* module = catalog_.module(coordinate);
        if (module == nullptr) {
            return fail(StatusCode::UnknownModule,
                         "module is not signed and catalogued: " +
                             coordinate.canonical());
        }

        closure_.witnesses.push_back(moduleWitness(
            *module, &catalog_.keys, options_.verifier));
        const BindingWitness& witness = closure_.witnesses.back();

        if (options_.verifier != nullptr &&
            !options_.verifier->verifyModule(*module)) {
            return fail(StatusCode::InvalidModuleSignature,
                        "signature verification failed for " +
                            coordinate.canonical());
        }

        const GenerationTrust trust = generationTrust(
            catalog_.keys, module->manifest.publisher,
            module->manifest.keyGeneration);
        if (options_.enforceCurrentTrust &&
            trust == GenerationTrust::Revoked) {
            trustIssue_ = StatusCode::RevokedKeyGeneration;
            trustMessage_ = "revoked public-key generation for " +
                            coordinate.canonical();
        } else if (options_.enforceCurrentTrust &&
                   trust != GenerationTrust::Accepted) {
            trustIssue_ = StatusCode::UntrustedKeyGeneration;
            trustMessage_ = "public-key generation outside accepted window " +
                            coordinate.canonical();
        }
        (void)witness;

        for (const Dependency& dependency : module->manifest.dependencies) {
            if (!dependency.when.matches(platform_)) {
                continue;
            }

            if (dependency.module.ns != coordinate.id.ns) {
                BridgeSelection bridge = selectBridge(
                    catalog_, platform_, options_.verifier, *module,
                    dependency);
                closure_.witnesses.insert(closure_.witnesses.end(),
                                          bridge.witnesses.begin(),
                                          bridge.witnesses.end());
                if (bridge.code != StatusCode::Ok) {
                    return fail(bridge.code, bridge.message);
                }
            }

            const ModuleCoordinates dependencyCoordinate{dependency.module,
                                                          dependency.version};
            const StatusCode childCode = visit(dependencyCoordinate);
            if (childCode != StatusCode::Ok) {
                return childCode;
            }
        }

        states_[coordinate] = VisitState::Done;
        stack_.pop_back();
        if (closure_.members.insert(coordinate).second) {
            closure_.order.push_back(coordinate);
        }

        if (trustIssue_ != StatusCode::Ok) {
            return fail(trustIssue_, trustMessage_);
        }
        return StatusCode::Ok;
    }

    StatusCode reportCycle(const ModuleCoordinates& repeated) {
        return cycleWithStack(repeated);
    }

    std::vector<ModuleCoordinates> stack_;

    StatusCode cycleWithStack(const ModuleCoordinates& repeated) {
        auto start = std::find(stack_.begin(), stack_.end(), repeated);
        std::vector<ModuleCoordinates> cycle;
        if (start != stack_.end()) {
            cycle.assign(start, stack_.end());
        }
        cycle.push_back(repeated);

        bool outsideGeneration = false;
        for (const ModuleCoordinates& coordinate : cycle) {
            const SignedModule* member = catalog_.module(coordinate);
            if (member == nullptr) continue;
            const GenerationTrust trust = generationTrust(
                catalog_.keys, member->manifest.publisher,
                member->manifest.keyGeneration);
            if (trust != GenerationTrust::Accepted) {
                outsideGeneration = true;
            }
        }

        if (outsideGeneration) {
            return fail(StatusCode::OutOfGenerationCycle,
                        "dependency cycle crosses an out-of-window public-key "
                        "generation");
        }
        return fail(StatusCode::DependencyCycle,
                    "dependency cycle detected");
    }
};

} // namespace

const char* toString(StatusCode code) noexcept {
    switch (code) {
    case StatusCode::Ok: return "Ok";
    case StatusCode::InvalidRequest: return "InvalidRequest";
    case StatusCode::UnknownModule: return "UnknownModule";
    case StatusCode::InvalidModuleSignature: return "InvalidModuleSignature";
    case StatusCode::UnknownPublisher: return "UnknownPublisher";
    case StatusCode::UntrustedKeyGeneration: return "UntrustedKeyGeneration";
    case StatusCode::RevokedKeyGeneration: return "RevokedKeyGeneration";
    case StatusCode::DependencyMissing: return "DependencyMissing";
    case StatusCode::DependencyCycle: return "DependencyCycle";
    case StatusCode::OutOfGenerationCycle: return "OutOfGenerationCycle";
    case StatusCode::BridgeMissing: return "BridgeMissing";
    case StatusCode::BridgeSignatureInvalid: return "BridgeSignatureInvalid";
    case StatusCode::BridgePlatformExcluded:
        return "BridgePlatformExcluded";
    case StatusCode::BridgeBindingMissing: return "BridgeBindingMissing";
    case StatusCode::NarrowInterfaceRequired:
        return "NarrowInterfaceRequired";
    case StatusCode::BroadAbiSubstitution: return "BroadAbiSubstitution";
    case StatusCode::NarrowContractMismatch:
        return "NarrowContractMismatch";
    case StatusCode::AbiMismatch: return "AbiMismatch";
    case StatusCode::SymbolMissing: return "SymbolMissing";
    case StatusCode::SymbolVersionMismatch: return "SymbolVersionMismatch";
    case StatusCode::DiamondAbiDivergence: return "DiamondAbiDivergence";
    case StatusCode::PermissionWidening: return "PermissionWidening";
    case StatusCode::PermissionDenied: return "PermissionDenied";
    case StatusCode::DestructorHookTargetMissing:
        return "DestructorHookTargetMissing";
    case StatusCode::DestructorPinningExceeded:
        return "DestructorPinningExceeded";
    case StatusCode::RollbackBudgetExceeded: return "RollbackBudgetExceeded";
    case StatusCode::RevokedGenerationReload:
        return "RevokedGenerationReload";
    case StatusCode::InvalidWitness: return "InvalidWitness";
    case StatusCode::ReviewViolation: return "ReviewViolation";
    }
    return "UnknownStatusCode";
}

const char* toString(WitnessKind kind) noexcept {
    switch (kind) {
    case WitnessKind::ModuleSignature: return "ModuleSignature";
    case WitnessKind::BridgeSignature: return "BridgeSignature";
    case WitnessKind::PlatformCondition: return "PlatformCondition";
    case WitnessKind::KeyGeneration: return "KeyGeneration";
    case WitnessKind::AbiDigest: return "AbiDigest";
    case WitnessKind::SymbolVersion: return "SymbolVersion";
    case WitnessKind::NarrowInterface: return "NarrowInterface";
    case WitnessKind::DiamondPath: return "DiamondPath";
    case WitnessKind::PermissionBoundary: return "PermissionBoundary";
    case WitnessKind::MemoryResidency: return "MemoryResidency";
    case WitnessKind::Rollback: return "Rollback";
    case WitnessKind::Pinning: return "Pinning";
    case WitnessKind::Binding: return "Binding";
    }
    return "UnknownWitnessKind";
}

std::string Version::str() const {
    return std::to_string(major) + "." + std::to_string(minor) + "." +
           std::to_string(patch);
}

std::string ModuleId::canonical() const {
    return ns + "/" + name;
}

std::string ModuleCoordinates::canonical() const {
    return id.canonical() + "@" + version.str();
}

ModuleId moduleId(std::string ns, std::string name) {
    return ModuleId{std::move(ns), std::move(name)};
}

ModuleCoordinates moduleAt(std::string ns, std::string name,
                            std::uint32_t major, std::uint32_t minor,
                            std::uint32_t patch) {
    return ModuleCoordinates{ModuleId{std::move(ns), std::move(name)},
                             Version{major, minor, patch}};
}

PlatformCondition PlatformCondition::always() {
    return {};
}

PlatformCondition PlatformCondition::onOs(std::string osName) {
    PlatformCondition condition;
    condition.os = std::move(osName);
    return condition;
}

PlatformCondition PlatformCondition::onArch(std::string archName) {
    PlatformCondition condition;
    condition.arch = std::move(archName);
    return condition;
}

PlatformCondition& PlatformCondition::requireFeature(std::string feature) {
    allFeatures.insert(std::move(feature));
    return *this;
}

PlatformCondition& PlatformCondition::excludeFeature(std::string feature) {
    absentFeatures.insert(std::move(feature));
    return *this;
}

PlatformCondition& PlatformCondition::withOs(std::string osName) {
    os = std::move(osName);
    return *this;
}

PlatformCondition& PlatformCondition::withArch(std::string archName) {
    arch = std::move(archName);
    return *this;
}

bool PlatformCondition::matches(const Platform& platform) const {
    if (os.has_value() && *os != platform.os) return false;
    if (arch.has_value() && *arch != platform.arch) return false;
    for (const std::string& feature : allFeatures) {
        if (!platform.features.contains(feature)) return false;
    }
    for (const std::string& feature : absentFeatures) {
        if (platform.features.contains(feature)) return false;
    }
    return true;
}

std::string PlatformCondition::canonical() const {
    return conditionCanonical(*this);
}

bool isSubset(const Permissions& subset, const Permissions& superset) {
    return std::includes(superset.begin(), superset.end(), subset.begin(),
                         subset.end());
}

Permissions permissionDifference(const Permissions& left,
                                 const Permissions& right) {
    Permissions result;
    std::set_difference(left.begin(), left.end(), right.begin(), right.end(),
                        std::inserter(result, result.end()));
    return result;
}

std::string fingerprint64(std::string_view text) {
    std::uint64_t hash = 1469598103934665603ULL;
    for (const unsigned char byte : text) {
        hash ^= static_cast<std::uint64_t>(byte);
        hash *= 1099511628211ULL;
    }
    std::ostringstream out;
    out << std::hex << std::nouppercase << std::setfill('0')
        << std::setw(16) << hash;
    return out.str();
}

std::string fingerprintLines(const std::vector<std::string>& lines) {
    std::string text;
    for (const std::string& line : lines) {
        text += line;
        text.push_back('\n');
    }
    return fingerprint64(text);
}

Signature TestSignatureOracle::sign(const ModuleManifest& manifest) const {
    return Signature{"test-oracle-fnv1a-v1", manifest.publisher,
                     manifest.keyGeneration,
                     fingerprint64(manifestCanonical(manifest))};
}

Signature TestSignatureOracle::sign(const Bridge& bridge) const {
    return Signature{"test-oracle-fnv1a-v1", bridge.publisher,
                     bridge.keyGeneration,
                     fingerprint64(bridgeCanonical(bridge))};
}

bool TestSignatureOracle::verifyModule(const SignedModule& module) const {
    Signature expected = sign(module.manifest);
    return module.signature.algorithm == expected.algorithm &&
           module.signature.signer == expected.signer &&
           module.signature.keyGeneration == expected.keyGeneration &&
           module.signature.value == expected.value;
}

bool TestSignatureOracle::verifyBridge(const Bridge& bridge) const {
    Signature expected = sign(bridge);
    return bridge.signature.algorithm == expected.algorithm &&
           bridge.signature.signer == expected.signer &&
           bridge.signature.keyGeneration == expected.keyGeneration &&
           bridge.signature.value == expected.value;
}

const PublisherTrust* KeyRing::find(std::string_view publisher) const {
    const auto found = publishers.find(std::string(publisher));
    if (found == publishers.end()) return nullptr;
    return &found->second;
}

PublisherTrust& KeyRing::operator[](std::string publisher) {
    return publishers[std::move(publisher)];
}

GenerationTrust generationTrust(const KeyRing& keyRing,
                                std::string_view publisher,
                                std::uint32_t generation) {
    const PublisherTrust* trust = keyRing.find(publisher);
    if (trust == nullptr) return GenerationTrust::UnknownPublisher;
    if (trust->revokedGenerations.contains(generation)) {
        return GenerationTrust::Revoked;
    }
    if (trust->acceptedGenerations.empty() ||
        trust->acceptedGenerations.contains(generation)) {
        return GenerationTrust::Accepted;
    }
    return GenerationTrust::OutsideWindow;
}

std::uint64_t Bridge::bindingShimBytes() const {
    std::uint64_t total = 0;
    for (const BridgeBinding& binding : bindings) {
        total = saturatedPlus(total, binding.shimBytes);
    }
    return total;
}

void Catalog::addModule(SignedModule module) {
    modules_[module.manifest.coordinates] = std::move(module);
}

void Catalog::addBridge(Bridge bridge) {
    bridges_.push_back(std::move(bridge));
}

const SignedModule* Catalog::module(
    const ModuleCoordinates& coordinates) const {
    const auto found = modules_.find(coordinates);
    return found == modules_.end() ? nullptr : &found->second;
}

const SignedModule* Catalog::moduleById(const ModuleId& id,
                                        const Version& version) const {
    return module(ModuleCoordinates{id, version});
}

const Bridge* Catalog::bridgeById(std::string_view bridgeId) const {
    for (const Bridge& bridge : bridges_) {
        if (bridge.bridgeId == bridgeId) return &bridge;
    }
    return nullptr;
}

std::vector<const Bridge*> Catalog::bridgesBetween(
    std::string_view fromNamespace, std::string_view toNamespace) const {
    std::vector<const Bridge*> result;
    for (const Bridge& bridge : bridges_) {
        if (bridge.fromNamespace == fromNamespace &&
            bridge.toNamespace == toNamespace) {
            result.push_back(&bridge);
        }
    }
    return result;
}

const std::map<ModuleCoordinates, SignedModule>& Catalog::modules() const {
    return modules_;
}

const std::vector<Bridge>& Catalog::bridges() const {
    return bridges_;
}

BindingWitness makeModuleWitness(const SignedModule& module,
                                 const KeyRing* keyRing,
                                 const ISignatureVerifier* verifier) {
    return moduleWitness(module, keyRing, verifier);
}

BindingWitness makeBridgeWitness(const Bridge& bridge,
                                 const Platform& platform,
                                 const KeyRing* keyRing,
                                 const ISignatureVerifier* verifier) {
    return bridgeWitness(bridge, platform, keyRing, verifier);
}

std::string witnessFingerprint(const BindingWitness& witness) {
    return fingerprintLines({
        witness.witnessId,
        std::string(toString(witness.kind)),
        witness.subject,
        witness.publisher,
        std::to_string(witness.keyGeneration),
        witness.abiDigest,
        witness.bindingDigest,
        witness.satisfied ? "satisfied" : "unsatisfied",
        std::string(toString(witness.code)),
        join(witness.details, ";")
    });
}

bool verifyWitnessFingerprint(const BindingWitness& witness) {
    return witness.fingerprint == witnessFingerprint(witness);
}

Closure computeClosure(const Catalog& catalog, const Platform& platform,
                       const ModuleCoordinates& root,
                       const ClosureOptions& options) {
    ClosureBuilder builder(catalog, platform, options);
    return builder.build(root);
}

namespace {

class DiamondTracer {
public:
    DiamondTracer(const Catalog& catalog, const Platform& platform,
                  DiamondReport& report)
        : catalog_(catalog), platform_(platform), report_(report) {}

    void trace(const ModuleCoordinates& coordinates,
               const std::string& symbolName,
               std::vector<ModuleCoordinates> path) {
        if (report_.code != StatusCode::Ok) return;

        if (std::find(path.begin(), path.end(), coordinates) != path.end()) {
            report_.code = StatusCode::DependencyCycle;
            report_.message = "re-export cycle at " + coordinates.canonical();
            return;
        }
        path.push_back(coordinates);

        const SignedModule* module = catalog_.module(coordinates);
        if (module == nullptr) {
            report_.code = StatusCode::UnknownModule;
            report_.message = "unknown module while tracing re-export";
            return;
        }

        const ExportedSymbol* symbol = findExport(*module, symbolName);
        if (symbol == nullptr) {
            report_.code = StatusCode::SymbolMissing;
            report_.message = coordinates.canonical() + " does not export " +
                              symbolName;
            return;
        }

        if (symbol->reexports.empty()) {
            report_.terminals.push_back(AbiTerminal{
                coordinates, symbol->name, symbol->version,
                symbol->abiDigest, path});
            return;
        }

        for (const ReexportEdge& edge : symbol->reexports) {
            const Dependency* dependency =
                findDependency(module->manifest, edge.dependency);
            if (dependency == nullptr) {
                report_.code = StatusCode::DependencyMissing;
                report_.message = "re-export names undeclared dependency " +
                                  edge.dependency.canonical();
                return;
            }
            if (!dependency->when.matches(platform_)) {
                continue;
            }
            trace(ModuleCoordinates{edge.dependency, dependency->version},
                  edge.sourceSymbol, path);
        }
    }

private:
    const Catalog& catalog_;
    const Platform& platform_;
    DiamondReport& report_;
};

std::uint64_t residentSum(const Catalog& catalog,
                          const std::set<ModuleCoordinates>& modules) {
    std::uint64_t total = 0;
    for (const ModuleCoordinates& coordinates : modules) {
        const SignedModule* module = catalog.module(coordinates);
        if (module != nullptr) {
            total = saturatedPlus(total, module->manifest.residentBytes);
        }
    }
    return total;
}

ResidencyRegion makeRegion(std::string name, std::string kind,
                           std::uint64_t bytes,
                           std::vector<ModuleCoordinates> modules = {},
                           std::vector<std::string> bridgeIds = {},
                           std::vector<BindingWitness> witnesses = {}) {
    ResidencyRegion region;
    region.name = std::move(name);
    region.kind = std::move(kind);
    region.bytes = bytes;
    region.modules = std::move(modules);
    region.bridgeIds = std::move(bridgeIds);
    region.witnesses = std::move(witnesses);
    std::vector<std::string> lines{
        region.name, region.kind, std::to_string(region.bytes)};
    for (const ModuleCoordinates& coordinate : region.modules) {
        lines.push_back("module:" + coordinate.canonical());
    }
    for (const std::string& bridgeId : region.bridgeIds) {
        lines.push_back("bridge:" + bridgeId);
    }
    for (const BindingWitness& witness : region.witnesses) {
        lines.push_back("witness:" + witness.fingerprint);
    }
    region.fingerprint = fingerprintLines(lines);
    return region;
}

RollbackWitness makeRollbackWitness(
    const Catalog& catalog, const PlanRequest& request,
    const std::set<ModuleCoordinates>& pinned,
    std::uint64_t retainedBaseBytes,
    std::uint64_t peakBytes,
    const std::vector<ModuleCoordinates>& loadedOrder) {
    RollbackWitness rollback;
    rollback.initialRoot = request.initialRoot;
    rollback.initialClosureRetained = true;
    rollback.retainedBytes = retainedBaseBytes;
    rollback.peakRollbackBytes = peakBytes;
    rollback.retainedModules.assign(pinned.begin(), pinned.end());

    for (const ModuleCoordinates& coordinates : rollback.retainedModules) {
        const SignedModule* module = catalog.module(coordinates);
        const bool revoked = module != nullptr &&
            generationTrust(catalog.keys, module->manifest.publisher,
                            module->manifest.keyGeneration) ==
                GenerationTrust::Revoked;
        const std::uint64_t bytes =
            module == nullptr ? 0 : module->manifest.residentBytes;
        if (revoked) {
            rollback.retainedRevokedGenerations.push_back(
                coordinates.canonical());
            rollback.actions.push_back(RollbackAction{
                "retain-pinned-revoked-generation-reload-forbidden",
                coordinates, coordinates.canonical(), bytes, false});
        } else {
            rollback.actions.push_back(RollbackAction{
                "retain-pinned-initial-snapshot", coordinates,
                coordinates.canonical(), bytes, false});
        }
    }

    for (auto it = loadedOrder.rbegin(); it != loadedOrder.rend(); ++it) {
        const SignedModule* module = catalog.module(*it);
        rollback.actions.push_back(RollbackAction{
            "quiesce-and-discard-loaded-prefix", *it, it->canonical(),
            module == nullptr ? 0 : module->manifest.residentBytes, false});
    }
    rollback.actions.push_back(RollbackAction{
        "release-transition-shims", {}, "transition-shims", 0, false});

    std::vector<std::string> fingerprintLinesInput;
    fingerprintLinesInput.push_back(rollback.initialRoot.canonical());
    fingerprintLinesInput.push_back(std::to_string(retainedBaseBytes));
    fingerprintLinesInput.push_back(std::to_string(peakBytes));
    for (const std::string& retained : rollback.retainedRevokedGenerations) {
        fingerprintLinesInput.push_back("retained-revoked:" + retained);
    }
    for (const RollbackAction& action : rollback.actions) {
        fingerprintLinesInput.push_back(action.kind + ":" + action.subject +
                                        ":" + std::to_string(action.bytes));
    }
    rollback.fingerprint = fingerprintLines(fingerprintLinesInput);
    return rollback;
}

std::string stepFingerprint(const ResidencyStep& step) {
    std::vector<std::string> lines{
        std::to_string(step.index), step.phase, step.action,
        step.module.canonical(),
        std::to_string(step.oldPinnedBytes),
        std::to_string(step.newClosureBytes),
        std::to_string(step.shimBytes),
        std::to_string(step.forwardPeakBytes),
        std::to_string(step.rollbackPeakBytes),
        std::to_string(step.budgetBytes),
        step.rollbackInvariantSatisfied ? "rollback-ok" : "rollback-blocked",
        step.rollback.fingerprint
    };
    for (const ResidencyRegion& region : step.regions) {
        lines.push_back(region.kind + "=" +
                        std::to_string(region.bytes) + ":" +
                        region.fingerprint);
    }
    return fingerprintLines(lines);
}

} // namespace

DiamondReport checkReexportDiamonds(const Catalog& catalog,
                                    const Platform& platform,
                                    const ModuleCoordinates& root) {
    DiamondReport report;
    const Closure structural = computeClosure(
        catalog, platform, root,
        ClosureOptions{nullptr, false, false});
    if (structural.code != StatusCode::Ok) {
        report.code = structural.code;
        report.message = structural.message;
        return report;
    }

    DiamondTracer tracer(catalog, platform, report);
    for (const ModuleCoordinates& coordinates : structural.order) {
        const SignedModule* module = catalog.module(coordinates);
        if (module == nullptr) continue;
        for (const ExportedSymbol& symbol : module->manifest.exports) {
            const std::size_t terminalStart = report.terminals.size();
            tracer.trace(coordinates, symbol.name, {});
            if (report.code != StatusCode::Ok) {
                return report;
            }

            std::set<std::string> abiDigests;
            for (std::size_t i = terminalStart;
                 i < report.terminals.size(); ++i) {
                if (!report.terminals[i].abiDigest.empty()) {
                    abiDigests.insert(report.terminals[i].abiDigest);
                }
            }
            if (abiDigests.size() > 1) {
                report.code = StatusCode::DiamondAbiDivergence;
                report.subject = coordinates.canonical() + ":" + symbol.name;
                report.message = "diamond re-export reaches inconsistent ABI "
                                 "digests for " +
                                 report.subject;
                for (const AbiTerminal& terminal : report.terminals) {
                    BindingWitness witness;
                    witness.witnessId = "diamond:" + report.subject + ":" +
                                        terminal.definingModule.canonical();
                    witness.kind = WitnessKind::DiamondPath;
                    witness.subject = report.subject;
                    witness.abiDigest = terminal.abiDigest;
                    witness.satisfied = false;
                    witness.code = StatusCode::DiamondAbiDivergence;
                    witness.details.push_back(
                        "terminal=" + terminal.definingModule.canonical() +
                        " symbol=" + terminal.symbol +
                        " version=" + terminal.version);
                    std::vector<std::string> pathParts;
                    for (const ModuleCoordinates& pathNode : terminal.path) {
                        pathParts.push_back(pathNode.canonical());
                    }
                    witness.details.push_back("path=" + join(pathParts, " -> "));
                    witness.fingerprint = witnessFingerprint(witness);
                    report.witnesses.push_back(std::move(witness));
                }
                return report;
            }
        }
    }
    return report;
}

bool RollbackWitness::safeWithinBudget(std::uint64_t budgetBytes) const {
    return initialClosureRetained && reloadBytes == 0 &&
           forbiddenRevokedReloads.empty() &&
           peakRollbackBytes <= budgetBytes;
}

PlanResult planMigration(const Catalog& catalog, const PlanRequest& request) {
    PlanResult result;
    MigrationPlan& plan = result.plan;
    plan.initialRoot = request.initialRoot;
    plan.targetRoot = request.targetRoot;
    plan.platform = request.platform;
    plan.budgetBytes = request.budget.peakResidentBytes;

    if (request.verifier == nullptr) {
        result.code = StatusCode::InvalidRequest;
        result.message = "a signature verifier is required";
        return result;
    }
    if (request.initialRoot.id.name.empty() ||
        request.targetRoot.id.name.empty()) {
        result.code = StatusCode::InvalidRequest;
        result.message = "initial and target roots are required";
        return result;
    }

    const ClosureOptions targetOptions{request.verifier, true, false};
    Closure targetClosure = computeClosure(
        catalog, request.platform, request.targetRoot, targetOptions);
    if (targetClosure.code != StatusCode::Ok) {
        result.code = targetClosure.code;
        result.message = targetClosure.message;
        return result;
    }

    const ClosureOptions initialOptions{request.verifier, false, true};
    Closure initialClosure = computeClosure(
        catalog, request.platform, request.initialRoot, initialOptions);
    if (initialClosure.code != StatusCode::Ok) {
        result.code = initialClosure.code;
        result.message = initialClosure.message;
        return result;
    }

    DiamondReport diamond = checkReexportDiamonds(
        catalog, request.platform, request.targetRoot);
    if (diamond.code != StatusCode::Ok) {
        result.code = diamond.code;
        result.message = diamond.message;
        return result;
    }

    HandlePinRegistry pinRegistry(catalog, request.platform,
                                   request.verifier);
    for (const OpenHandle& handle : request.handles) {
        const StatusCode pinCode = pinRegistry.pin(handle);
        if (pinCode != StatusCode::Ok) {
            result.code = pinCode;
            result.message = "two-phase pinning failed for handle " +
                             handle.handleId;
            return result;
        }
    }

    std::set<ModuleCoordinates> pinnedSet = initialClosure.members;
    std::uint64_t destructorScratchBytes = 0;
    std::set<std::string> pinnedBridgeIds;
    for (const auto& [handleId, lease] : pinRegistry.leases()) {
        (void)handleId;
        pinnedSet.insert(lease.pinnedModules.begin(),
                          lease.pinnedModules.end());
        pinnedBridgeIds.insert(lease.pinnedBridgeIds.begin(),
                               lease.pinnedBridgeIds.end());
        destructorScratchBytes = saturatedPlus(destructorScratchBytes,
                                               lease.scratchBytes);
        plan.pinLeases.push_back(lease);
    }

    std::set<ModuleCoordinates> extraPinned;
    for (const ModuleCoordinates& coordinate : pinnedSet) {
        if (!initialClosure.members.contains(coordinate)) {
            extraPinned.insert(coordinate);
        }
    }

    const std::uint64_t oldClosureBytes = initialClosure.residentBytes;
    const std::uint64_t extraPinnedBytes =
        residentSum(catalog, extraPinned);
    std::uint64_t hookShimBytes = 0;
    for (const std::string& bridgeId : pinnedBridgeIds) {
        const Bridge* bridge = catalog.bridgeById(bridgeId);
        if (bridge != nullptr) {
            hookShimBytes = saturatedPlus(hookShimBytes,
                                          bridge->bindingShimBytes());
        }
    }

    const std::uint64_t oldPinnedBytes = saturatedPlus(
        saturatedPlus(oldClosureBytes, extraPinnedBytes),
        destructorScratchBytes);
    const std::uint64_t baselineShimBytes = hookShimBytes;
    const std::uint64_t baselinePeak =
        saturatedPlus(oldPinnedBytes, baselineShimBytes);

    std::vector<ModuleCoordinates> extraModules(extraPinned.begin(),
                                                extraPinned.end());
    std::vector<std::string> hookBridgeIds(pinnedBridgeIds.begin(),
                                           pinnedBridgeIds.end());

    ResidencyStep baseline;
    baseline.index = 0;
    baseline.phase = "two-phase-pinning";
    baseline.action = "pin-old-snapshot-and-destructor-bridge-targets";
    baseline.oldPinnedBytes = oldPinnedBytes;
    baseline.newClosureBytes = 0;
    baseline.shimBytes = baselineShimBytes;
    baseline.forwardPeakBytes = baselinePeak;
    baseline.rollbackPeakBytes = baselinePeak;
    baseline.budgetBytes = request.budget.peakResidentBytes;
    baseline.regions.push_back(makeRegion(
        "old snapshot closure", "old-closure", oldClosureBytes,
        initialClosure.order));
    baseline.regions.push_back(makeRegion(
        "destructor bridge target closure", "hook-bridge-targets",
        extraPinnedBytes, extraModules));
    baseline.regions.push_back(makeRegion(
        "two-phase destructor scratch", "destructor-scratch",
        destructorScratchBytes));
    baseline.regions.push_back(makeRegion(
        "pinned bridge shims", "transition-shim", baselineShimBytes, {},
        hookBridgeIds));
    baseline.bindings = initialClosure.witnesses;
    baseline.rollback = makeRollbackWitness(
        catalog, request, pinnedSet, oldPinnedBytes + baselineShimBytes,
        baselinePeak, {});
    baseline.rollbackInvariantSatisfied =
        baseline.rollback.safeWithinBudget(request.budget.peakResidentBytes);
    baseline.fingerprint = stepFingerprint(baseline);
    plan.steps.push_back(std::move(baseline));

    if (baselinePeak > request.budget.peakResidentBytes) {
        result.code = StatusCode::DestructorPinningExceeded;
        result.message = "mandatory old snapshot and two-phase destructor "
                         "pinning require " + std::to_string(baselinePeak) +
                         " bytes; budget is " +
                         std::to_string(request.budget.peakResidentBytes);
        plan.peakBytes = baselinePeak;
        plan.planFingerprint = fingerprintLines({plan.steps.back().fingerprint});
        return result;
    }

    std::uint64_t newClosureBytes = 0;
    std::uint64_t shimBytes = baselineShimBytes;
    std::set<std::string> countedBindings;
    std::set<std::string> countedWholeBridges;
    std::set<std::string> activeBridgeIds = pinnedBridgeIds;
    for (const std::string& bridgeId : pinnedBridgeIds) {
        countedWholeBridges.insert(bridgeId);
        const Bridge* pinnedBridge = catalog.bridgeById(bridgeId);
        if (pinnedBridge != nullptr) {
            for (const BridgeBinding& binding : pinnedBridge->bindings) {
                countedBindings.insert(bridgeId + ":" +
                                       binding.importedSymbol);
            }
        }
    }
    std::vector<ModuleCoordinates> loadedOrder;

    for (std::size_t orderIndex = 0;
         orderIndex < targetClosure.order.size(); ++orderIndex) {
        const ModuleCoordinates coordinate = targetClosure.order[orderIndex];
        const SignedModule* module = catalog.module(coordinate);
        if (module == nullptr) {
            result.code = StatusCode::UnknownModule;
            result.message = coordinate.canonical();
            return result;
        }

        loadedOrder.push_back(coordinate);
        newClosureBytes = saturatedPlus(newClosureBytes,
                                        module->manifest.residentBytes);
        std::uint64_t addedShimBytes = module->manifest.transitionShimBytes;
        std::vector<BindingWitness> stepBindings;
        stepBindings.push_back(makeModuleWitness(
            *module, &catalog.keys, request.verifier));

        for (const Dependency& dependency : module->manifest.dependencies) {
            if (!dependency.when.matches(request.platform)) continue;
            if (dependency.module.ns == coordinate.id.ns) continue;

            BridgeSelection selection = selectBridge(
                catalog, request.platform, request.verifier, *module,
                dependency);
            if (selection.code != StatusCode::Ok ||
                selection.bridge == nullptr) {
                result.code = selection.code;
                result.message = selection.message;
                return result;
            }
            stepBindings.insert(stepBindings.end(),
                                selection.witnesses.begin(),
                                selection.witnesses.end());
            activeBridgeIds.insert(selection.bridge->bridgeId);

            if (dependency.importedSymbols.empty()) {
                if (countedWholeBridges.insert(selection.bridge->bridgeId)
                        .second) {
                    addedShimBytes = saturatedPlus(
                        addedShimBytes,
                        selection.bridge->bindingShimBytes());
                }
                continue;
            }

            for (const SymbolRequirement& requirement :
                 dependency.importedSymbols) {
                const std::string key = selection.bridge->bridgeId + ":" +
                                        requirement.name;
                if (!countedBindings.insert(key).second) continue;
                for (const BridgeBinding& binding :
                     selection.bridge->bindings) {
                    if (binding.importedSymbol == requirement.name) {
                        addedShimBytes = saturatedPlus(addedShimBytes,
                                                       binding.shimBytes);
                        break;
                    }
                }
            }
        }

        shimBytes = saturatedPlus(shimBytes, addedShimBytes);
        const std::uint64_t peakBytes = saturatedPlus(
            saturatedPlus(oldPinnedBytes, newClosureBytes), shimBytes);

        ResidencyStep step;
        step.index = orderIndex + 1;
        step.phase = "load-new-closure-prefix";
        step.action = "load " + coordinate.canonical();
        step.module = coordinate;
        step.oldPinnedBytes = oldPinnedBytes;
        step.newClosureBytes = newClosureBytes;
        step.shimBytes = shimBytes;
        step.forwardPeakBytes = peakBytes;
        step.rollbackPeakBytes = peakBytes;
        step.budgetBytes = request.budget.peakResidentBytes;
        step.bindings = std::move(stepBindings);

        std::vector<ModuleCoordinates> loadedModules = loadedOrder;
        std::vector<std::string> bridgeIds(activeBridgeIds.begin(),
                                           activeBridgeIds.end());
        step.regions.push_back(makeRegion(
            "old snapshot closure", "old-closure", oldClosureBytes,
            initialClosure.order));
        step.regions.push_back(makeRegion(
            "destructor bridge target closure", "hook-bridge-targets",
            extraPinnedBytes, extraModules));
        step.regions.push_back(makeRegion(
            "two-phase destructor scratch", "destructor-scratch",
            destructorScratchBytes));
        step.regions.push_back(makeRegion(
            "loaded new closure prefix", "new-closure", newClosureBytes,
            loadedModules));
        step.regions.push_back(makeRegion(
            "active transition and bridge shims", "transition-shim",
            shimBytes, {}, bridgeIds));

        step.rollback = makeRollbackWitness(
            catalog, request, pinnedSet, oldPinnedBytes + baselineShimBytes,
            peakBytes, loadedOrder);
        step.rollbackInvariantSatisfied =
            step.rollback.safeWithinBudget(request.budget.peakResidentBytes);
        step.fingerprint = stepFingerprint(step);

        if (peakBytes > request.budget.peakResidentBytes) {
            step.rollbackInvariantSatisfied = false;
            step.fingerprint = stepFingerprint(step);
            plan.steps.push_back(std::move(step));
            plan.peakBytes = peakBytes;
            result.code = StatusCode::RollbackBudgetExceeded;
            result.message = "step " +
                             std::to_string(plan.steps.size() - 1) +
                             " cannot roll back within peak budget: " +
                             std::to_string(peakBytes) + " > " +
                             std::to_string(request.budget.peakResidentBytes);
            plan.planFingerprint = fingerprintLines({
                plan.steps.back().fingerprint});
            return result;
        }

        plan.steps.push_back(std::move(step));
        plan.peakBytes = std::max(plan.peakBytes, peakBytes);
    }

    plan.witnesses.insert(plan.witnesses.end(),
                          targetClosure.witnesses.begin(),
                          targetClosure.witnesses.end());
    plan.witnesses.insert(plan.witnesses.end(),
                          diamond.witnesses.begin(),
                          diamond.witnesses.end());

    std::vector<std::string> stepFingerprints;
    for (const ResidencyStep& step : plan.steps) {
        stepFingerprints.push_back(step.fingerprint);
    }
    plan.planFingerprint = fingerprintLines(stepFingerprints);
    result.code = StatusCode::Ok;
    result.message = "migration plan is rollback-safe at every step";
    return result;
}

ReviewReport reviewPlan(const Catalog& catalog,
                        const PlanRequest& request,
                        const MigrationPlan& plan) {
    ReviewReport report;

    auto violate = [&](StatusCode code, std::string subject,
                       std::string message) {
        report.ok = false;
        report.violations.push_back(
            ReviewViolation{code, std::move(subject), std::move(message)});
    };

    if (plan.initialRoot != request.initialRoot ||
        plan.targetRoot != request.targetRoot) {
        violate(StatusCode::InvalidRequest, "roots",
                "plan roots do not match review request");
    }
    if (plan.budgetBytes != request.budget.peakResidentBytes) {
        violate(StatusCode::InvalidRequest, "budget",
                "plan budget does not match request");
    }

    const std::string expectedPlanFingerprint = [&] {
        std::vector<std::string> fingerprints;
        for (const ResidencyStep& step : plan.steps) {
            fingerprints.push_back(step.fingerprint);
        }
        return fingerprintLines(fingerprints);
    }();
    if (plan.planFingerprint != expectedPlanFingerprint) {
        violate(StatusCode::InvalidWitness, "plan",
                "plan fingerprint does not bind its step witnesses");
    }

    if (plan.pinLeases.size() != request.handles.size()) {
        violate(StatusCode::InvalidWitness, "pinning",
                "plan does not return one two-phase pin lease per handle");
    }
    for (const PinLease& lease : plan.pinLeases) {
        ++report.checkedWitnesses;
        if (!verifyWitnessFingerprint(lease.witness)) {
            violate(StatusCode::InvalidWitness, lease.witness.witnessId,
                    "pin lease witness fingerprint mismatch");
        }
        if (lease.phase != PinPhase::Open || lease.pinnedModules.empty()) {
            violate(StatusCode::InvalidWitness, lease.handleId,
                    "migration plan must hold phase-one strong pins");
        }
    }

    for (std::size_t index = 0; index < plan.steps.size(); ++index) {
        const ResidencyStep& step = plan.steps[index];
        const std::string stepSubject = "step:" + std::to_string(index);

        if (step.index != index) {
            violate(StatusCode::InvalidWitness, stepSubject,
                    "step index is not contiguous");
        }
        if (step.fingerprint != stepFingerprint(step)) {
            violate(StatusCode::InvalidWitness, stepSubject,
                    "step fingerprint mismatch");
        }

        std::uint64_t regionBytes = 0;
        for (const ResidencyRegion& region : step.regions) {
            regionBytes = saturatedPlus(regionBytes, region.bytes);
            ++report.checkedWitnesses;
            if (region.fingerprint != [&] {
                    ResidencyRegion copy = region;
                    copy.fingerprint.clear();
                    return makeRegion(copy.name, copy.kind, copy.bytes,
                                      copy.modules, copy.bridgeIds,
                                      copy.witnesses)
                        .fingerprint;
                }()) {
                violate(StatusCode::InvalidWitness,
                        stepSubject + ":" + region.kind,
                        "residency region fingerprint mismatch");
            }
            for (const BindingWitness& witness : region.witnesses) {
                ++report.checkedWitnesses;
                if (!verifyWitnessFingerprint(witness)) {
                    violate(StatusCode::InvalidWitness, witness.witnessId,
                            "binding witness fingerprint mismatch");
                }
            }
        }

        const std::uint64_t calculatedPeak = saturatedPlus(
            saturatedPlus(step.oldPinnedBytes, step.newClosureBytes),
            step.shimBytes);
        if (regionBytes != calculatedPeak ||
            calculatedPeak != step.forwardPeakBytes) {
            violate(StatusCode::InvalidWitness, stepSubject,
                    "residency bytes do not reconcile to old + new + shim");
        }
        if (step.rollbackPeakBytes != step.forwardPeakBytes) {
            violate(StatusCode::RollbackBudgetExceeded, stepSubject,
                    "rollback peak does not include the full transitional "
                    "residency");
        }
        if (step.forwardPeakBytes > plan.budgetBytes) {
            violate(index == 0 ? StatusCode::DestructorPinningExceeded
                               : StatusCode::RollbackBudgetExceeded,
                    stepSubject, "step exceeds the shared peak byte budget");
        }
        if (!step.rollback.safeWithinBudget(plan.budgetBytes) ||
            !step.rollbackInvariantSatisfied) {
            violate(StatusCode::RollbackBudgetExceeded, stepSubject,
                    "step cannot safely return to the initial snapshot");
        }
        if (step.rollback.initialRoot != request.initialRoot) {
            violate(StatusCode::RollbackBudgetExceeded, stepSubject,
                    "rollback target is not the initial snapshot");
        }
        if (step.rollback.fingerprint.empty()) {
            violate(StatusCode::InvalidWitness, stepSubject,
                    "missing rollback binding witness");
        }

        for (const RollbackAction& action : step.rollback.actions) {
            if (action.reloadsRevokedGeneration) {
                violate(StatusCode::RevokedGenerationReload,
                        action.subject,
                        "rollback would reload a revoked public-key "
                        "generation");
            }
            if (action.kind.rfind("retain-pinned-revoked", 0) == 0) {
                const ModuleCoordinates coordinates = action.module;
                const SignedModule* module = catalog.module(coordinates);
                if (module == nullptr) {
                    violate(StatusCode::InvalidWitness, action.subject,
                            "revoked retained module is absent from catalog");
                } else {
                    const GenerationTrust trust = generationTrust(
                        catalog.keys, module->manifest.publisher,
                        module->manifest.keyGeneration);
                    if (trust != GenerationTrust::Revoked) {
                        violate(StatusCode::InvalidWitness, action.subject,
                                "revoked retention witness does not match "
                                "key ring");
                    }
                }
            }
        }

        for (const BindingWitness& witness : step.bindings) {
            ++report.checkedWitnesses;
            if (!verifyWitnessFingerprint(witness)) {
                violate(StatusCode::InvalidWitness, witness.witnessId,
                        "binding witness fingerprint mismatch");
            }
            if (!witness.satisfied &&
                witness.code != StatusCode::RevokedKeyGeneration) {
                violate(witness.code, witness.witnessId,
                        "unsatisfied binding witness in reviewed plan");
            }
        }

        if (step.phase == "load-new-closure-prefix") {
            bool hasModuleWitness = false;
            for (const BindingWitness& witness : step.bindings) {
                if (witness.kind == WitnessKind::ModuleSignature &&
                    witness.subject == step.module.canonical()) {
                    hasModuleWitness = true;
                    break;
                }
            }
            if (!hasModuleWitness) {
                violate(StatusCode::InvalidWitness, stepSubject,
                        "loaded step lacks its module binding witness");
            }
        }

        report.observedPeakBytes =
            std::max(report.observedPeakBytes, step.forwardPeakBytes);
    }

    if (report.observedPeakBytes > request.budget.peakResidentBytes) {
        // A violation was already emitted for the offending step.
        report.ok = false;
    }
    return report;
}

HandlePinRegistry::HandlePinRegistry(
    const Catalog& catalog, const Platform& platform,
    const ISignatureVerifier* verifier)
    : catalog_(catalog), platform_(platform), verifier_(verifier) {}

StatusCode HandlePinRegistry::pin(const OpenHandle& handle) {
    if (leases_.contains(handle.handleId)) {
        return StatusCode::InvalidRequest;
    }

    const ClosureOptions options{verifier_, false, true};
    Closure snapshot = computeClosure(
        catalog_, platform_, handle.snapshotRoot, options);
    if (snapshot.code != StatusCode::Ok) {
        if (snapshot.code == StatusCode::UnknownModule) {
            return StatusCode::DestructorHookTargetMissing;
        }
        return snapshot.code;
    }

    PinLease lease;
    lease.handleId = handle.handleId;
    lease.snapshotRoot = handle.snapshotRoot;
    lease.phase = PinPhase::Open;
    lease.pinnedModules = snapshot.members;

    for (const DestructorHook& hook : handle.hooks) {
        lease.scratchBytes = saturatedPlus(lease.scratchBytes,
                                           hook.scratchBytes);
        for (const ModuleCoordinates& target : hook.bridgeTargets) {
            Closure targetClosure = computeClosure(
                catalog_, platform_, target, options);
            if (targetClosure.code != StatusCode::Ok) {
                return targetClosure.code == StatusCode::UnknownModule
                           ? StatusCode::DestructorHookTargetMissing
                           : targetClosure.code;
            }
            lease.pinnedModules.insert(targetClosure.members.begin(),
                                       targetClosure.members.end());
        }
        for (const std::string& bridgeId : hook.bridgeIds) {
            if (catalog_.bridgeById(bridgeId) == nullptr) {
                return StatusCode::BridgeMissing;
            }
            lease.pinnedBridgeIds.insert(bridgeId);
        }
    }

    lease.pinnedBytes = saturatedPlus(
        residentSum(catalog_, lease.pinnedModules), lease.scratchBytes);
    for (const std::string& bridgeId : lease.pinnedBridgeIds) {
        const Bridge* bridge = catalog_.bridgeById(bridgeId);
        if (bridge != nullptr) {
            lease.pinnedBytes = saturatedPlus(lease.pinnedBytes,
                                              bridge->bindingShimBytes());
        }
    }

    lease.witness.witnessId = "pin:" + lease.handleId;
    lease.witness.kind = WitnessKind::Pinning;
    lease.witness.subject = lease.handleId;
    lease.witness.satisfied = true;
    lease.witness.code = StatusCode::Ok;
    lease.witness.details = {
        "phase=open-strong-pin",
        "modules=" + std::to_string(lease.pinnedModules.size()),
        "bridges=" + std::to_string(lease.pinnedBridgeIds.size()),
        "bytes=" + std::to_string(lease.pinnedBytes)};
    lease.witness.fingerprint = witnessFingerprint(lease.witness);
    leases_.emplace(lease.handleId, std::move(lease));
    return StatusCode::Ok;
}

StatusCode HandlePinRegistry::beginDestruction(std::string_view handleId) {
    const auto found = leases_.find(std::string(handleId));
    if (found == leases_.end()) return StatusCode::InvalidRequest;
    PinLease& lease = found->second;
    if (lease.phase != PinPhase::Open) return StatusCode::InvalidRequest;

    lease.phase = PinPhase::DestructionArmed;
    lease.witness.details = {
        "phase=one-dispatch-drained-destructor-still-pinned",
        "modules=" + std::to_string(lease.pinnedModules.size()),
        "bridges=" + std::to_string(lease.pinnedBridgeIds.size()),
        "bytes=" + std::to_string(lease.pinnedBytes)};
    lease.witness.fingerprint = witnessFingerprint(lease.witness);
    return StatusCode::Ok;
}

StatusCode HandlePinRegistry::completeDestruction(
    std::string_view handleId) {
    const auto found = leases_.find(std::string(handleId));
    if (found == leases_.end()) return StatusCode::InvalidRequest;
    PinLease& lease = found->second;
    if (lease.phase != PinPhase::DestructionArmed) {
        return StatusCode::InvalidRequest;
    }

    lease.phase = PinPhase::DestructorCompleted;
    lease.witness.details = {
        "phase=two-destructor-completed-release-permitted",
        "released-bytes=" + std::to_string(lease.pinnedBytes)};
    lease.pinnedModules.clear();
    lease.pinnedBridgeIds.clear();
    lease.scratchBytes = 0;
    lease.pinnedBytes = 0;
    lease.witness.fingerprint = witnessFingerprint(lease.witness);
    return StatusCode::Ok;
}

const PinLease* HandlePinRegistry::lease(std::string_view handleId) const {
    const auto found = leases_.find(std::string(handleId));
    return found == leases_.end() ? nullptr : &found->second;
}

const std::map<std::string, PinLease>& HandlePinRegistry::leases() const {
    return leases_;
}

std::vector<const PinLease*> HandlePinRegistry::activeLeases() const {
    std::vector<const PinLease*> result;
    for (const auto& [id, lease] : leases_) {
        (void)id;
        if (lease.phase != PinPhase::DestructorCompleted) {
            result.push_back(&lease);
        }
    }
    return result;
}

std::uint64_t HandlePinRegistry::activePinnedBytesUnion() const {
    std::set<ModuleCoordinates> modules;
    std::set<std::string> bridgeIds;
    std::uint64_t scratch = 0;
    for (const PinLease* lease : activeLeases()) {
        modules.insert(lease->pinnedModules.begin(),
                       lease->pinnedModules.end());
        bridgeIds.insert(lease->pinnedBridgeIds.begin(),
                         lease->pinnedBridgeIds.end());
        scratch += lease->scratchBytes;
    }

    std::uint64_t total = saturatedPlus(residentSum(catalog_, modules),
                                        scratch);
    for (const std::string& bridgeId : bridgeIds) {
        const Bridge* bridge = catalog_.bridgeById(bridgeId);
        if (bridge != nullptr) {
            total = saturatedPlus(total, bridge->bindingShimBytes());
        }
    }
    return total;
}

} // namespace mplan
