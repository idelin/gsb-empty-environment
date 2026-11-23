#pragma once

#include <algorithm>
#include <array>
#include <cstddef>
#include <cstdint>
#include <functional>
#include <limits>
#include <map>
#include <optional>
#include <set>
#include <stdexcept>
#include <string>
#include <string_view>
#include <utility>
#include <vector>

// Gateway object reassembly component.
//
// The engine is stateless: every query is evaluated against the single
// TrustSnapshot supplied by the host. Certificates carry an immutable copy of
// the snapshot they were issued under, so an issued certificate can always be
// re-verified against its original snapshot even after the current snapshot
// revokes a principal or changes a threshold.
namespace gsb {

inline constexpr std::size_t max_candidate_records = 22;
inline constexpr std::size_t digest_size_bytes = 32;

struct RecordId {
    std::uint64_t value = 0;
    friend bool operator==(RecordId lhs, RecordId rhs) {
        return lhs.value == rhs.value;
    }
    friend bool operator<(RecordId lhs, RecordId rhs) {
        return lhs.value < rhs.value;
    }
};

struct Version {
    std::uint64_t value = 0;
    friend bool operator==(Version lhs, Version rhs) {
        return lhs.value == rhs.value;
    }
    friend bool operator<(Version lhs, Version rhs) {
        return lhs.value < rhs.value;
    }
};

struct SenderId {
    std::uint64_t value = 0;
    friend bool operator==(SenderId lhs, SenderId rhs) {
        return lhs.value == rhs.value;
    }
    friend bool operator<(SenderId lhs, SenderId rhs) {
        return lhs.value < rhs.value;
    }
};

// Opaque public-key material. Aliases whose key material is identical always
// resolve to the same KeyMaterial and therefore count as a single vote.
struct KeyMaterial {
    std::uint64_t value = 0;
    friend bool operator==(KeyMaterial lhs, KeyMaterial rhs) {
        return lhs.value == rhs.value;
    }
    friend bool operator<(KeyMaterial lhs, KeyMaterial rhs) {
        return lhs.value < rhs.value;
    }
};

// Half-open sparse 64-bit interval [begin, end).
struct ByteRange {
    std::uint64_t begin = 0;
    std::uint64_t end = 0;

    bool valid() const { return begin < end; }

    std::uint64_t length() const {
        return valid() ? end - begin : std::uint64_t{0};
    }

    bool contains(const ByteRange& other) const {
        return other.valid() && begin <= other.begin && other.end <= end;
    }

    bool intersects(const ByteRange& other) const {
        return valid() && other.valid() &&
               begin < other.end && other.begin < end;
    }

    ByteRange intersection(const ByteRange& other) const {
        if (!intersects(other)) {
            return {};
        }
        return {std::max(begin, other.begin), std::min(end, other.end)};
    }
};

struct Digest {
    std::array<std::uint8_t, digest_size_bytes> bytes{};

    friend bool operator==(const Digest& lhs, const Digest& rhs) {
        return lhs.bytes == rhs.bytes;
    }
    friend bool operator<(const Digest& lhs, const Digest& rhs) {
        return lhs.bytes < rhs.bytes;
    }
};

// Immutable fragment record presented to the engine. The span is the half-open
// byte interval covered by the fragment; authority is the interval the sender
// is allowed to write, which may only partially cover a manifest leaf.
struct FragmentRecord {
    RecordId id;
    Version version;
    SenderId sender;
    ByteRange span;
    Digest digest;
    ByteRange authority;
};

struct ManifestLeaf {
    ByteRange span;
    std::uint32_t threshold = 0;
};

// Immutable trust snapshot. A revocation or threshold change produces a new
// snapshot with a new generation; old snapshots remain embedded in certificates
// that were already issued.
struct TrustSnapshot {
    std::uint64_t generation = 0;
    std::vector<ManifestLeaf> leaves;
    Digest trusted_root;

