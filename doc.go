// Package netsec protects an RTP receive session against source-address
// spoofing, sequence-number jump resynchronization, forged RTCP BYE packets,
// and local SSRC collisions.
//
// Create one SourceBinder per receive session. Feed RTP and RTCP packets from
// the network loop through HandleRTPPacket and HandleRTCPPacket, or use the
// parsed field methods HandleRTP and HandleRTCPBYE. An accepted RTP packet may
// be forwarded to the media path; a rejected packet must be discarded.
package netsec
