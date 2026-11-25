#ifndef CERTREV_REFERENCE_HPP
#define CERTREV_REFERENCE_HPP

#include "certrev/revocation.hpp"

#include <functional>
#include <set>
#include <stdexcept>

namespace certrev::reference {

struct ExpandedResult {
    TicketStatus status = TicketStatus::Revoked;
    std::vector<Id> witness_receipt_ids;
};

inline bool compatibleAssignment(const std::vector<SupportAtom>& atoms,
                                 const Bank& bank,
                                 const EvaluationOptions& options) {
    if (atoms.empty()) return true;

    if (options.require_coherent_unpinned_mapping) {
        Id coherent;
        for (const SupportAtom& atom : atoms) {
            const Id& version = atom.resolution.mapping_version;
            if (version.empty() || atom.resolution.pinned) continue;
            if (coherent.empty()) coherent = version;
            else if (coherent != version) return false;
        }
    }

    const StateKey& state = atoms.front().state;
    for (const SupportAtom& atom : atoms) {
        if (atom.state != state) return false;
    }

    if (!options.check_physical_contradictions) return true;
    for (std::size_t i = 0; i < atoms.size(); ++i) {
        const Receipt& left_receipt = bank.receipts[atoms[i].claim.receipt_index];
        const MeasurementClaim& left = left_receipt.claims[atoms[i].claim.claim_index];
        for (std::size_t j = i + 1; j < atoms.size(); ++j) {
            const Receipt& right_receipt = bank.receipts[atoms[j].claim.receipt_index];
            const MeasurementClaim& right = right_receipt.claims[atoms[j].claim.claim_index];
            if (rangesOverlap(left.physical, right.physical) &&
                !left.digest.empty() && !right.digest.empty() &&
                left.digest != right.digest) {
                return false;
            }
        }
    }
    return true;
}

inline std::vector<Id> receiptIds(const Bank& bank,
                                  const std::vector<SupportAtom>& atoms) {
    std::set<Id> ids;
    for (const SupportAtom& atom : atoms) {
        ids.insert(bank.receipts[atom.claim.receipt_index].id);
    }
    return {ids.begin(), ids.end()};
}

// Deliberately simple exponential oracle. It expands every receipt subset and
// every measurement/mapping choice. Use it only for small banks in tests or
// host-side differential checks.
inline ExpandedResult fullyExpandedEvaluate(const AdmissionTicket& ticket,
                                            const Bank& bank,
                                            const RevocationBook& book,
                                            EvaluationOptions options = {}) {
    Evaluator services(bank, book, options);
    ExpandedResult result;
    const std::size_t receipt_count = bank.receipts.size();
    if (receipt_count >= 63) {
        throw std::invalid_argument("fullyExpandedEvaluate is intended for fewer than 63 receipts");
    }

    for (std::uint64_t mask = 0; mask < (std::uint64_t{1} << receipt_count); ++mask) {
        std::vector<std::size_t> subset;
        for (std::size_t i = 0; i < receipt_count; ++i) {
            if ((mask >> i) & 1ULL) subset.push_back(i);
        }

        std::vector<std::vector<SupportAtom>> groups;
        for (std::size_t q = 0; q < ticket.requirements.size(); ++q) {
            const Requirement& requirement = ticket.requirements[q];
            Id requirement_id = requirement.id.empty()
                                    ? "requirement-" + std::to_string(q + 1)
                                    : requirement.id;
            std::vector<Blocker> ignored_resolution_blockers;
            std::vector<ResolutionAlternative> alternatives =
                services.resolveRequirement(ticket, requirement, ignored_resolution_blockers);

            std::vector<SupportAtom> group;
            for (const ResolutionAlternative& alternative : alternatives) {
                for (std::size_t receipt_index : subset) {
                    const Receipt& receipt = bank.receipts[receipt_index];
                    if (receipt.generation != ticket.generation) continue;
                    for (std::size_t claim_index = 0;
                         claim_index < receipt.claims.size(); ++claim_index) {
                        const MeasurementClaim& claim = receipt.claims[claim_index];
                        if (claim.physical != alternative.physical) continue;
                        if (!requirement.expected_digest.empty() &&
                            claim.digest != requirement.expected_digest) {
                            continue;
                        }

                        SupportAtom atom;
                        atom.requirement_id = requirement_id;
                        atom.resolution = alternative;
                        atom.claim = {receipt_index, claim_index};
                        atom.state = services.stateKey(receipt);
                        Availability availability;
                        atom.blockers = services.assessAtom(
                            receipt, claim, alternative, ticket.generation, availability);
                        atom.availability = availability;
                        if (availability != Availability::Dead) group.push_back(std::move(atom));
                    }
                }
            }
            groups.push_back(std::move(group));
        }

        bool subset_has_pending_witness = false;
        std::vector<Id> pending_ids;
        std::vector<SupportAtom> current;
        struct KeptFound {};
        const auto enumerate = [&](auto&& self, std::size_t index) -> void {
            if (index == groups.size()) {
                if (!compatibleAssignment(current, bank, options)) return;
                bool pending = false;
                for (const SupportAtom& atom : current) {
                    if (atom.availability == Availability::Pending) pending = true;
                }
                std::vector<Id> ids = receiptIds(bank, current);
                if (!pending) {
                    result.status = TicketStatus::Kept;
                    result.witness_receipt_ids = std::move(ids);
                    throw KeptFound{};
                }
                if (!subset_has_pending_witness) {
                    subset_has_pending_witness = true;
                    pending_ids = std::move(ids);
                }
                return;
            }
            for (const SupportAtom& atom : groups[index]) {
                current.push_back(atom);
                self(self, index + 1);
                current.pop_back();
            }
        };

        try {
            enumerate(enumerate, 0);
        } catch (KeptFound) {
            return result;
        }

        if (subset_has_pending_witness && result.status != TicketStatus::Pending) {
            result.status = TicketStatus::Pending;
            result.witness_receipt_ids = pending_ids;
        }
    }

    return result;
}

} // namespace certrev::reference

#endif