    // Host-maintained tables used by the default hooks:
    // sender -> revocation timestamp (0 means not revoked).
    std::map<std::uint64_t, std::uint64_t> revocation_times;
    // sender -> public-key material (0 means unknown).
    std::map<std::uint64_t, std::uint64_t> key_of_sender;
};

// Host-injected primitives. Defaults read the tables embedded in the snapshot;
// a real gateway supplies its own implementations.
struct HostHooks {
    // Returns true when the fragment signature is valid for its sender.
    std::function<bool(const FragmentRecord&, const TrustSnapshot&)>
        verify_signature;
    // Returns the revocation timestamp of the sender (0 = not revoked).
    std::function<std::uint64_t(SenderId, const TrustSnapshot&)>
        revocation_time;
    // Resolves a sender to its public-key material (KeyMaterial{0} = unknown).
    std::function<KeyMaterial(SenderId, const TrustSnapshot&)> key_material;
};

HostHooks default_hooks();

enum class RejectReason {
    ok,
    malformed_span,
    authority_violation,
    outside_manifest,
    bad_signature,
    revoked_sender,
    unknown_key_material,
    root_mismatch,
    not_covered,
};

struct RejectionEvidence {
    RecordId record;
    RejectReason reason = RejectReason::ok;
    std::uint64_t revocation_timestamp = 0;
};

// A retained inconsistency between two signed fragments. Nothing is discarded:
// first-come-first-wins is never used.
struct ConflictEvidence {
    RecordId a;
    RecordId b;
    ByteRange overlap;
    // true: same object version but incompatible digests (hard content
    // conflict). false: different versions endorsing the same byte region.
    bool same_version = true;
};

struct CellDeficit {
    ByteRange cell;
    std::uint32_t required = 0;
    std::uint32_t actual = 0;
};

struct Certificate {
    TrustSnapshot snapshot;
    Version version;
    std::vector<FragmentRecord> records;
    std::uint64_t total_referenced_bytes = 0;
    Digest content_root;
    std::vector<ConflictEvidence> conflicts;
};

struct QueryReport {
    bool deliverable = false;
    std::optional<Certificate> certificate;
    std::vector<RejectionEvidence> rejected;
    std::vector<ConflictEvidence> conflicts;
    std::vector<CellDeficit> deficits;
    std::vector<Version> root_mismatch_versions;
};

struct CertificateVerification {
    bool valid = false;
    RejectReason reason = RejectReason::ok;
    std::string detail;
};

// Deterministic dependency-free digest helper (FNV-1a style, expanded to the
// 32-byte Digest shape). Production deployments may ignore it and supply their
// own trusted-root material.
Digest digest_from_bytes(const std::uint8_t* data, std::size_t size);
Digest digest_from_string(std::string_view text);

// Recomputes the content root of an object version from fragments that fully
// cover every manifest leaf. Throws std::invalid_argument if any leaf cell is
// left uncovered.
Digest compute_object_root(
    const TrustSnapshot& snapshot,
    Version version,
    const std::vector<const FragmentRecord*>& fragments);

class ReassemblyEngine {
public:
    explicit ReassemblyEngine(HostHooks hooks = default_hooks());

    // Evaluates one query against exactly one trust snapshot. At most
    // max_candidate_records may be supplied; duplicate IDs are rejected.
    QueryReport query(
        const TrustSnapshot& snapshot,
        const std::vector<FragmentRecord>& candidates) const;

    // Re-verifies a previously issued certificate against the snapshot embedded
    // in the certificate itself, never against the current snapshot.
    CertificateVerification verify_certificate(
        const Certificate& certificate) const;

private:
    HostHooks hooks_;
};

// ---------------------------------------------------------------------------
// Implementation
// ---------------------------------------------------------------------------

namespace detail {

class HashStream {
public:
    HashStream() {
        for (std::size_t lane = 0; lane < 4; ++lane) {
            state_[lane] =
                fnv_offset_basis_ ^ (0x9E3779B97F4A7C15ULL * (lane + 1));
        }
    }

