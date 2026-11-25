#ifndef CERTREV_REVOCATION_HPP
#define CERTREV_REVOCATION_HPP

#include <algorithm>
#include <cstdint>
#include <functional>
#include <map>
#include <memory>
#include <optional>
#include <set>
#include <sstream>
#include <string>
#include <tuple>
#include <type_traits>
#include <utility>
#include <vector>

namespace certrev {

using Id = std::string;
using Digest = std::string;
using Generation = std::uint64_t;
using SlotIndex = std::int64_t;

enum class TicketStatus {
    Kept,
    Revoked,
    Pending
};

inline const char* toString(TicketStatus status) noexcept {
    switch (status) {
        case TicketStatus::Kept: return "kept";
        case TicketStatus::Revoked: return "revoked";
        case TicketStatus::Pending: return "pending";
    }
    return "unknown";
}

enum class VerificationState {
    Unverified,
    Verified,
    Invalid
};

inline const char* toString(VerificationState state) noexcept {
    switch (state) {
        case VerificationState::Unverified: return "unverified";
        case VerificationState::Verified: return "verified";
        case VerificationState::Invalid: return "invalid";
    }
    return "unknown";
}

enum class SlotCoordinate {
    Physical,
    Logical
};

struct SlotRange {
    SlotIndex first = 0;
    SlotIndex limit = 0;

    constexpr SlotRange() = default;
    constexpr SlotRange(SlotIndex begin, SlotIndex end) : first(begin), limit(end) {}

    constexpr bool valid() const noexcept { return first <= limit; }
    constexpr bool empty() const noexcept { return first >= limit; }
    constexpr SlotIndex length() const noexcept { return limit - first; }

    constexpr bool operator==(const SlotRange& other) const noexcept {
        return first == other.first && limit == other.limit;
    }
    constexpr bool operator!=(const SlotRange& other) const noexcept { return !(*this == other); }
    constexpr bool operator<(const SlotRange& other) const noexcept {
        return first != other.first ? first < other.first : limit < other.limit;
    }
};

inline SlotRange intersectRanges(const SlotRange& a, const SlotRange& b) {
    SlotIndex first = std::max(a.first, b.first);
    SlotIndex limit = std::min(a.limit, b.limit);
    if (first > limit) return SlotRange{0, 0};
    return SlotRange{first, limit};
}

inline bool rangesOverlap(const SlotRange& a, const SlotRange& b) {
    return a.valid() && b.valid() && a.length() > 0 && b.length() > 0 &&
           a.first < b.limit && b.first < a.limit;
}

inline bool rangeContains(const SlotRange& outer, const SlotRange& inner) {
    return outer.valid() && inner.valid() && outer.first <= inner.first &&
           inner.limit <= outer.limit;
}

inline std::vector<SlotRange> normalizeRanges(std::vector<SlotRange> ranges) {
    std::vector<SlotRange> valid;
    for (SlotRange range : ranges) {
        if (range.valid() && !range.empty()) valid.push_back(range);
    }
    std::sort(valid.begin(), valid.end());
    std::vector<SlotRange> merged;
    for (SlotRange range : valid) {
        if (merged.empty() || range.first > merged.back().limit) {
            merged.push_back(range);
        } else {
            merged.back().limit = std::max(merged.back().limit, range.limit);
        }
    }
    return merged;
}

inline std::vector<SlotRange> unionRanges(const std::vector<SlotRange>& ranges) {
    return normalizeRanges(ranges);
}

inline bool coveredByUnion(SlotRange query, const std::vector<SlotRange>& cover) {
    if (query.empty()) return true;
    SlotIndex cursor = query.first;
    for (const SlotRange& range : normalizeRanges(cover)) {
        if (range.limit <= cursor) continue;
        if (range.first > cursor) return false;
        cursor = std::max(cursor, range.limit);
        if (cursor >= query.limit) return true;
    }
    return cursor >= query.limit;
}

struct MappingSegment {
    SlotRange logical;
    SlotRange physical;

    bool operator==(const MappingSegment& other) const noexcept {
        return logical == other.logical && physical == other.physical;
    }
};

struct LogicalMapping {
    Id version;
    Digest mapping_root;
    std::vector<MappingSegment> segments;
    VerificationState verification = VerificationState::Unverified;

    bool operator==(const LogicalMapping& other) const {
        return version == other.version && mapping_root == other.mapping_root &&
               segments == other.segments && verification == other.verification;
    }
};

struct MeasurementClaim {
    Id id;
    SlotRange physical;
    Digest digest;
    std::string proof_blob;
    VerificationState proof_state = VerificationState::Unverified;
};

struct Receipt {
    Id id;
    Id signer;
    Generation generation = 0;
    SlotRange physical_range;
    LogicalMapping mapping;
    Digest proof_root;
    std::vector<MeasurementClaim> claims;
    std::string signature_blob;
    VerificationState signature_state = VerificationState::Unverified;
};

struct Requirement {
    Id id;
    SlotRange slots;
    Digest expected_digest;
    SlotCoordinate coordinate = SlotCoordinate::Logical;
    std::optional<Id> mapping_version;

