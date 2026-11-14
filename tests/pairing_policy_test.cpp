#include "netsec.hpp"

#include <cassert>
#include <cstdio>

using namespace netsec;

namespace {

Address addr(uint8_t seed) { return {seed, 0x11, 0x22, 0x33, 0x44, 0x55}; }

Key key(uint8_t seed) {
    Key k{};
    for (std::size_t i = 0; i < k.size(); ++i) {
        k[i] = static_cast<uint8_t>(seed + i * 7u);
    }
    return k;
}

PairingFeatures strong_features(IoCapability io) {
    PairingFeatures f;
    f.io = io;
    f.authreq = {/*.bonding=*/true, /*.mitm=*/true,
                 /*.secure_connections=*/true};
    f.max_key_size = 16;
    return f;
}

SecurityPolicy strict_policy() {
    SecurityPolicy p;
    p.require_mitm = true;
    p.require_bonding = true;
    p.require_secure_connections = true;
    p.min_key_size = 16;
    return p;
}

// 1. Downgrade negotiation: the MITM tampers with the feature exchange to
//    shrink the key and strip the MITM/SC flags. Every variant is rejected
//    and no bond (and thus no LTK) is created.
void test_downgrade_negotiation() {
    PairingPolicy policy(strict_policy());
    BondStore bonds;
    PairingSession session(policy, bonds);
    const PairingFeatures local =
        strong_features(IoCapability::KeyboardDisplay);
    const Key ltk = key(0xA0);

    PairingFeatures short_key = strong_features(IoCapability::KeyboardDisplay);
    short_key.max_key_size = 7; // attacker forces minimum entropy
    PairingDecision d =
        session.pair(local, short_key, addr(1), key(0x01), ltk);
    assert(!d.accepted && d.reason == RejectReason::KeySizeTooSmall);

    PairingFeatures no_mitm = strong_features(IoCapability::KeyboardDisplay);
    no_mitm.authreq.mitm = false; // attacker strips the MITM requirement
    d = session.pair(local, no_mitm, addr(1), key(0x01), ltk);
    assert(!d.accepted && d.reason == RejectReason::MitmDowngrade);

    PairingFeatures no_bond = strong_features(IoCapability::KeyboardDisplay);
    no_bond.authreq.bonding = false;
    d = session.pair(local, no_bond, addr(1), key(0x01), ltk);
    assert(!d.accepted && d.reason == RejectReason::BondingDowngrade);

    PairingFeatures no_sc = strong_features(IoCapability::KeyboardDisplay);
    no_sc.authreq.secure_connections = false;
    d = session.pair(local, no_sc, addr(1), key(0x01), ltk);
    assert(!d.accepted && d.reason == RejectReason::ScDowngrade);

    assert(bonds.active_count() == 0); // LTK never leaves the device
    std::puts("ok: downgrade negotiation rejected");
}

// 2. Capability forgery: the MITM claims NoInputNoOutput to force Just
//    Works. With MITM required, Just Works is refused outright.
void test_capability_forgery() {
    PairingPolicy policy(strict_policy());
    BondStore bonds;
    PairingSession session(policy, bonds);
    const PairingFeatures local =
        strong_features(IoCapability::KeyboardDisplay);

    PairingFeatures forged = strong_features(IoCapability::NoInputNoOutput);
    assert(PairingPolicy::select_method(local.io, forged.io) ==
           PairingMethod::JustWorks);
    PairingDecision d =
        session.pair(local, forged, addr(2), key(0x02), key(0xB0));
    assert(!d.accepted && d.reason == RejectReason::MitmNotSatisfied);
    assert(bonds.active_count() == 0);
    std::puts("ok: forged NoInputNoOutput cannot force Just Works");
}

// 3. Key replacement: the MITM substitutes its own LTK/IRK for the victim's
//    address. The bond binds address + identity key + LTK, so the attacker's
//    keys never verify and it cannot impersonate the device.
void test_key_replacement() {
    PairingPolicy policy(strict_policy());
    BondStore bonds;
    PairingSession session(policy, bonds);
    const PairingFeatures local =
        strong_features(IoCapability::DisplayYesNo);
    const PairingFeatures remote =
        strong_features(IoCapability::KeyboardDisplay);

    const Address victim = addr(3);
    const Key victim_irk = key(0x03);
    const Key victim_ltk = key(0xC0);
    PairingDecision d = session.pair(local, remote, victim, victim_irk,
                                     victim_ltk);
    assert(d.accepted);

    const Key attacker_irk = key(0xEE);
    const Key attacker_ltk = key(0xEF);
    assert(!bonds.verify(victim, attacker_irk, attacker_ltk));
    assert(!bonds.verify(victim, victim_irk, attacker_ltk));
    assert(!bonds.verify(victim, attacker_irk, victim_ltk));
    assert(bonds.verify(victim, victim_irk, victim_ltk));
    std::puts("ok: substituted keys fail bond verification");
}

// 4. Legitimate pairing: matching capabilities select an authenticated
//    model, the bond is stored, and reconnects verify.
void test_legitimate_pairing() {
    PairingPolicy policy(strict_policy());
    BondStore bonds;
    PairingSession session(policy, bonds);
    const PairingFeatures local =
        strong_features(IoCapability::DisplayYesNo);
    const PairingFeatures remote =
        strong_features(IoCapability::KeyboardDisplay);

    PairingDecision d =
        session.pair(local, remote, addr(4), key(0x04), key(0xD0));
    assert(d.accepted && d.method == PairingMethod::NumericComparison);
    assert(d.key_size == 16);

    const auto bond = bonds.find_active(addr(4));
    assert(bond.has_value());
    assert(bond->mitm_protected && bond->key_size == 16);
    assert(bond->irk == key(0x04));
    assert(bonds.verify(addr(4), key(0x04), key(0xD0)));
    std::puts("ok: legitimate pairing bonds with MITM protection");
}

// 5. Re-bonding: pairing the same address again invalidates the old bond;
//    the old LTK (potentially observed by an attacker) is useless.
void test_rebonding_invalidates_old_keys() {
    PairingPolicy policy(strict_policy());
    BondStore bonds;
    PairingSession session(policy, bonds);
    const PairingFeatures local =
        strong_features(IoCapability::KeyboardDisplay);
    const PairingFeatures remote =
        strong_features(IoCapability::KeyboardDisplay);

    const Address peer = addr(5);
    const Key irk_v1 = key(0x05);
    const Key ltk_v1 = key(0xE0);
    PairingDecision d = session.pair(local, remote, peer, irk_v1, ltk_v1);
    assert(d.accepted && d.method == PairingMethod::NumericComparison);
    assert(bonds.verify(peer, irk_v1, ltk_v1));

    const Key irk_v2 = key(0x15);
    const Key ltk_v2 = key(0xE1);
    d = session.pair(local, remote, peer, irk_v2, ltk_v2);
    assert(d.accepted);

    assert(!bonds.verify(peer, irk_v1, ltk_v1)); // old bond invalidated
    assert(bonds.verify(peer, irk_v2, ltk_v2));  // new bond active
    assert(bonds.active_count() == 1);
    const auto bond = bonds.find_active(peer);
    assert(bond.has_value() && bond->generation == 2);
    std::puts("ok: re-pairing invalidates the previous bond");
}

} // namespace

int main() {
    test_downgrade_negotiation();
    test_capability_forgery();
    test_key_replacement();
    test_legitimate_pairing();
    test_rebonding_invalidates_old_keys();
    std::puts("all pairing policy tests passed");
    return 0;
}
