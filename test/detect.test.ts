import { test } from "node:test";
import assert from "node:assert/strict";
import { detectSdpRewrite } from "../index.ts";
import type { ViolationCode } from "../index.ts";

const ORIGINAL = [
  "v=0",
  "o=- 4611733055438367294 2 IN IP4 127.0.0.1",
  "s=-",
  "t=0 0",
  "a=group:BUNDLE 0 1",
  "a=ice-ufrag:abcd",
  "a=ice-pwd:0123456789abcdefghijkl",
  "a=fingerprint:sha-256 11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF:00",
  "m=audio 9 UDP/TLS/RTP/SAVPF 111 0 8",
  "c=IN IP4 0.0.0.0",
  "a=mid:0",
  "a=rtpmap:111 opus/48000/2",
  "a=candidate:1 1 udp 2113937151 10.0.0.5 54400 typ host",
  "m=video 9 UDP/TLS/RTP/SAVPF 96 97 98",
  "c=IN IP4 0.0.0.0",
  "a=mid:1",
  "a=rtpmap:96 VP8/90000",
  "",
].join("\r\n");

const REGISTERED = { registeredNetworks: ["10.0.0.0/8", "192.168.0.0/16"] };

function codes(result: { violations: Array<{ code: ViolationCode }> }): ViolationCode[] {
  return result.violations.map((violation) => violation.code);
}

function mutate(sdp: string, fn: (lines: string[]) => string[]): string {
  return fn(sdp.split("\r\n")).join("\r\n");
}

test("unmodified SDP passes", () => {
  const result = detectSdpRewrite(ORIGINAL, ORIGINAL, REGISTERED);
  assert.equal(result.ok, true);
  assert.deepEqual(result.violations, []);
});

test("reordering payload types within the same m= line is allowed", () => {
  const received = mutate(ORIGINAL, (lines) =>
    lines.map((line) =>
      line === "m=audio 9 UDP/TLS/RTP/SAVPF 111 0 8"
        ? "m=audio 9 UDP/TLS/RTP/SAVPF 0 8 111"
        : line,
    ),
  );
  const result = detectSdpRewrite(ORIGINAL, received, REGISTERED);
  assert.equal(result.ok, true);
});

test("adding or removing payload types is rejected", () => {
  const received = mutate(ORIGINAL, (lines) =>
    lines.map((line) =>
      line === "m=audio 9 UDP/TLS/RTP/SAVPF 111 0 8"
        ? "m=audio 9 UDP/TLS/RTP/SAVPF 111 0"
        : line,
    ),
  );
  const result = detectSdpRewrite(ORIGINAL, received, REGISTERED);
  assert.equal(result.ok, false);
  assert.ok(codes(result).includes("PAYLOAD_TYPES_CHANGED"));
});

test("changed DTLS fingerprint is rejected", () => {
  const received = ORIGINAL.replace(
    "a=fingerprint:sha-256 11:22",
    "a=fingerprint:sha-256 99:99",
  );
  const result = detectSdpRewrite(ORIGINAL, received, REGISTERED);
  assert.equal(result.ok, false);
  assert.ok(codes(result).includes("FINGERPRINT_CHANGED"));
});

test("changed ice-ufrag and ice-pwd are rejected", () => {
  const received = ORIGINAL.replace("a=ice-ufrag:abcd", "a=ice-ufrag:evil").replace(
    "a=ice-pwd:0123456789abcdefghijkl",
    "a=ice-pwd:xxxxxxxxxxxxxxxxxxxxxx",
  );
  const result = detectSdpRewrite(ORIGINAL, received, REGISTERED);
  assert.equal(result.ok, false);
  assert.ok(codes(result).includes("ICE_UFRAG_CHANGED"));
  assert.ok(codes(result).includes("ICE_PWD_CHANGED"));
});

test("changed mid is rejected", () => {
  const received = ORIGINAL.replace("a=mid:1", "a=mid:9");
  const result = detectSdpRewrite(ORIGINAL, received, REGISTERED);
  assert.equal(result.ok, false);
  assert.ok(codes(result).includes("MID_CHANGED"));
});

test("added media section is rejected", () => {
  const received = mutate(ORIGINAL, (lines) => [
    ...lines,
    "m=application 9 UDP/DTLS/SCTP webrtc-datachannel",
    "a=mid:2",
  ]);
  const result = detectSdpRewrite(ORIGINAL, received, REGISTERED);
  assert.equal(result.ok, false);
  assert.ok(codes(result).includes("MEDIA_SECTION_COUNT_CHANGED"));
});

test("removed media section is rejected", () => {
  const received = mutate(ORIGINAL, (lines) => {
    const start = lines.findIndex((line) => line.startsWith("m=video"));
    return lines.slice(0, start);
  });
  const result = detectSdpRewrite(ORIGINAL, received, REGISTERED);
  assert.equal(result.ok, false);
  assert.ok(codes(result).includes("MEDIA_SECTION_COUNT_CHANGED"));
});

test("new host candidate inside registered networks is allowed", () => {
  const received = mutate(ORIGINAL, (lines) => [
    ...lines,
    "a=candidate:2 1 udp 2113937151 192.168.1.20 54401 typ host",
  ]);
  const result = detectSdpRewrite(ORIGINAL, received, REGISTERED);
  assert.equal(result.ok, true);
});

test("new host candidate in unregistered private network is rejected", () => {
  const received = mutate(ORIGINAL, (lines) => [
    ...lines,
    "a=candidate:2 1 udp 2113937151 172.16.0.9 54401 typ host",
  ]);
  const result = detectSdpRewrite(ORIGINAL, received, REGISTERED);
  assert.equal(result.ok, false);
  assert.ok(codes(result).includes("UNREGISTERED_HOST_CANDIDATE"));
});

test("new host candidate with public attacker address is rejected", () => {
  const received = mutate(ORIGINAL, (lines) => [
    ...lines,
    "a=candidate:2 1 udp 2113937151 203.0.113.66 54401 typ host",
  ]);
  const result = detectSdpRewrite(ORIGINAL, received, REGISTERED);
  assert.equal(result.ok, false);
  assert.ok(codes(result).includes("UNREGISTERED_HOST_CANDIDATE"));
});

test("non-host candidates are not subject to network registration", () => {
  const received = mutate(ORIGINAL, (lines) => [
    ...lines,
    "a=candidate:3 1 udp 1677729535 203.0.113.10 54402 typ srflx raddr 10.0.0.5 rport 54400",
  ]);
  const result = detectSdpRewrite(ORIGINAL, received, REGISTERED);
  assert.equal(result.ok, true);
});
