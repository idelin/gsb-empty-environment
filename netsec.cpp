#include "netsec.hpp"

#include <algorithm>

namespace netsec {

namespace {

constexpr uint8_t kMinKeySize = 7;
constexpr uint8_t kMaxKeySize = 16;

bool provides_mitm_protection(PairingMethod method) {
    return method != PairingMethod::JustWorks;
}

} // namespace

PairingPolicy::PairingPolicy(SecurityPolicy policy) : policy_(policy) {
    policy_.min_key_size =
        std::clamp(policy_.min_key_size, kMinKeySize, kMaxKeySize);
}

PairingMethod PairingPolicy::select_method(IoCapability initiator,
                                           IoCapability responder) {
    // Just Works is unavoidable when either side cannot display or confirm.
    if (initiator == IoCapability::NoInputNoOutput ||
        responder == IoCapability::NoInputNoOutput) {
        return PairingMethod::JustWorks;
    }

    const auto has_keyboard = [](IoCapability io) {
        return io == IoCapability::KeyboardOnly ||
               io == IoCapability::KeyboardDisplay;
    };
    const auto has_display = [](IoCapability io) {
        return io == IoCapability::DisplayOnly ||
               io == IoCapability::DisplayYesNo ||
               io == IoCapability::KeyboardDisplay;
    };
    const auto has_yesno = [](IoCapability io) {
        return io == IoCapability::DisplayYesNo ||
               io == IoCapability::KeyboardDisplay;
    };

    // Both sides can compare numbers: strongest authenticated model.
    if (has_yesno(initiator) && has_yesno(responder)) {
        return PairingMethod::NumericComparison;
    }
    // One side types a passkey shown on the other side's display.
    if ((has_keyboard(initiator) && has_display(responder)) ||
        (has_display(initiator) && has_keyboard(responder))) {
        return PairingMethod::PasskeyEntry;
    }
    // Two keyboards: both enter the same passkey.
    if (has_keyboard(initiator) && has_keyboard(responder)) {
        return PairingMethod::PasskeyEntry;
    }
    // Display-only on both ends leaves no authenticated channel.
    return PairingMethod::JustWorks;
}

PairingDecision
PairingPolicy::evaluate(const PairingFeatures& local,
                        const PairingFeatures& remote) const {
    PairingDecision decision;

    // Item-by-item downgrade checks: bonding, MITM and SC flags must not be
    // negotiated away, and the negotiated key size must meet the minimum.
    if (policy_.require_bonding &&
        !(local.authreq.bonding && remote.authreq.bonding)) {
        decision.reason = RejectReason::BondingDowngrade;
        return decision;
    }
    if (policy_.require_mitm &&
        !(local.authreq.mitm && remote.authreq.mitm)) {
        decision.reason = RejectReason::MitmDowngrade;
        return decision;
    }
    if (policy_.require_secure_connections &&
        !(local.authreq.secure_connections &&
          remote.authreq.secure_connections)) {
        decision.reason = RejectReason::ScDowngrade;
        return decision;
    }

    const uint8_t negotiated =
        std::min({local.max_key_size, remote.max_key_size, kMaxKeySize});
    if (negotiated < policy_.min_key_size) {
        decision.reason = RejectReason::KeySizeTooSmall;
        return decision;
    }

    const PairingMethod method = select_method(local.io, remote.io);
    if (policy_.require_mitm && !provides_mitm_protection(method)) {
        // MITM required but capabilities only allow Just Works: refuse.
        decision.reason = RejectReason::MitmNotSatisfied;
        return decision;
    }

    decision.accepted = true;
    decision.reason = RejectReason::None;
    decision.method = method;
    decision.key_size = negotiated;
    return decision;
}

const BondRecord& BondStore::store(Address address, Key irk, Key ltk,
                                   uint8_t key_size, bool mitm_protected) {
    // Re-pairing invalidates every previous bond for this address.
    invalidate(address);
    BondRecord record;
    record.address = address;
    record.irk = irk;
    record.ltk = ltk;
    record.key_size = key_size;
    record.mitm_protected = mitm_protected;
    record.generation = next_generation_++;
    record.active = true;
    records_.push_back(record);
    return records_.back();
}

std::optional<BondRecord>
BondStore::find_active(const Address& address) const {
    for (const auto& record : records_) {
        if (record.active && record.address == address) {
            return record;
        }
    }
    return std::nullopt;
}

bool BondStore::verify(const Address& address, const Key& irk,
                       const Key& ltk) const {
    const auto record = find_active(address);
    return record.has_value() && record->irk == irk && record->ltk == ltk;
}

void BondStore::invalidate(const Address& address) {
    for (auto& record : records_) {
        if (record.address == address) {
            record.active = false;
        }
    }
}

std::size_t BondStore::active_count() const {
    std::size_t count = 0;
    for (const auto& record : records_) {
        if (record.active) {
            ++count;
        }
    }
    return count;
}

PairingSession::PairingSession(const PairingPolicy& policy, BondStore& bonds)
    : policy_(policy), bonds_(bonds) {}

PairingDecision PairingSession::pair(const PairingFeatures& local,
                                     const PairingFeatures& remote,
                                     const Address& peer_address,
                                     const Key& peer_irk, const Key& ltk) {
    PairingDecision decision = policy_.evaluate(local, remote);
    if (!decision.accepted) {
        // Rejected sessions never distribute or persist the LTK.
        return decision;
    }
    bonds_.store(peer_address, peer_irk, ltk, decision.key_size,
                 decision.method != PairingMethod::JustWorks);
    return decision;
}

} // namespace netsec