    static Requirement logical(Id requirement_id, SlotRange range, Digest digest = {}) {
        Requirement result;
        result.id = std::move(requirement_id);
        result.slots = range;
        result.expected_digest = std::move(digest);
        result.coordinate = SlotCoordinate::Logical;
        return result;
    }

    static Requirement physical(Id requirement_id, SlotRange range, Digest digest = {}) {
        Requirement result = logical(std::move(requirement_id), range, std::move(digest));
        result.coordinate = SlotCoordinate::Physical;
        return result;
    }
};

struct AdmissionTicket {
    Id id;
    Generation generation = 0;
    std::optional<Id> mapping_version;
    std::vector<Requirement> requirements;
};

struct Bank {
    std::vector<AdmissionTicket> tickets;
    std::vector<Receipt> receipts;
    std::vector<LogicalMapping> mappings;
};

struct Blocker {
    Id code;
    std::string message;
    Id receipt_id;
    Id requirement_id;
    Id notice_id;
};

enum class Availability {
    Usable,
    Pending,
    Dead
};

struct StateKey {
    Generation generation = 0;
    Digest proof_root;
    Id mapping_version;

    bool operator==(const StateKey& other) const noexcept {
        return generation == other.generation && proof_root == other.proof_root &&
               mapping_version == other.mapping_version;
    }
    bool operator!=(const StateKey& other) const noexcept { return !(*this == other); }
    bool operator<(const StateKey& other) const noexcept {
        return std::tie(generation, proof_root, mapping_version) <
               std::tie(other.generation, other.proof_root, other.mapping_version);
    }
};

struct EvaluationOptions {
    bool require_coherent_unpinned_mapping = true;
    bool mapping_version_is_state = false;
    bool check_physical_contradictions = true;
};

struct SignerRestoration {
    Id signer;
    std::optional<Generation> from_generation;
};

struct SlotRangeRevocation {
    Id notice_id;
    SlotRange range;
    std::optional<Generation> generation;
    Id reason;
};

struct MappingVersionRevocation {
    Id notice_id;
    Id mapping_version;
    std::optional<Generation> generation;
};

struct ProofRootRevocation {
    Id notice_id;
    Digest proof_root;
    std::optional<Generation> generation;
};

class RevocationBook {
public:
    RevocationBook() = default;

    std::uint64_t revision() const noexcept { return revision_; }

    RevocationBook& revokeSigner(const Id& signer,
                                 std::optional<Generation> generation = std::nullopt,
                                 Id notice_id = {}) {
        SignerPolicy& policy = signers_[signer];
        Id notice = makeNoticeId("signer", std::move(notice_id));
        if (generation) {
            policy.revoked_generations.insert(*generation);
            policy.revoke_notices[*generation] = notice;
        } else {
            policy.all_revoked = true;
            policy.all_notice = notice;
        }
        ++revision_;
        return *this;
    }

    RevocationBook& restoreSigner(const Id& signer,
                                  std::optional<Generation> from_generation = std::nullopt) {
        SignerPolicy& policy = signers_[signer];
        if (!from_generation) {
            policy.all_revoked = false;
            policy.trusted_from.reset();
            policy.revoked_generations.clear();
            policy.revoke_notices.clear();
            policy.all_notice.clear();
        } else {
            policy.trusted_from = *from_generation;
        }
        ++revision_;
        return *this;
    }

    bool isSignerRevoked(const Id& signer, Generation generation) const {
        auto found = signers_.find(signer);
        if (found == signers_.end()) return false;
        const SignerPolicy& policy = found->second;
        if (policy.revoked_generations.count(generation) != 0) return true;
        if (policy.trusted_from && generation >= *policy.trusted_from) return false;
        return policy.all_revoked;
    }

    Id signerNotice(const Id& signer, Generation generation) const {
        auto found = signers_.find(signer);
        if (found == signers_.end()) return {};
        const SignerPolicy& policy = found->second;
        auto notice = policy.revoke_notices.find(generation);
        if (notice != policy.revoke_notices.end()) return notice->second;
        if (policy.all_revoked &&
            !(policy.trusted_from && generation >= *policy.trusted_from)) {
            return policy.all_notice;
        }
        return {};
    }

    RevocationBook& revokeReceipt(const Id& receipt_id, Id notice_id = {}) {
        receipt_notices_[receipt_id] = makeNoticeId("receipt", std::move(notice_id));
        ++revision_;
        return *this;
    }

    bool isReceiptRevoked(const Id& receipt_id) const {
        return receipt_notices_.count(receipt_id) != 0;
    }

    Id receiptNotice(const Id& receipt_id) const {
        auto found = receipt_notices_.find(receipt_id);
        return found == receipt_notices_.end() ? Id{} : found->second;
    }

    RevocationBook& revokeProofRoot(const Digest& root,
                                    std::optional<Generation> generation = std::nullopt,
                                    Id notice_id = {}) {
        root_revocations_.push_back({
            makeNoticeId("root", std::move(notice_id)), root, generation});
        ++revision_;
        return *this;
    }

    bool isProofRootRevoked(const Digest& root, Generation generation) const {
        return !proofRootNotice(root, generation).empty();
    }

    Id proofRootNotice(const Digest& root, Generation generation) const {
        for (const ProofRootRevocation& item : root_revocations_) {
            if (item.proof_root == root && generationMatches(item.generation, generation)) {
                return item.notice_id;
            }
        }
        return {};
    }