    void update(const void* data, std::size_t size) {
        const auto* bytes = static_cast<const std::uint8_t*>(data);
        for (std::size_t i = 0; i < size; ++i) {
            for (std::uint64_t& state : state_) {
                state ^= bytes[i];
                state *= fnv_prime_;
            }
        }
    }

    void u64(std::uint64_t value) {
        std::uint8_t raw[8];
        for (std::uint8_t& byte : raw) {
            byte = static_cast<std::uint8_t>(value & 0xFFU);
            value >>= 8U;
        }
        update(raw, sizeof(raw));
    }

    void tag(std::uint8_t value) { update(&value, 1); }

    Digest finish() const {
        Digest result;
        for (std::size_t lane = 0; lane < 4; ++lane) {
            std::uint64_t value = state_[lane];
            for (std::size_t byte_index = 0; byte_index < 8; ++byte_index) {
                result.bytes[lane * 8 + byte_index] =
                    static_cast<std::uint8_t>(value & 0xFFU);
                value >>= 8U;
            }
        }
        return result;
    }

private:
    static constexpr std::uint64_t fnv_offset_basis_ =
        0xCBF29CE484222325ULL;
    static constexpr std::uint64_t fnv_prime_ = 0x100000001B3ULL;
    std::uint64_t state_[4]{};
};

inline std::uint64_t saturating_add(std::uint64_t lhs, std::uint64_t rhs) {
    if (lhs > std::numeric_limits<std::uint64_t>::max() - rhs) {
        return std::numeric_limits<std::uint64_t>::max();
    }
    return lhs + rhs;
}

struct EvaluatedRecord {
    const FragmentRecord* record = nullptr;
    KeyMaterial key;
};

struct Cell {
    ByteRange span;
    const ManifestLeaf* leaf = nullptr;
};

// Sparse endpoint sweep: cells are built only from explicit event endpoints,
// never from the maximum 64-bit address.
inline std::vector<Cell> build_cells(
    const TrustSnapshot& snapshot,
    const std::vector<EvaluatedRecord>& records) {
    std::vector<std::uint64_t> endpoints;
    endpoints.reserve(snapshot.leaves.size() * 2 + records.size() * 2);
    for (const ManifestLeaf& leaf : snapshot.leaves) {
        endpoints.push_back(leaf.span.begin);
        endpoints.push_back(leaf.span.end);
    }
    for (const EvaluatedRecord& evaluated : records) {
        endpoints.push_back(evaluated.record->span.begin);
        endpoints.push_back(evaluated.record->span.end);
    }
    std::sort(endpoints.begin(), endpoints.end());
    endpoints.erase(
        std::unique(endpoints.begin(), endpoints.end()), endpoints.end());

    std::vector<Cell> cells;
    for (std::size_t i = 0; i + 1 < endpoints.size(); ++i) {
        ByteRange span{endpoints[i], endpoints[i + 1]};
        if (!span.valid()) {
            continue;
        }
        const ManifestLeaf* owner = nullptr;
        for (const ManifestLeaf& leaf : snapshot.leaves) {
            if (leaf.span.contains(span)) {
                owner = &leaf;
                break;
            }
        }
        if (owner != nullptr) {
            cells.push_back({span, owner});
        }
    }
    return cells;
}

// Digest claimed for a cell: the tightest (shortest) fragment covering it wins,
// ties broken by record ID. Nested fragments therefore do not corrupt each
// other while both still contribute independent endorsements.
inline Digest cell_digest(
    const std::vector<const EvaluatedRecord*>& covering) {
    const EvaluatedRecord* tightest = covering.front();
    for (const EvaluatedRecord* candidate : covering) {
        const std::uint64_t candidate_length =
            candidate->record->span.length();
        const std::uint64_t tightest_length =
            tightest->record->span.length();
        if (candidate_length < tightest_length ||
            (candidate_length == tightest_length &&
             candidate->record->id < tightest->record->id)) {
            tightest = candidate;
        }
    }
    return tightest->record->digest;
}

inline Digest root_from_cells(
    const TrustSnapshot& snapshot,
    Version version,
    const std::vector<Cell>& cells,
    const std::vector<std::optional<Digest>>& cell_digests) {
    HashStream stream;
    stream.tag('G');
    stream.u64(static_cast<std::uint64_t>(snapshot.leaves.size()));
    for (const ManifestLeaf& leaf : snapshot.leaves) {
        stream.tag('L');
        stream.u64(leaf.span.begin);
        stream.u64(leaf.span.end);
        stream.u64(version.value);
        for (std::size_t i = 0; i < cells.size(); ++i) {
            if (cells[i].leaf != &leaf) {
                continue;
            }
            if (!cell_digests[i].has_value()) {
                throw std::invalid_argument(
                    "compute_object_root: uncovered leaf cell");
            }
            stream.tag('C');
            stream.u64(cells[i].span.begin);
            stream.u64(cells[i].span.end);
            stream.update(cell_digests[i]->bytes.data(), digest_size_bytes);
        }
    }
    return stream.finish();
}

}  // namespace detail

inline HostHooks default_hooks() {
    HostHooks hooks;
    hooks.verify_signature =
        [](const FragmentRecord&, const TrustSnapshot&) { return true; };
    hooks.revocation_time =
        [](SenderId sender, const TrustSnapshot& snapshot) {
            auto found = snapshot.revocation_times.find(sender.value);
            return found == snapshot.revocation_times.end()
                       ? std::uint64_t{0}
                       : found->second;
        };
    hooks.key_material =
        [](SenderId sender, const TrustSnapshot& snapshot) {
            auto found = snapshot.key_of_sender.find(sender.value);
            return found == snapshot.key_of_sender.end()
                       ? KeyMaterial{}
                       : KeyMaterial{found->second};
        };
    return hooks;
}

inline Digest digest_from_bytes(
    const std::uint8_t* data, std::size_t size) {
    detail::HashStream stream;
    stream.tag('D');
    stream.u64(static_cast<std::uint64_t>(size));
    if (data != nullptr && size != 0) {
        stream.update(data, size);
    }
    return stream.finish();
}

inline Digest digest_from_string(std::string_view text) {
    return digest_from_bytes(
        reinterpret_cast<const std::uint8_t*>(text.data()), text.size());
}

inline Digest compute_object_root(
    const TrustSnapshot& snapshot,
    Version version,
    const std::vector<const FragmentRecord*>& fragments) {
    std::vector<detail::EvaluatedRecord> evaluated;
    evaluated.reserve(fragments.size());
    HostHooks hooks = default_hooks();
    for (const FragmentRecord* fragment : fragments) {
        evaluated.push_back(
            {fragment, hooks.key_material(fragment->sender, snapshot)});
    }
    std::vector<detail::Cell> cells = detail::build_cells(snapshot, evaluated);
    std::vector<std::optional<Digest>> cell_digests(cells.size());
    for (std::size_t i = 0; i < cells.size(); ++i) {
        std::vector<const detail::EvaluatedRecord*> covering;
        for (const detail::EvaluatedRecord& item : evaluated) {
            if (item.record->span.contains(cells[i].span)) {
                covering.push_back(&item);
            }
        }
        if (covering.empty()) {
            throw std::invalid_argument(
                "compute_object_root: uncovered leaf cell");
        }
        cell_digests[i] = detail::cell_digest(covering);
    }
    return detail::root_from_cells(snapshot, version, cells, cell_digests);
}

inline ReassemblyEngine::ReassemblyEngine(HostHooks hooks)
    : hooks_(std::move(hooks)) {
    if (!hooks_.verify_signature || !hooks_.revocation_time ||
        !hooks_.key_material) {
        throw std::invalid_argument(
            "ReassemblyEngine: every host hook must be provided");
    }
}

namespace detail {

struct VersionEvaluation {
    std::vector<std::uint32_t> cell_masks;
    bool hard_conflict = false;
    bool feasible = false;
    Digest root;
};

inline bool hard_conflict_pair(
    const FragmentRecord& a, const FragmentRecord& b) {
    return a.version == b.version && a.span.intersects(b.span) &&
           !(a.digest == b.digest) &&
           !(a.span.contains(b.span) || b.span.contains(a.span));
}

// Lexicographic comparison of the ascending record-id tuple represented by a
// bitmask (bit i maps to the i-th eligible record, already ID-sorted).
inline bool mask_tuple_less(std::uint32_t lhs, std::uint32_t rhs) {
    std::uint32_t differing = lhs ^ rhs;
    if (differing == 0) {
        return false;
    }
    std::uint32_t highest_difference =
        std::uint32_t{1}
        << (31 - static_cast<std::uint32_t>(__builtin_clz(differing)));
    return (lhs & highest_difference) == 0;
}

}  // namespace detail

inline QueryReport ReassemblyEngine::query(
    const TrustSnapshot& snapshot,
    const std::vector<FragmentRecord>& candidates) const {
    QueryReport report;

    if (candidates.size() > max_candidate_records) {
        throw std::invalid_argument(
            "ReassemblyEngine: a query accepts at most 22 candidate records");
    }
    for (const ManifestLeaf& leaf : snapshot.leaves) {
        if (!leaf.span.valid() || leaf.threshold == 0) {
            throw std::invalid_argument(
                "ReassemblyEngine: manifest leaves must be valid and positive");
        }
    }
    for (std::size_t i = 1; i < snapshot.leaves.size(); ++i) {
        if (snapshot.leaves[i - 1].span.end >
            snapshot.leaves[i].span.begin) {
            throw std::invalid_argument(
                "ReassemblyEngine: manifest leaves must be ordered and "
                "non-overlapping");
        }
    }

    std::vector<FragmentRecord> ordered = candidates;
    std::sort(ordered.begin(), ordered.end(),
              [](const FragmentRecord& lhs, const FragmentRecord& rhs) {
                  return lhs.id < rhs.id;
              });
    for (std::size_t i = 1; i < ordered.size(); ++i) {
        if (ordered[i].id == ordered[i - 1].id) {
            throw std::invalid_argument(
                "ReassemblyEngine: duplicate fragment record id");
        }
    }

    std::vector<detail::EvaluatedRecord> eligible;
    eligible.reserve(ordered.size());

    for (const FragmentRecord& record : ordered) {
        RejectReason reason = RejectReason::ok;
        std::uint64_t revocation_timestamp = 0;

        if (!record.span.valid()) {
            reason = RejectReason::malformed_span;
        } else if (!record.authority.valid() ||
                   !record.authority.contains(record.span)) {
            reason = RejectReason::authority_violation;
        } else {
            bool inside_manifest = false;
            for (const ManifestLeaf& leaf : snapshot.leaves) {
                if (leaf.span.intersects(record.span)) {
                    inside_manifest = true;
                    break;
                }
            }
            if (!inside_manifest) {
                reason = RejectReason::outside_manifest;
            } else if (!hooks_.verify_signature(record, snapshot)) {
                reason = RejectReason::bad_signature;
            } else {
                revocation_timestamp =
                    hooks_.revocation_time(record.sender, snapshot);
                if (revocation_timestamp != 0) {
                    reason = RejectReason::revoked_sender;
                } else {
                    KeyMaterial key =
                        hooks_.key_material(record.sender, snapshot);
                    if (key.value == 0) {
                        reason = RejectReason::unknown_key_material;
                    } else {
                        eligible.push_back({&record, key});
                    }
                }
            }
        }

        if (reason != RejectReason::ok) {
            report.rejected.push_back(
                {record.id, reason, revocation_timestamp});
        }
    }

    // Retain every signed-fragment inconsistency as evidence. Ordering is
    // deterministic and independent of arrival order.
    for (std::size_t i = 0; i < eligible.size(); ++i) {
        for (std::size_t j = i + 1; j < eligible.size(); ++j) {
            const FragmentRecord& a = *eligible[i].record;
            const FragmentRecord& b = *eligible[j].record;
            ByteRange overlap = a.span.intersection(b.span);
            if (!overlap.valid()) {
                continue;
            }
            if (a.version == b.version) {
                bool nested =
                    a.span.contains(b.span) || b.span.contains(a.span);
                if (!nested && !(a.digest == b.digest)) {
                    report.conflicts.push_back(
                        {a.id, b.id, overlap, true});
                }
            } else {
                // Different versions endorsing the same byte region are
                // retained even when the byte digest is identical.
                report.conflicts.push_back(
                    {a.id, b.id, overlap, false});
            }
        }
    }

    std::vector<detail::Cell> cells =
        detail::build_cells(snapshot, eligible);

    std::set<Version> versions;
    for (const detail::EvaluatedRecord& item : eligible) {
        versions.insert(item.record->version);
    }

    std::map<Version, detail::VersionEvaluation> evaluations;
    for (Version version : versions) {
        detail::VersionEvaluation evaluation;
        evaluation.cell_masks.assign(cells.size(), 0U);
        for (std::size_t cell_index = 0; cell_index < cells.size();
             ++cell_index) {
            std::uint32_t mask = 0;
            for (std::size_t r = 0; r < eligible.size(); ++r) {
                const FragmentRecord& record = *eligible[r].record;
                if (record.version == version &&
                    record.span.contains(cells[cell_index].span)) {
                    mask |= (std::uint32_t{1} << r);
                }
            }
            evaluation.cell_masks[cell_index] = mask;
        }

        for (std::size_t c = 0; c < cells.size(); ++c) {
            std::uint32_t mask = evaluation.cell_masks[c];
            std::set<KeyMaterial> distinct_keys;
            std::uint32_t cursor = mask;
            while (cursor != 0) {
                std::size_t bit = static_cast<std::size_t>(
                    __builtin_ctz(cursor));
                distinct_keys.insert(eligible[bit].key);
                cursor &= cursor - 1U;
            }

            std::uint32_t actual =
                static_cast<std::uint32_t>(distinct_keys.size());
            std::uint32_t required = cells[c].leaf->threshold;
            if (actual < required) {
                report.deficits.push_back(
                    {cells[c].span, required, actual});
            }
        }

        // Same-version partial overlaps with different digests taint the
        // whole version; nested intervals remain compatible. This is a
        // pairwise check because two partially overlapping fragments never
        // jointly contain any single endpoint cell.
        for (std::size_t i = 0; i < eligible.size(); ++i) {
            for (std::size_t j = i + 1; j < eligible.size(); ++j) {
                const FragmentRecord& a = *eligible[i].record;
                const FragmentRecord& b = *eligible[j].record;
                if (a.version == version && b.version == version &&
                    detail::hard_conflict_pair(a, b)) {
                    evaluation.hard_conflict = true;
                }
            }
        }

        bool quorum_everywhere = true;
        for (std::size_t c = 0; c < cells.size(); ++c) {
            std::set<KeyMaterial> distinct_keys;
            std::uint32_t cursor = evaluation.cell_masks[c];
            while (cursor != 0) {
                std::size_t bit =
                    static_cast<std::size_t>(__builtin_ctz(cursor));
                distinct_keys.insert(eligible[bit].key);
                cursor &= cursor - 1U;
            }
            if (static_cast<std::uint32_t>(distinct_keys.size()) <
                cells[c].leaf->threshold) {
                quorum_everywhere = false;
                break;
            }
        }

        if (!evaluation.hard_conflict && quorum_everywhere) {
            std::vector<const FragmentRecord*> version_records;
            for (const detail::EvaluatedRecord& item : eligible) {
                if (item.record->version == version) {
                    version_records.push_back(item.record);
                }
            }
            evaluation.root = compute_object_root(
                snapshot, version, version_records);
            evaluation.feasible =
                evaluation.root == snapshot.trusted_root;
            if (!evaluation.feasible) {
                report.root_mismatch_versions.push_back(version);
            }
        }
        evaluations.emplace(version, std::move(evaluation));
    }

    struct CandidateChoice {
        Version version;
        std::uint32_t mask;
        std::uint64_t total_bytes;
    };
    std::optional<CandidateChoice> best;

    auto accept = [&](Version version, std::uint32_t mask) {
        const detail::VersionEvaluation& evaluation =
            evaluations.find(version)->second;
        for (std::size_t c = 0; c < cells.size(); ++c) {
            std::uint32_t chosen = mask & evaluation.cell_masks[c];
            if (chosen == 0) {
                return;
            }
            std::set<KeyMaterial> distinct_keys;
            std::uint32_t cursor = chosen;
            while (cursor != 0) {
                std::size_t bit =
                    static_cast<std::size_t>(__builtin_ctz(cursor));
                distinct_keys.insert(eligible[bit].key);
                cursor &= cursor - 1U;
            }
            if (static_cast<std::uint32_t>(distinct_keys.size()) <
                cells[c].leaf->threshold) {
                return;
            }
        }

        std::uint64_t candidate_bytes = 0;
        std::uint32_t cursor = mask;
        while (cursor != 0) {
            std::size_t bit =
                static_cast<std::size_t>(__builtin_ctz(cursor));
            candidate_bytes = detail::saturating_add(
                candidate_bytes, eligible[bit].record->span.length());
            cursor &= cursor - 1U;
        }

        CandidateChoice choice{version, mask, candidate_bytes};
        if (!best.has_value() ||
            __builtin_popcount(choice.mask) <
                __builtin_popcount(best->mask) ||
            (__builtin_popcount(choice.mask) ==
                 __builtin_popcount(best->mask) &&
             (choice.total_bytes < best->total_bytes ||
              (choice.total_bytes == best->total_bytes &&
                (detail::mask_tuple_less(choice.mask, best->mask) ||
                (choice.mask == best->mask &&
                 choice.version < best->version)))))) {
            best = choice;
        }
    };

    // Exhaustive enumeration over every subset, ordered by cardinality so the
    // first accepted level contains a minimum-record certificate.
    const std::uint32_t universe =
        eligible.empty()
            ? 0U
            : ((std::uint32_t{1} << eligible.size()) - 1U);
    std::size_t best_count = 0;
    for (std::size_t count = 1; count <= eligible.size(); ++count) {
        if (best_count != 0) {
            break;
        }
        for (std::uint32_t mask = 1; mask <= universe; ++mask) {
            if (static_cast<std::size_t>(__builtin_popcount(mask)) !=
                count) {
                continue;
            }
            for (const auto& [version, evaluation] : evaluations) {
                if (evaluation.feasible) {
                    accept(version, mask);
                }
            }
        }
        if (best.has_value()) {
            best_count = count;
        }
    }

    if (best.has_value()) {
        Certificate certificate;
        certificate.snapshot = snapshot;
        certificate.version = best->version;
        certificate.total_referenced_bytes = best->total_bytes;
        std::uint32_t cursor = best->mask;
        while (cursor != 0) {
            std::size_t bit =
                static_cast<std::size_t>(__builtin_ctz(cursor));
            certificate.records.push_back(*eligible[bit].record);
            cursor &= cursor - 1U;
        }
        std::sort(certificate.records.begin(), certificate.records.end(),
                  [](const FragmentRecord& lhs, const FragmentRecord& rhs) {
                      return lhs.id < rhs.id;
                  });
        for (const ConflictEvidence& conflict : report.conflicts) {
            auto cited = [&](RecordId id) {
                return std::any_of(
                    certificate.records.begin(),
                    certificate.records.end(),
                    [&](const FragmentRecord& record) {
                        return record.id == id;
                    });
            };
            if (cited(conflict.a) && cited(conflict.b)) {
                certificate.conflicts.push_back(conflict);
            }
        }
        certificate.content_root =
            evaluations.find(best->version)->second.root;
        report.certificate = std::move(certificate);
        report.deliverable = true;
    }

    std::sort(report.deficits.begin(), report.deficits.end(),
              [](const CellDeficit& lhs, const CellDeficit& rhs) {
                  return lhs.cell.begin < rhs.cell.begin;
              });
    return report;
}

inline CertificateVerification ReassemblyEngine::verify_certificate(
    const Certificate& certificate) const {
    auto fail = [](RejectReason reason, const std::string& detail) {
        return CertificateVerification{false, reason, detail};
    };

    if (certificate.records.size() > max_candidate_records) {
        return fail(RejectReason::not_covered,
                    "certificate exceeds the candidate record cap");
    }

    std::set<RecordId> ids;
    for (const FragmentRecord& record : certificate.records) {
        if (!ids.insert(record.id).second) {
            return fail(RejectReason::not_covered,
                        "certificate contains duplicate record ids");
        }
    }

    // Re-judge strictly from the snapshot embedded in the certificate. The
    // current snapshot is never consulted here.
    QueryReport replayed =
        query(certificate.snapshot, certificate.records);
    if (!replayed.deliverable || !replayed.certificate.has_value()) {
        return fail(RejectReason::not_covered,
                    "certificate fragments no longer satisfy their snapshot");
    }

    const Certificate& reissued = *replayed.certificate;
    if (!(reissued.version == certificate.version)) {
        return fail(RejectReason::not_covered,
                    "certificate version is not the endorsed version");
    }
    if (reissued.records.size() != certificate.records.size()) {
        return fail(RejectReason::not_covered,
                    "certificate is not a minimal record set");
    }
    for (std::size_t i = 0; i < certificate.records.size(); ++i) {
        if (!(reissued.records[i].id == certificate.records[i].id)) {
            return fail(RejectReason::not_covered,
                        "certificate record set does not match the minimal set");
        }
    }

    if (!(reissued.content_root == certificate.content_root)) {
        return fail(RejectReason::root_mismatch,
                    "embedded content root does not recompute");
    }
    if (!(reissued.content_root == certificate.snapshot.trusted_root)) {
        return fail(RejectReason::root_mismatch,
                    "recomputed root does not match the trusted root");
    }

    std::uint64_t recomputed_bytes = 0;
    for (const FragmentRecord& record : certificate.records) {
        recomputed_bytes = detail::saturating_add(
            recomputed_bytes, record.span.length());
    }
    if (recomputed_bytes != certificate.total_referenced_bytes) {
        return fail(RejectReason::not_covered,
                    "embedded referenced-byte total is incorrect");
    }

    if (reissued.conflicts.size() != certificate.conflicts.size()) {
        return fail(RejectReason::not_covered,
                    "embedded conflict evidence is incomplete");
    }
    for (std::size_t i = 0; i < certificate.conflicts.size(); ++i) {
        const ConflictEvidence& expected = reissued.conflicts[i];
        const ConflictEvidence& actual = certificate.conflicts[i];
        if (!(expected.a == actual.a) || !(expected.b == actual.b) ||
            expected.same_version != actual.same_version) {
            return fail(RejectReason::not_covered,
                        "embedded conflict evidence does not match");
        }
    }

    return CertificateVerification{
        true, RejectReason::ok,
        "certificate valid under snapshot generation " +
            std::to_string(certificate.snapshot.generation)};
}

}  // namespace gsb
