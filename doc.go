// Package netsec implements the receive-side EAPOL-Key state machine for a
// WPA2 wireless supplicant.
//
// The state machine accepts WPA/RSN EAPOL-Key Message 3 frames and enforces
// three properties that prevent key-reinstallation and replay attacks:
//
//   - a Message 3 MIC is verified before the replay counter is committed or
//     encrypted Key Data is unwrapped;
//   - the committed replay counter must strictly increase, with an equal
//     counter accepted only as an authenticated retransmission of an already
//     installed ANonce;
//   - a PTK is installed at most once per ANonce. Duplicate Message 3 frames
//     are answered with Message 4 without reinstalling keys or resetting the
//     replay counter.
package netsec