    RevocationBook& revokeRange(SlotRange range,
                                std::optional<Generation> generation = std::nullopt,
                                Id reason = {},
                                Id notice_id = {}) {
        range_revocations_.push_back({
            makeNoticeId("range", std::move(notice_id)), range, generation, std::move(reason)});
        ++revision_;
        return *this;
    }

    const std::vector<SlotRangeRevocation>& rangeRevocations() const noexcept {
        return range_revocations_;
    }

    Id rangeNotice(Generation generation, SlotRange physical) const {
        for (const SlotRangeRevocation& item : range_revocations_) {
            if (generationMatches(item.generation, generation) &&
                rangesOverlap(item.range, physical)) {
                return item.notice_id;
            }
        }
        return {};
    }

    const SlotRangeRevocation* rangeBlocker(Generation generation, SlotRange physical) const {
        for (const SlotRangeRevocation& item : range_revocations_) {
            if (generationMatches(item.generation, generation) &&
                rangesOverlap(item.range, physical)) {
                return &item;
            }
        }
        return nullptr;
    }

    RevocationBook& revokeMappingVersion(const Id& version,
                                         std::optional<Generation> generation = std::nullopt,
                                         Id notice_id = {}) {
        mapping_revocations_.push_back({
            makeNoticeId("mapping", std::move(notice_id)), version, generation});
        ++revision_;
        return *this;
    }

    bool isMappingRevoked(const Id& version, Generation generation) const {
        return !mappingNotice(version, generation).empty();
    }

    Id mappingNotice(const Id& version, Generation generation) const {
        for (const MappingVersionRevocation& item : mapping_revocations_) {
            if (item.mapping_version == version &&
                generationMatches(item.generation, generation)) {
                return item.notice_id;
            }
        }
        return {};
    }

    static bool generationMatches(const std::optional<Generation>& scope, Generation value) {
        return !scope || *scope == value;
    }

private:
    struct SignerPolicy {
        bool all_revoked = false;
        Id all_notice;
        std::optional<Generation> trusted_from;
        std::set<Generation> revoked_generations;
        std::map<Generation, Id> revoke_notices;
    };

    Id makeNoticeId(const char* prefix, Id requested) {
        if (!requested.empty()) return requested;
        std::ostringstream out;
        out << prefix << '-' << (++notice_counter_);
        return out.str();
    }

    std::uint64_t revision_ = 0;
    std::uint64_t notice_counter_ = 0;
    std::map<Id, SignerPolicy> signers_;
    std::map<Id, Id> receipt_notices_;
    std::vector<ProofRootRevocation> root_revocations_;
    std::vector<SlotRangeRevocation> range_revocations_;
    std::vector<MappingVersionRevocation> mapping_revocations_;
};

struct ResolvedMeasurement {
    Id requirement_id;
    SlotRange requested;
    SlotRange physical;
    Digest expected_digest;
    Digest actual_digest;
    Id receipt_id;
    Id claim_id;
    Id mapping_version;
};

struct SupportBundle {
    Id ticket_id;
    AdmissionTicket ticket;
    StateKey state;
    std::vector<Id> receipt_ids;
    std::vector<Receipt> receipts;
    std::vector<ResolvedMeasurement> measurements;
    std::vector<SlotRange> physical_union;
    std::vector<Id> mapping_versions;
    std::uint64_t revocation_revision = 0;
    std::uint64_t fingerprint = 0;
    std::string canonical;
};

struct Decision {
    Id ticket_id;
    TicketStatus status = TicketStatus::Revoked;
    std::uint64_t revision = 0;
    SupportBundle support;
    SupportBundle pending_support;
    std::vector<Blocker> blockers;
    std::vector<std::string> incompatibilities;
};

struct TicketChange {
    Id ticket_id;
    TicketStatus before = TicketStatus::Kept;
    TicketStatus after = TicketStatus::Kept;
    bool support_changed = false;
    Decision decision;
};

struct TicketDiff {
    std::uint64_t from_revision = 0;
    std::uint64_t to_revision = 0;
    std::vector<TicketChange> changes;
    std::vector<SupportBundle> remaining_support;
    std::vector<Id> newly_withdrawn;
    std::vector<Id> definitively_revoked;
    std::vector<Id> newly_pending;
    std::vector<Id> restored_to_kept;
    std::vector<Id> support_updated;
};

inline std::uint64_t fnv1a64(const std::string& bytes) noexcept {
    std::uint64_t hash = 1469598103934665603ULL;
    for (unsigned char byte : bytes) {
        hash ^= static_cast<std::uint64_t>(byte);
        hash *= 1099511628211ULL;
    }
    return hash;
}

inline void appendCanonicalString(std::string& out, const std::string& value) {
    out += std::to_string(value.size());
    out.push_back(':');
    out += value;
}

inline void appendCanonicalRange(std::string& out, const SlotRange& range) {
    appendCanonicalString(out, std::to_string(range.first));
    appendCanonicalString(out, std::to_string(range.limit));
}

inline std::string canonicalReceipt(const Receipt& receipt) {
    std::string out;
    appendCanonicalString(out, "receipt");
    appendCanonicalString(out, receipt.id);
    appendCanonicalString(out, receipt.signer);
    appendCanonicalString(out, std::to_string(receipt.generation));
    appendCanonicalRange(out, receipt.physical_range);
    appendCanonicalString(out, receipt.mapping.version);
    appendCanonicalString(out, receipt.mapping.mapping_root);
    appendCanonicalString(out, receipt.proof_root);
    appendCanonicalString(out, std::to_string(static_cast<int>(receipt.signature_state)));
    for (const MappingSegment& segment : receipt.mapping.segments) {
        appendCanonicalRange(out, segment.logical);
        appendCanonicalRange(out, segment.physical);
    }
    for (const MeasurementClaim& claim : receipt.claims) {
        appendCanonicalString(out, claim.id);
        appendCanonicalRange(out, claim.physical);
        appendCanonicalString(out, claim.digest);
        appendCanonicalString(out, claim.proof_blob);
        appendCanonicalString(out, std::to_string(static_cast<int>(claim.proof_state)));
    }
    appendCanonicalString(out, receipt.signature_blob);
    return out;
}

inline std::string canonicalBundle(const SupportBundle& bundle) {
    std::string out;
    appendCanonicalString(out, "bundle");
    appendCanonicalString(out, bundle.ticket_id);
    appendCanonicalString(out, std::to_string(bundle.state.generation));
    appendCanonicalString(out, bundle.state.proof_root);
    appendCanonicalString(out, bundle.state.mapping_version);
    for (const Receipt& receipt : bundle.receipts) {
        std::string canonical = canonicalReceipt(receipt);
        appendCanonicalString(out, canonical);
    }
    for (const ResolvedMeasurement& measurement : bundle.measurements) {
        appendCanonicalString(out, measurement.requirement_id);
        appendCanonicalRange(out, measurement.physical);
        appendCanonicalString(out, measurement.expected_digest);
        appendCanonicalString(out, measurement.actual_digest);
        appendCanonicalString(out, measurement.receipt_id);
        appendCanonicalString(out, measurement.claim_id);
        appendCanonicalString(out, measurement.mapping_version);
    }
    return out;
}

class MappingCatalog {
public:
    std::map<Id, LogicalMapping> mappings;
    std::vector<Id> order;
    std::set<Id> conflicts;

