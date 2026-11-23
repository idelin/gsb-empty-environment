#pragma once

#include "gsb/reassembly.hpp"

#include <cstdint>
#include <iostream>
#include <map>
#include <optional>
#include <set>
#include <stdexcept>
#include <vector>

namespace testutil {

inline int& failures() {
    static int value = 0;
    return value;
}

#define CHECK(condition)                                            \
    do {                                                            \
        if (!(condition)) {                                         \
            ++testutil::failures();                                 \
            std::cerr << "CHECK failed: " #condition " ("           \
                      << __FILE__ << ":" << __LINE__ << ")\n";      \
        }                                                           \
    } while (false)

inline gsb::Digest digest(std::string text) {
    return gsb::digest_from_string(text);
}

inline gsb::FragmentRecord record(
    std::uint64_t id,
    std::uint64_t version,
    std::uint64_t sender,
    std::uint64_t begin,
    std::uint64_t end,
    gsb::Digest digest,
    std::uint64_t authority_begin,
    std::uint64_t authority_end) {
    return gsb::FragmentRecord{
        gsb::RecordId{id},
        gsb::Version{version},
        gsb::SenderId{sender},
        gsb::ByteRange{begin, end},
        digest,
        gsb::ByteRange{authority_begin, authority_end},
    };
}

inline gsb::TrustSnapshot two_leaf_snapshot() {
    gsb::TrustSnapshot snapshot;
    snapshot.generation = 1;
    snapshot.leaves = {
        gsb::ManifestLeaf{gsb::ByteRange{0, 100}, 2},
        gsb::ManifestLeaf{gsb::ByteRange{100, 200}, 2},
    };
    snapshot.key_of_sender = {
        {1, 101}, {2, 102}, {3, 103}, {4, 104}, {5, 105}, {6, 106},
    };
    return snapshot;
}

inline bool has_rejection(
    const gsb::QueryReport& report,
    gsb::RecordId id,
    gsb::RejectReason reason) {
    for (const gsb::RejectionEvidence& evidence : report.rejected) {
        if (evidence.record == id && evidence.reason == reason) {
            return true;
        }
    }
    return false;
}

inline bool has_conflict(
    const gsb::QueryReport& report,
    gsb::RecordId a,
    gsb::RecordId b,
    bool same_version) {
    for (const gsb::ConflictEvidence& conflict : report.conflicts) {
        bool pair = (conflict.a == a && conflict.b == b) ||
                    (conflict.a == b && conflict.b == a);
        if (pair && conflict.same_version == same_version) {
            return true;
        }
    }
    return false;
}

inline gsb::Digest root_of(
    const gsb::TrustSnapshot& snapshot,
    gsb::Version version,
    const std::vector<gsb::FragmentRecord>& records) {
    std::vector<const gsb::FragmentRecord*> pointers;
    for (const gsb::FragmentRecord& record : records) {
        pointers.push_back(&record);
    }
    return gsb::compute_object_root(snapshot, version, pointers);
}

struct OracleChoice {
    std::size_t count = 0;
    std::uint64_t bytes = 0;
    std::vector<std::uint64_t> ids;
    std::uint64_t version = 0;
};

// The trusted root is a property of the full object version, not of a chosen
// subset. The oracle therefore precomputes each version root once from every
// fragment of that version, mirroring the engine semantics.
inline std::map<std::uint64_t, gsb::Digest> version_roots(
    const gsb::TrustSnapshot& snapshot,
    const std::vector<gsb::FragmentRecord>& records) {
    std::map<std::uint64_t, std::vector<gsb::FragmentRecord>> by_version;
    for (const gsb::FragmentRecord& record : records) {
        by_version[record.version.value].push_back(record);
    }
    std::map<std::uint64_t, gsb::Digest> roots;
    for (auto& [version, version_records] : by_version) {
        try {
            roots.emplace(
                version,
                root_of(snapshot, gsb::Version{version}, version_records));
        } catch (const std::invalid_argument&) {
            // The version does not cover every manifest leaf cell.
        }
    }
    return roots;
}

// Independent brute-force oracle over every non-empty record subset.
inline std::optional<OracleChoice> oracle(
    const gsb::TrustSnapshot& snapshot,
    const std::vector<gsb::FragmentRecord>& records) {
    std::optional<OracleChoice> best;
    std::size_t total = records.size();
    std::map<std::uint64_t, gsb::Digest> trusted_version_roots =
        version_roots(snapshot, records);

    for (std::uint32_t mask = 1;
         mask < (std::uint32_t{1} << total); ++mask) {
        std::vector<const gsb::FragmentRecord*> chosen;
        for (std::size_t i = 0; i < total; ++i) {
            if (mask & (std::uint32_t{1} << i)) {
                chosen.push_back(&records[i]);
            }
        }

        std::optional<std::uint64_t> common_version;
        bool valid = true;
        for (const gsb::FragmentRecord* fragment : chosen) {
            if (!common_version.has_value()) {
                common_version = fragment->version.value;
            } else if (*common_version != fragment->version.value) {
                valid = false;
            }
        }
        if (!valid) {
            continue;
        }

        for (std::size_t i = 0; i < chosen.size() && valid; ++i) {
            for (std::size_t j = i + 1; j < chosen.size(); ++j) {
                if (gsb::detail::hard_conflict_pair(
                        *chosen[i], *chosen[j])) {
                    valid = false;
                    break;
                }
            }
        }
        if (!valid) {
            continue;
        }

        for (const gsb::ManifestLeaf& leaf : snapshot.leaves) {
            std::vector<std::uint64_t> endpoints{
                leaf.span.begin, leaf.span.end};
            for (const gsb::FragmentRecord* fragment : chosen) {
                if (fragment->span.intersects(leaf.span)) {
                    endpoints.push_back(
                        std::max(fragment->span.begin, leaf.span.begin));
                    endpoints.push_back(
                        std::min(fragment->span.end, leaf.span.end));
                }
            }
            std::sort(endpoints.begin(), endpoints.end());
            endpoints.erase(
                std::unique(endpoints.begin(), endpoints.end()),
                endpoints.end());

            for (std::size_t k = 0; k + 1 < endpoints.size(); ++k) {
                gsb::ByteRange cell{endpoints[k], endpoints[k + 1]};
                if (!leaf.span.contains(cell)) {
                    continue;
                }
                std::set<std::uint64_t> keys;
                bool covered = false;
                for (const gsb::FragmentRecord* fragment : chosen) {
                    if (fragment->span.contains(cell)) {
                        covered = true;
                        auto found = snapshot.key_of_sender.find(
                            fragment->sender.value);
                        if (found != snapshot.key_of_sender.end()) {
                            keys.insert(found->second);
                        }
                    }
                }
                if (!covered || keys.size() < leaf.threshold) {
                    valid = false;
                    break;
                }
            }
            if (!valid) {
                break;
            }
        }
        if (!valid) {
            continue;
        }

        auto trusted =
            trusted_version_roots.find(*common_version);
        if (trusted == trusted_version_roots.end() ||
            !(trusted->second == snapshot.trusted_root)) {
            continue;
        }

        OracleChoice candidate;
        candidate.count = chosen.size();
        candidate.version = *common_version;
        std::uint64_t total_bytes = 0;
        for (const gsb::FragmentRecord* fragment : chosen) {
            candidate.ids.push_back(fragment->id.value);
            total_bytes += fragment->span.length();
        }
        std::sort(candidate.ids.begin(), candidate.ids.end());
        candidate.bytes = total_bytes;

        if (!best.has_value() || candidate.count < best->count ||
            (candidate.count == best->count &&
             (candidate.bytes < best->bytes ||
              (candidate.bytes == best->bytes &&
               candidate.ids < best->ids)))) {
            best = candidate;
        }
    }
    return best;
}

}  // namespace testutil
