#pragma once

#include <array>
#include <cstddef>
#include <cstdint>
#include <optional>
#include <vector>

namespace netsec {

// BLE IO capabilities as advertised in the pairing feature exchange.
enum class IoCapability {
    DisplayOnly,
    DisplayYesNo,
    KeyboardOnly,
    NoInputNoOutput,
    KeyboardDisplay,
};

// Association models selectable for a pairing session.
enum class PairingMethod {
    JustWorks,         // no MITM protection
    PasskeyEntry,      // authenticated
    NumericComparison, // authenticated (secure connections)
    OutOfBand,         // authenticated if OOB channel is authenticated
};

enum class RejectReason {
    None,
    MitmNotSatisfied, // MITM required but the negotiated model is unauthenticated
    KeySizeTooSmall,  // negotiated key size below the policy minimum
    BondingDowngrade, // bonding required but the peer dropped the bonding flag
    MitmDowngrade,    // MITM required but the peer dropped the MITM authreq flag
    ScDowngrade,      // secure connections required but the peer dropped the SC flag
};

struct AuthReq {
    bool bonding = true;
    bool mitm = false;
    bool secure_connections = true;
};

// One side's pairing features as exchanged over the air.
struct PairingFeatures {
    IoCapability io = IoCapability::NoInputNoOutput;
    AuthReq authreq{};
    uint8_t max_key_size = 16; // valid range 7..16 bytes
};

// Local security policy enforced by the stack.
struct SecurityPolicy {
    bool require_mitm = true;
    bool require_bonding = true;
    bool require_secure_connections = true;
    uint8_t min_key_size = 16;
};

struct PairingDecision {
    bool accepted = false;
    RejectReason reason = RejectReason::None;
    PairingMethod method = PairingMethod::JustWorks;
    uint8_t key_size = 0;
};

class PairingPolicy {
public:
    explicit PairingPolicy(SecurityPolicy policy);

    // Evaluates a pairing feature exchange against the policy. Any downgrade
    // attempt (smaller key, dropped MITM/bonding/SC flags, unauthenticated
    // association model) is rejected.
    PairingDecision evaluate(const PairingFeatures& local,
                             const PairingFeatures& remote) const;

    // Association model selection from both sides' IO capabilities.
    static PairingMethod select_method(IoCapability initiator,
                                       IoCapability responder);

    const SecurityPolicy& policy() const { return policy_; }

private:
    SecurityPolicy policy_;
};

using Address = std::array<uint8_t, 6>;
using Key = std::array<uint8_t, 16>;

// A bond binds the peer's address to its identity key (IRK) and LTK.
struct BondRecord {
    Address address{};
    Key irk{};
    Key ltk{};
    uint8_t key_size = 0;
    bool mitm_protected = false;
    uint64_t generation = 0;
    bool active = true;
};

class BondStore {
public:
    // Stores a new bond. Any previous bond for the same address is
    // invalidated, so re-pairing supersedes old keys.
    const BondRecord& store(Address address, Key irk, Key ltk,
                            uint8_t key_size, bool mitm_protected);

    std::optional<BondRecord> find_active(const Address& address) const;

    // A bond is only honored when address, identity key and LTK all match.
    bool verify(const Address& address, const Key& irk, const Key& ltk) const;

    void invalidate(const Address& address);
    std::size_t active_count() const;

private:
    std::vector<BondRecord> records_;
    uint64_t next_generation_ = 1;
};

// Drives one pairing session: feature exchange evaluation followed by bond
// creation. The LTK is supplied by the stack's own key generator and is only
// persisted when the policy accepts the session, so a rejected (downgraded)
// session never distributes the long term key.
class PairingSession {
public:
    PairingSession(const PairingPolicy& policy, BondStore& bonds);

    PairingDecision pair(const PairingFeatures& local,
                         const PairingFeatures& remote,
                         const Address& peer_address, const Key& peer_irk,
                         const Key& ltk);

private:
    const PairingPolicy& policy_;
    BondStore& bonds_;
};

} // namespace netsec