    void add(const LogicalMapping& mapping) {
        if (mapping.version.empty()) return;
        auto found = mappings.find(mapping.version);
        if (found == mappings.end()) {
            mappings.emplace(mapping.version, mapping);
            order.push_back(mapping.version);
            return;
        }
        const LogicalMapping& existing = found->second;
        bool definition_differs = existing.mapping_root != mapping.mapping_root ||
                                  existing.segments != mapping.segments;
        if (definition_differs) conflicts.insert(mapping.version);
    }

    const LogicalMapping* get(const Id& version) const {
        auto found = mappings.find(version);
        return found == mappings.end() ? nullptr : &found->second;
    }

    bool conflicted(const Id& version) const { return conflicts.count(version) != 0; }
};

struct ClaimRef {
    std::size_t receipt_index = 0;
    std::size_t claim_index = 0;
};

struct ResolutionAlternative {
    SlotRange physical;
    Id mapping_version;
    bool pinned = false;
    VerificationState mapping_verification = VerificationState::Verified;
};

struct SupportAtom {
    Id requirement_id;
    SlotRange requested;
    ResolutionAlternative resolution;
    ClaimRef claim;
    StateKey state;
    Availability availability = Availability::Usable;
    std::vector<Blocker> blockers;
};

struct EvaluationReport {
    std::uint64_t revision = 0;
    std::vector<Decision> decisions;

    const Decision* find(const Id& ticket_id) const {
        for (const Decision& decision : decisions) {
            if (decision.ticket_id == ticket_id) return &decision;
        }
        return nullptr;
    }
};

class Evaluator {
public:
    Evaluator(const Bank& bank, const RevocationBook& book, EvaluationOptions options = {})
        : bank_(&bank), book_(&book), options_(options) {
        for (const LogicalMapping& mapping : bank.mappings) catalog_.add(mapping);
        for (const Receipt& receipt : bank.receipts) catalog_.add(receipt.mapping);
    }

    EvaluationReport run() const {
        EvaluationReport report;
        report.revision = book_->revision();
        report.decisions.reserve(bank_->tickets.size());
        for (const AdmissionTicket& ticket : bank_->tickets) {
            report.decisions.push_back(evaluateTicket(ticket));
        }
        return report;
    }

