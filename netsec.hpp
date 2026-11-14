#pragma once

#include <cstdint>
#include <map>
#include <set>
#include <span>
#include <vector>

namespace netsec {

using RouterId = std::uint32_t;
using AreaId = std::uint32_t;
using Ipv4Addr = std::uint32_t;

constexpr std::uint16_t kMaxAge = 3600;                 // RFC 2328 MaxAge (seconds)
constexpr std::uint32_t kInitialSequence = 0x80000001u; // RFC 2328 InitialSequenceNumber
constexpr std::uint32_t kMaxSequence = 0x7fffffffu;     // RFC 2328 MaxSequenceNumber
constexpr std::uint32_t kReservedSequence = 0x80000000u;

enum class LsaType : std::uint8_t {
  kRouter = 1,
  kNetwork = 2,
  kSummaryNet = 3,
  kSummaryAsbr = 4,
  kAsExternal = 5,
};

struct LsaHeader {
  std::uint16_t age = 0;
  LsaType type = LsaType::kRouter;
  RouterId link_state_id = 0;
  RouterId advertising_router = 0;
  std::uint32_t sequence = kInitialSequence;
  std::uint16_t checksum = 0;
  std::uint16_t length = 0;
};

struct RouterLink {
  RouterId neighbor = 0;
  std::uint16_t metric = 1;
};

struct Lsa {
  LsaHeader header;
  std::vector<RouterLink> links; // router-LSA style body
};

// ---------------------------------------------------------------------------
// Wire helpers (RFC 2328 section 12 + Appendix B)
// ---------------------------------------------------------------------------

std::uint16_t fletcher16(std::span<const std::uint8_t> data) noexcept;

// Serializes the LSA in network byte order. `zero_age` mirrors the protocol
// rule that the age field is excluded from the checksum so routers may age
// an LSA without recomputing it.
std::vector<std::uint8_t> serialize_lsa(const Lsa& lsa, bool zero_age,
                                        bool zero_checksum);

// Fills header.length and header.checksum so the LSA passes verification.
void finalize_lsa(Lsa& lsa);

bool verify_lsa_checksum(const Lsa& lsa) noexcept;

// RFC 2328 section 12.1.6: circular signed comparison of sequence numbers.
bool sequence_more_recent(std::uint32_t a, std::uint32_t b) noexcept;
bool sequence_valid(std::uint32_t seq) noexcept;
bool age_valid(std::uint16_t age) noexcept;

// ---------------------------------------------------------------------------
// Virtual link endpoint validation
// ---------------------------------------------------------------------------

struct HelloPacket {
  RouterId router_id = 0;
  AreaId area_id = 0;
  Ipv4Addr source = 0;
};

struct VirtualLinkConfig {
  RouterId local_router_id = 0;
  RouterId peer_router_id = 0; // configured remote endpoint router-id
  AreaId transit_area = 0;     // configured transit (non-backbone) area
  Ipv4Addr peer_source = 0;    // expected source address of the peer
};

enum class HelloVerdict {
  kAccept,
  kBadEndpoint, // router-id does not match the configured peer
  kBadArea,     // hello arrived on the wrong transit area
  kBadSource,   // source address does not match the configured peer
};

HelloVerdict validate_hello(const VirtualLinkConfig& config,
                            const HelloPacket& hello) noexcept;

// ---------------------------------------------------------------------------
// LSA wire validation + link-state database
// ---------------------------------------------------------------------------

enum class LsaVerdict {
  kAccept,
  kBadChecksum,
  kBadAge,
  kBadSequence,
};

LsaVerdict validate_lsa_wire(const Lsa& lsa) noexcept;

struct LsaKey {
  LsaType type = LsaType::kRouter;
  RouterId link_state_id = 0;
  RouterId advertising_router = 0;
  bool operator<(const LsaKey& other) const noexcept;
};

LsaKey key_of(const Lsa& lsa) noexcept;

enum class InstallResult {
  kInstalled, // first copy of this LSA
  kUpdated,   // newer sequence number replaced the stored copy
  kDuplicate, // identical sequence number: dropped
  kRollback,  // older sequence number: dropped
  kRejected,  // failed wire validation (checksum/age/sequence)
};

class LinkStateDatabase {
public:
  InstallResult install(const Lsa& lsa, LsaVerdict* wire_verdict = nullptr);
  const Lsa* lookup(const LsaKey& key) const;
  std::size_t size() const noexcept { return db_.size(); }

  using const_iterator = std::map<LsaKey, Lsa>::const_iterator;
  const_iterator begin() const noexcept { return db_.begin(); }
  const_iterator end() const noexcept { return db_.end(); }

private:
  std::map<LsaKey, Lsa> db_;
};

// ---------------------------------------------------------------------------
// Area scoping + SPF input filtering
// ---------------------------------------------------------------------------

struct AreaPolicy {
  AreaId local_area = 0;
  // AS-external (type 5) LSAs are only trusted from these advertisers.
  std::set<RouterId> trusted_external_advertisers;
};

enum class SpfVerdict {
  kAccept,
  kWrongArea,         // LSA arrived on an area other than the local one
  kUntrustedExternal, // AS-external LSA from a non-trusted advertiser
  kUnsupportedType,   // summary LSAs are not accepted into this SPF
};

// Only intra-area LSAs of the local area and explicitly trusted AS-external
// LSAs may feed the SPF computation.
SpfVerdict spf_filter(const AreaPolicy& policy, AreaId arrival_area,
                      const Lsa& lsa) noexcept;

// Dijkstra over the router LSAs that pass spf_filter. Returns the shortest
// metric to every reachable router-id (excluding `self`).
std::map<RouterId, std::uint32_t> run_spf(const AreaPolicy& policy,
                                          AreaId arrival_area,
                                          const LinkStateDatabase& db,
                                          RouterId self);

} // namespace netsec
