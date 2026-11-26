#ifndef PACBTI_ANALYZER_HPP
#define PACBTI_ANALYZER_HPP

#include <cstdint>
#include <map>
#include <string>
#include <vector>

#include "pacbti/label.hpp"
#include "pacbti/model.hpp"

namespace pacbti {

enum class FindingKind : std::uint8_t {
    AuthBypassXpac,
    PoisonedBranch,
    SigningOracle,
    ModifierTypeConflict,
    MissingBtiLanding,
    UntypedIndirectBranch
};

struct SiteLoc {
    std::uint32_t block = 0;
    std::uint32_t instruction = 0;
    std::string instructionName;
};

struct Finding {
    FindingKind kind = FindingKind::SigningOracle;
    SiteLoc site;
    std::string message;
    std::vector<SiteLoc> trace;
    std::uint16_t typeId = kInvalidType;
    KeyDomain key = KeyDomain::None;
    std::uint64_t targetAddress = 0;
};

struct AnalysisReport {
    std::vector<Finding> findings;
    std::uint32_t fixpointIterations = 0;

    std::vector<const Finding*> ofKind(FindingKind kind) const {
        std::vector<const Finding*> result;
        for (const Finding& finding : findings) {
            if (finding.kind == kind) {
                result.push_back(&finding);
            }
        }
        return result;
    }

    bool hasKind(FindingKind kind) const {
        for (const Finding& finding : findings) {
            if (finding.kind == kind) {
                return true;
            }
        }
        return false;
    }

    std::size_t count(FindingKind kind) const {
        std::size_t total = 0;
        for (const Finding& finding : findings) {
            if (finding.kind == kind) {
                ++total;
            }
        }
        return total;
    }
};

class Analyzer {
public:
    explicit Analyzer(const ControlFlowGraph& cfg);

    AnalysisReport run();

    const AbstractState& blockEntry(std::uint32_t blockId) const;
    const AbstractState& blockExit(std::uint32_t blockId) const;

    static const char* findingName(FindingKind kind);
    static const char* keyName(KeyDomain key);
    static const char* btiName(BtiKind kind);

private:
    using StateMap = std::map<std::uint32_t, AbstractState>;

    const ControlFlowGraph& cfg_;
    StateMap entryStates_;
    StateMap exitStates_;
    AnalysisReport report_;

    struct ConflictKey {
        KeyDomain key = KeyDomain::None;
        OriginList modifierOrigins;

        bool operator<(const ConflictKey& other) const {
            if (key != other.key) {
                return static_cast<std::uint8_t>(key) <
                       static_cast<std::uint8_t>(other.key);
            }
            return modifierOrigins < other.modifierOrigins;
        }
    };

    struct ConflictInfo {
        std::uint16_t firstType = kInvalidType;
        SiteLoc firstSite;
    };

    std::map<ConflictKey, ConflictInfo> conflictSeen_;

    struct DedupKey {
        FindingKind kind;
        std::uint32_t block;
        std::uint32_t instruction;
        std::uint16_t typeId;
        std::uint64_t address;

        bool operator<(const DedupKey& other) const {
            if (kind != other.kind) {
                return static_cast<std::uint8_t>(kind) <
                       static_cast<std::uint8_t>(other.kind);
            }
            if (block != other.block) {
                return block < other.block;
            }
            if (instruction != other.instruction) {
                return instruction < other.instruction;
            }
            if (typeId != other.typeId) {
                return typeId < other.typeId;
            }
            return address < other.address;
        }
    };

    std::vector<DedupKey> emitted_;

    void analyzeBlock(std::uint32_t blockId);
    void transfer(const Instruction& ins,
                  std::uint32_t blockId,
                  std::uint32_t insIndex,
                  AbstractState& state);

    void doSign(const Instruction& ins,
                std::uint32_t blockId,
                std::uint32_t insIndex,
                AbstractState& state);
    void doAuth(const Instruction& ins,
                std::uint32_t blockId,
                std::uint32_t insIndex,
                AbstractState& state);
    void doXpac(const Instruction& ins,
                std::uint32_t blockId,
                std::uint32_t insIndex,
                AbstractState& state);
    void doIndirectBranch(const Instruction& ins,
                          std::uint32_t blockId,
                          std::uint32_t insIndex,
                          AbstractState& state);

    void checkOracleSink(const Label& label,
                         const SiteLoc& site,
                         const std::string& sink);
    void registerConflict(const Label& label, const SiteLoc& site);
    void checkBti(const Instruction& ins,
                  const SiteLoc& site,
                  const Label& label);

    bool isCallSite(const Instruction& ins) const;
    bool landingMatches(bool callSite, BtiKind landing) const;
    std::uint16_t resolveBranchType(const Instruction& ins, const Label& label) const;
    const CodePoint* findCodePoint(std::uint64_t address) const;

    void emit(Finding finding);
    bool alreadyEmitted(FindingKind kind,
                        const SiteLoc& site,
                        std::uint16_t typeId,
                        std::uint64_t address);
    SiteLoc locate(std::uint32_t blockId, std::uint32_t insIndex) const;
    std::vector<SiteLoc> traceOrigins(const OriginList& origins) const;
};

}  // namespace pacbti

#endif