    Decision evaluateTicket(const AdmissionTicket& ticket) const {
        Decision decision;
        decision.ticket_id = ticket.id;
        decision.revision = book_->revision();

        std::vector<std::vector<SupportAtom>> groups;
        groups.reserve(ticket.requirements.size());
        bool missing_group = false;

        for (std::size_t q = 0; q < ticket.requirements.size(); ++q) {
            const Requirement& requirement = ticket.requirements[q];
            Id requirement_id = requirement.id.empty()
                                    ? "requirement-" + std::to_string(q + 1)
                                    : requirement.id;
            std::vector<Blocker> resolution_blockers;
            std::vector<ResolutionAlternative> alternatives =
                resolveRequirement(ticket, requirement, resolution_blockers);
            for (Blocker& blocker : resolution_blockers) {
                blocker.requirement_id = requirement_id;
                decision.blockers.push_back(blocker);
            }

            std::vector<SupportAtom> atoms;
            for (const ResolutionAlternative& alternative : alternatives) {
                for (std::size_t i = 0; i < bank_->receipts.size(); ++i) {
                    const Receipt& receipt = bank_->receipts[i];
                    if (receipt.generation != ticket.generation) continue;
                    for (std::size_t j = 0; j < receipt.claims.size(); ++j) {
                        const MeasurementClaim& claim = receipt.claims[j];
                        if (claim.physical != alternative.physical) continue;
                        if (!requirement.expected_digest.empty() &&
                            claim.digest != requirement.expected_digest) {
                            continue;
                        }
                        SupportAtom atom;
                        atom.requirement_id = requirement_id;
                        atom.requested = requirement.slots;
                        atom.resolution = alternative;
                        atom.claim = {i, j};
                        atom.state = stateKey(receipt);
                        atom.blockers = assessAtom(receipt, claim, alternative, ticket.generation,
                                                   atom.availability);
                        atoms.push_back(std::move(atom));
                    }
                }
            }

            if (atoms.empty()) {
                missing_group = true;
                decision.blockers.push_back({"no_matching_measurement",
                                             "no receipt claim resolves and matches requirement",
                                             {}, requirement_id, {}});
            }
            groups.push_back(std::move(atoms));
        }

        Candidate best_kept;
        Candidate best_pending;
        bool has_kept = false;
        bool has_pending = false;
        std::string incompatibility;

        std::vector<SupportAtom> current;
        const auto search = [&](auto&& self, std::size_t index) -> void {
            if (index == groups.size()) {
                std::string error;
                if (!assignmentCompatible(current, error)) {
                    if (incompatibility.empty()) incompatibility = error;
                    return;
                }
                Candidate candidate = makeCandidate(current);
                if (candidate.pending_count == 0) {
                    if (!has_kept || candidate < best_kept) {
                        best_kept = candidate;
                        has_kept = true;
                    }
                } else if (!has_pending || candidate < best_pending) {
                    best_pending = candidate;
                    has_pending = true;
                }
                return;
            }

            for (const SupportAtom& atom : groups[index]) {
                if (atom.availability == Availability::Dead) continue;
                current.push_back(atom);
                self(self, index + 1);
                current.pop_back();
            }
        };
        search(search, 0);

        collectAtomBlockers(groups, decision);

        if (has_kept) {
            decision.status = TicketStatus::Kept;
            decision.support = makeBundle(ticket, best_kept.atoms, true);
        } else if (has_pending) {
            decision.status = TicketStatus::Pending;
            decision.pending_support = makeBundle(ticket, best_pending.atoms, false);
            decision.blockers.push_back({"evidence_unverified",
                                         "a complete combination is possible if pending evidence verifies",
                                         {}, {}, {}});
        } else {
            decision.status = TicketStatus::Revoked;
            if (!incompatibility.empty()) decision.incompatibilities.push_back(incompatibility);
            if (incompatibility.empty()) {
                decision.incompatibilities.push_back(
                    "no live, compatible proof combination satisfies the ticket");
            }
            (void)missing_group;
        }
        return decision;
    }

    StateKey stateKey(const Receipt& receipt) const {
        return {receipt.generation, receipt.proof_root,
                options_.mapping_version_is_state ? receipt.mapping.version : Id{}};
    }

    std::vector<ResolutionAlternative> resolveRequirement(
        const AdmissionTicket& ticket, const Requirement& requirement,
        std::vector<Blocker>& blockers) const {
        std::vector<ResolutionAlternative> result;
        if (requirement.coordinate == SlotCoordinate::Physical) {
            result.push_back({requirement.slots, {}, true, VerificationState::Verified});
            return result;
        }

        Id pinned;
        if (requirement.mapping_version) pinned = *requirement.mapping_version;
        else if (ticket.mapping_version) pinned = *ticket.mapping_version;
        bool is_pinned = !pinned.empty();

        std::vector<Id> versions;
        if (is_pinned) versions.push_back(pinned);
        else versions = catalog_.order;

        for (const Id& version : versions) {
            if (book_->isMappingRevoked(version, ticket.generation)) {
                blockers.push_back({"mapping_version_revoked",
                                    "mapping version is revoked: " + version,
                                    {}, requirement.id, book_->mappingNotice(version, ticket.generation)});
                continue;
            }
            if (catalog_.conflicted(version)) {
                blockers.push_back({"mapping_version_conflict",
                                    "conflicting authenticated definitions for mapping " + version,
                                    {}, requirement.id, {}});
                continue;
            }
            const LogicalMapping* mapping = catalog_.get(version);
            if (!mapping) {
                blockers.push_back({"mapping_version_missing",
                                    "mapping version is not available: " + version,
                                    {}, requirement.id, {}});
                continue;
            }
            if (mapping->verification == VerificationState::Invalid) {
                blockers.push_back({"mapping_invalid", "mapping proof is invalid: " + version,
                                    {}, requirement.id, {}});
                continue;
            }

            for (const MappingSegment& segment : mapping->segments) {
            if (!rangeContains(segment.logical, requirement.slots)) continue;
            if (segment.logical.length() != segment.physical.length()) continue;
            SlotIndex offset = requirement.slots.first - segment.logical.first;
            if (offset < 0 || offset >= segment.physical.length()) continue;
            if (requirement.slots.length() > segment.physical.length() - offset) continue;
            SlotRange physical{segment.physical.first + offset,
                               segment.physical.first + offset + requirement.slots.length()};
                result.push_back({physical, version, is_pinned, mapping->verification});
            }
        }
        return result;
    }

