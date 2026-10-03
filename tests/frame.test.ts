import { test } from "node:test";
import assert from "node:assert/strict";
import {
  decodeFrame,
  encodeFrame,
  FLAG_ACK,
  FLAG_PROBE,
  HEADER_LENGTH,
  type Frame,
} from "../src/frame.ts";

test("帧编解码往返：标志位/序号/确认号/窗口/载荷全部保真", () => {
  const frame: Frame = {
    flags: FLAG_ACK | FLAG_PROBE,
    seq: 0xdeadbeef,
    ack: 123456,
    window: 65535,
    payload: Buffer.from("hello sliding window", "utf8"),
  };
  const decoded = decodeFrame(encodeFrame(frame));
  assert.equal(decoded.flags, frame.flags);
  assert.equal(decoded.seq, frame.seq);
  assert.equal(decoded.ack, frame.ack);
  assert.equal(decoded.window, frame.window);
  assert.deepEqual(decoded.payload, frame.payload);
});

test("空载荷 ACK 帧（纯信标）编解码", () => {
  const decoded = decodeFrame(
    encodeFrame({ flags: FLAG_ACK, seq: 0, ack: 4096, window: 0, payload: Buffer.alloc(0) }),
  );
  assert.equal(decoded.window, 0);
  assert.equal(decoded.ack, 4096);
  assert.equal(decoded.payload.length, 0);
});

test("编码后的帧长 = 头长 + 载荷长", () => {
  const payload = Buffer.alloc(1000, 0xab);
  const encoded = encodeFrame({ flags: 0, seq: 0, ack: 0, window: 1, payload });
  assert.equal(encoded.length, HEADER_LENGTH + 1000);
});

test("截断的帧解码时报错", () => {
  assert.throws(() => decodeFrame(Buffer.alloc(5)), /too short/);
});
