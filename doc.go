// Package netsec implements admission control for SOME/IP Service Discovery
// (SOME/IP-SD) messages received by an in-vehicle gateway.
//
// It mitigates two common SD attacks:
//
//   - Offer spoofing / service squatting: an attacker advertises a service
//     tuple it does not own to hijack clients, or sends a forged StopOffer
//     (OfferService entry with TTL 0) to take a legitimate provider offline.
//   - Reflected subscriptions: an attacker places a victim's address in the
//     endpoint option of a Subscribe entry so that published event traffic is
//     reflected at the victim.
//
// The gateway's SD handler first calls ParseMessage on the received datagram
// and then Controller.Admit (or the combined Controller.AdmitPacket) with the
// transport source address. One EntryVerdict is returned per entry:
//
//   - VerdictAccept: the entry may enter the SD state machine.
//   - VerdictReject: a well-formed entry violates policy (spoofed Offer or
//     StopOffer, endpoint address differing from the source address).
//   - VerdictDrop: a malformed entry, currently only an option index outside
//     the options array; such entries are discarded without state changes.
//
// Policy summary:
//
//   - An Offer is accepted only when its (ServiceID, InstanceID, MajorVersion)
//     tuple has been provisioned through RegisterProvider and the packet
//     source is one of the registered provider addresses. The endpoint option
//     carried by the Offer must likewise name the source address.
//   - A StopOffer (TTL 0) is only accepted from the address whose Offer
//     currently owns the tuple.
//   - A Subscribe is accepted only when every unicast endpoint option in its
//     first option run names the packet source address. Multicast options are
//     exempt because they legitimately carry a group address.
//   - Any entry whose option index/count references a non-existent option is
//     dropped before any other check.
package netsec