    std::vector<Blocker> assessAtom(const Receipt& receipt, const MeasurementClaim& claim,
                                    const ResolutionAlternative& resolution,
                                    Generation generation, Availability& availability) const {
        bool dead = false;
        bool pending = false;
        std::vector<Blocker> blockers;

        auto addDead = [&](Blocker blocker) {
            blockers.push_back(std::move(blocker));
            dead = true;
        };
        auto addPending = [&](Blocker blocker) {
            blockers.push_back(std::move(blocker));
            pending = true;
        };

        if (receipt.signature_state == VerificationState::Invalid) {
            addDead({"invalid_signature", "receipt signature is invalid", receipt.id, {}, {}});
        } else if (receipt.signature_state == VerificationState::Unverified) {
            addPending({"signature_unverified", "receipt signature has not been verified",
                        receipt.id, {}, {}});
        }

        if (claim.proof_state == VerificationState::Invalid) {
            addDead({"invalid_proof", "measurement proof is invalid", receipt.id, {}, {}});
        } else if (claim.proof_state == VerificationState::Unverified) {
            addPending({"proof_unverified", "measurement proof has not been verified", receipt.id,
                        {}, {}});
        }

        if (resolution.mapping_verification == VerificationState::Unverified) {
            addPending({"mapping_unverified", "logical mapping proof has not been verified",
                        receipt.id, {}, {}});
        }

        if (receipt.proof_root.empty()) {
            addDead({"missing_proof_root", "receipt does not bind a complete proof root",
                     receipt.id, {}, {}});
        }

        Id receipt_notice = book_->receiptNotice(receipt.id);
        if (!receipt_notice.empty()) {
            addDead({"receipt_revoked", "receipt is revoked", receipt.id, {}, receipt_notice});
        }

        Id signer_notice = book_->signerNotice(receipt.signer, generation);
        if (!signer_notice.empty() || book_->isSignerRevoked(receipt.signer, generation)) {
            addDead({"signer_revoked", "receipt signer is revoked for this generation", receipt.id,
                     {}, signer_notice});
        }

        Id root_notice = book_->proofRootNotice(receipt.proof_root, generation);
        if (!root_notice.empty()) {
            addDead({"proof_root_revoked", "complete proof root is revoked", receipt.id, {},
                     root_notice});
        }

        const SlotRangeRevocation* range_blocker = book_->rangeBlocker(generation, claim.physical);
        if (range_blocker) {
            addDead({"physical_range_revoked",
                     "claimed physical slot intersects revoked underlying range", receipt.id, {},
                     range_blocker->notice_id});
        }

        if (dead) availability = Availability::Dead;
        else if (pending) availability = Availability::Pending;
        else availability = Availability::Usable;
        return blockers;
    }

private:
    struct Candidate {
        std::vector<SupportAtom> atoms;
        std::set<Id> receipt_ids;
        std::size_t pending_count = 0;

        bool operator<(const Candidate& other) const {
            if (pending_count != other.pending_count) {
                return pending_count < other.pending_count;
            }
            if (receipt_ids.size() != other.receipt_ids.size()) {
                return receipt_ids.size() < other.receipt_ids.size();
            }
            return receipt_ids < other.receipt_ids;
        }
    };

    const Bank* bank_;
    const RevocationBook* book_;
    EvaluationOptions options_;
    MappingCatalog catalog_;

    Candidate makeCandidate(const std::vector<SupportAtom>& atoms) const {
        Candidate candidate;
        candidate.atoms = atoms;
        for (const SupportAtom& atom : atoms) {
            candidate.receipt_ids.insert(bank_->receipts[atom.claim.receipt_index].id);
            if (atom.availability == Availability::Pending) ++candidate.pending_count;
        }
        return candidate;
    }

    bool assignmentCompatible(const std::vector<SupportAtom>& atoms, std::string& error) const {
        if (atoms.empty()) return true;

        if (options_.require_coherent_unpinned_mapping) {
            Id coherent_mapping;
            for (const SupportAtom& atom : atoms) {
                const Id& version = atom.resolution.mapping_version;
                if (version.empty() || atom.resolution.pinned) continue;
                if (coherent_mapping.empty()) coherent_mapping = version;
                else if (coherent_mapping != version) {
                    error = "ticket mixes unpinned logical mapping versions";
                    return false;
                }
            }
        }

        const StateKey& state = atoms.front().state;
        for (std::size_t i = 1; i < atoms.size(); ++i) {
            if (atoms[i].state != state) {
                error = "ticket combines measurements from incompatible proof states/forks";
                return false;
            }
        }

        if (options_.check_physical_contradictions) {
            for (std::size_t i = 0; i < atoms.size(); ++i) {
                const Receipt& left_receipt = bank_->receipts[atoms[i].claim.receipt_index];
                const MeasurementClaim& left = left_receipt.claims[atoms[i].claim.claim_index];
                for (std::size_t j = i + 1; j < atoms.size(); ++j) {
                    const Receipt& right_receipt = bank_->receipts[atoms[j].claim.receipt_index];
                    const MeasurementClaim& right =
                        right_receipt.claims[atoms[j].claim.claim_index];
                    if (rangesOverlap(left.physical, right.physical) &&
                        !left.digest.empty() && !right.digest.empty() &&
                        left.digest != right.digest) {
                        error = "same physical slots have contradictory measurement digests";
                        return false;
                    }
                }
            }
        }
        return true;
    }

    void collectAtomBlockers(const std::vector<std::vector<SupportAtom>>& groups,
                             Decision& decision) const {
        std::set<std::tuple<Id, Id, Id, Id>> seen;
        for (const auto& group : groups) {
            for (const SupportAtom& atom : group) {
                for (const Blocker& blocker : atom.blockers) {
                    auto key = std::make_tuple(blocker.code, blocker.receipt_id,
                                              blocker.requirement_id, blocker.notice_id);
                    if (seen.insert(key).second) decision.blockers.push_back(blocker);
                }
            }
        }
    }

    SupportBundle makeBundle(const AdmissionTicket& ticket,
                             const std::vector<SupportAtom>& atoms,
                             bool complete) const {
        SupportBundle bundle;
        bundle.ticket_id = ticket.id;
        bundle.ticket = ticket;
        bundle.revocation_revision = book_->revision();
        if (!atoms.empty()) bundle.state = atoms.front().state;

        std::set<std::size_t> receipt_indices;
        std::set<Id> mapping_versions;
        std::vector<SlotRange> physical;

        for (const SupportAtom& atom : atoms) {
            receipt_indices.insert(atom.claim.receipt_index);
            const Receipt& receipt = bank_->receipts[atom.claim.receipt_index];
            const MeasurementClaim& claim = receipt.claims[atom.claim.claim_index];
            bundle.measurements.push_back({
                atom.requirement_id,
                atom.requested,
                claim.physical,
                {},
                claim.digest,
                receipt.id,
                claim.id,
                atom.resolution.mapping_version,
            });
            physical.push_back(claim.physical);
            if (!atom.resolution.mapping_version.empty()) {
                mapping_versions.insert(atom.resolution.mapping_version);
            }
            if (!receipt.mapping.version.empty()) mapping_versions.insert(receipt.mapping.version);
        }

        for (std::size_t index : receipt_indices) {
            const Receipt& receipt = bank_->receipts[index];
            bundle.receipt_ids.push_back(receipt.id);
            bundle.receipts.push_back(receipt);
        }
        bundle.physical_union = normalizeRanges(physical);
        bundle.mapping_versions.assign(mapping_versions.begin(), mapping_versions.end());
        bundle.canonical = canonicalBundle(bundle);
        bundle.fingerprint = fnv1a64(bundle.canonical);
        (void)complete;
        return bundle;
    }
};

struct BundleVerifiers {
    std::function<VerificationState(const Receipt&)> verify_signature;
    std::function<VerificationState(const Receipt&, const MeasurementClaim&)> verify_claim;
};

struct BundleVerificationResult {
    bool valid = false;
    TicketStatus status = TicketStatus::Revoked;
    std::uint64_t book_revision = 0;
    bool revision_current = true;
    std::vector<std::string> errors;
    Decision decision;
};

inline BundleVerificationResult verifySupportBundle(
    const SupportBundle& bundle, const RevocationBook& book,
    const BundleVerifiers& verifiers = {}, EvaluationOptions options = {}) {
    BundleVerificationResult result;
    result.book_revision = book.revision();
    result.revision_current = bundle.revocation_revision == book.revision();

    Bank local;
    local.tickets.push_back(bundle.ticket);
    local.receipts = bundle.receipts;

    std::set<Id> receipt_ids;
    for (const Receipt& receipt : bundle.receipts) {
        if (!receipt_ids.insert(receipt.id).second) {
            result.errors.push_back("duplicate receipt in bundle: " + receipt.id);
        }
        Receipt* mutable_receipt = nullptr;
        for (Receipt& item : local.receipts) {
            if (item.id == receipt.id) mutable_receipt = &item;
        }
        if (!mutable_receipt) continue;
        if (verifiers.verify_signature) {
            mutable_receipt->signature_state = verifiers.verify_signature(*mutable_receipt);
        }
        if (verifiers.verify_claim) {
            for (MeasurementClaim& claim : mutable_receipt->claims) {
                claim.proof_state = verifiers.verify_claim(*mutable_receipt, claim);
            }
        }
    }

    Evaluator evaluator(local, book, options);
    result.decision = evaluator.evaluateTicket(bundle.ticket);
    result.status = result.decision.status;
    if (result.decision.status != TicketStatus::Kept) {
        result.errors.push_back("bundle does not currently establish a kept ticket");
        for (const Blocker& blocker : result.decision.blockers) {
            if (!blocker.message.empty()) result.errors.push_back(blocker.code + ": " + blocker.message);
        }
        for (const std::string& error : result.decision.incompatibilities) {
            result.errors.push_back(error);
        }
    }
    if (result.decision.status == TicketStatus::Kept) {
        std::set<Id> expected(bundle.receipt_ids.begin(), bundle.receipt_ids.end());
        std::set<Id> actual(result.decision.support.receipt_ids.begin(),
                            result.decision.support.receipt_ids.end());
        if (expected != actual) {
            result.errors.push_back("bundle receipt set is not the minimal current witness");
        }
        if (result.decision.support.state != bundle.state) {
            result.errors.push_back("bundle state does not match recomputed state");
        }
    }
    result.valid = result.errors.empty() && result.decision.status == TicketStatus::Kept;
    return result;
}

class RevocationComponent {
public:
    explicit RevocationComponent(EvaluationOptions options = {}) : options_(options) {}

    AdmissionTicket& addTicket(AdmissionTicket ticket,
                               TicketStatus previous_status = TicketStatus::Kept) {
        ticket_index_[ticket.id] = bank_.tickets.size();
        bank_.tickets.push_back(std::move(ticket));
        last_status_[bank_.tickets.back().id] = previous_status;
        return bank_.tickets.back();
    }

    Receipt& addReceipt(Receipt receipt) {
        receipt_index_[receipt.id] = bank_.receipts.size();
        bank_.receipts.push_back(std::move(receipt));
        return bank_.receipts.back();
    }

    LogicalMapping& addMapping(LogicalMapping mapping) {
        bank_.mappings.push_back(std::move(mapping));
        return bank_.mappings.back();
    }

    RevocationBook& revocations() noexcept { return book_; }
    const RevocationBook& revocations() const noexcept { return book_; }
    Bank& bank() noexcept { return bank_; }
    const Bank& bank() const noexcept { return bank_; }

    void setLastStatus(const Id& ticket_id, TicketStatus status) {
        last_status_[ticket_id] = status;
    }

    EvaluationReport evaluate() const {
        return Evaluator(bank_, book_, options_).run();
    }

    TicketDiff prime() {
        EvaluationReport report = evaluate();
        last_status_.clear();
        last_fingerprint_.clear();
        for (const Decision& decision : report.decisions) {
            last_status_[decision.ticket_id] = decision.status;
            if (decision.status == TicketStatus::Kept) {
                last_fingerprint_[decision.ticket_id] = decision.support.fingerprint;
            }
        }
        last_revision_ = report.revision;
        TicketDiff empty;
        empty.from_revision = report.revision;
        empty.to_revision = report.revision;
        return empty;
    }

    TicketDiff reevaluate() {
        EvaluationReport report = evaluate();
        TicketDiff diff;
        diff.from_revision = last_revision_;
        diff.to_revision = report.revision;

        for (const Decision& decision : report.decisions) {
            TicketStatus before = TicketStatus::Kept;
            auto status_found = last_status_.find(decision.ticket_id);
            if (status_found != last_status_.end()) before = status_found->second;

            std::optional<std::uint64_t> old_fingerprint;
            auto fingerprint_found = last_fingerprint_.find(decision.ticket_id);
            if (fingerprint_found != last_fingerprint_.end()) {
                old_fingerprint = fingerprint_found->second;
            }
            std::uint64_t new_fingerprint = decision.status == TicketStatus::Kept
                                                ? decision.support.fingerprint
                                                : 0;
            bool support_changed = old_fingerprint && *old_fingerprint != new_fingerprint &&
                                   before == decision.status;
            bool status_changed = before != decision.status;

            if (status_changed || support_changed) {
                TicketChange change;
                change.ticket_id = decision.ticket_id;
                change.before = before;
                change.after = decision.status;
                change.support_changed = support_changed;
                change.decision = decision;
                diff.changes.push_back(std::move(change));
            }

            if (before == TicketStatus::Kept && decision.status != TicketStatus::Kept) {
                diff.newly_withdrawn.push_back(decision.ticket_id);
            }
            if (decision.status == TicketStatus::Revoked &&
                before != TicketStatus::Revoked) {
                diff.definitively_revoked.push_back(decision.ticket_id);
            }
            if (decision.status == TicketStatus::Pending &&
                before != TicketStatus::Pending) {
                diff.newly_pending.push_back(decision.ticket_id);
            }
            if (decision.status == TicketStatus::Kept &&
                before != TicketStatus::Kept) {
                diff.restored_to_kept.push_back(decision.ticket_id);
            }
            if (support_changed) diff.support_updated.push_back(decision.ticket_id);

            if (decision.status == TicketStatus::Kept) {
                diff.remaining_support.push_back(decision.support);
            }

            last_status_[decision.ticket_id] = decision.status;
            if (decision.status == TicketStatus::Kept) {
                last_fingerprint_[decision.ticket_id] = decision.support.fingerprint;
            } else {
                last_fingerprint_[decision.ticket_id] = 0;
            }
        }
        last_revision_ = report.revision;
        return diff;
    }

private:
    EvaluationOptions options_;
    Bank bank_;
    RevocationBook book_;
    std::map<Id, std::size_t> ticket_index_;
    std::map<Id, std::size_t> receipt_index_;
    std::map<Id, TicketStatus> last_status_;
    std::map<Id, std::uint64_t> last_fingerprint_;
    std::uint64_t last_revision_ = 0;
};

using CertificateRevocationComponent = RevocationComponent;

} // namespace certrev

#endif
