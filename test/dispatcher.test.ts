import assert from "node:assert/strict";
import test from "node:test";

import {
  BatchDispatcher,
  type BatchDispatchResult,
  type CallbackHandle,
} from "../index.ts";

function nestArray(levels: number, leaf: unknown = 0): unknown {
  let value = leaf;
  for (let index = 0; index < levels; index += 1) {
    value = [value];
  }
  return value;
}

function assertFailedWith(
  result: BatchDispatchResult,
  index: number,
  code: string,
): void {
  const call = result.results[index];
  assert.ok(call, `result at index ${index} should exist`);
  assert.equal(call.ok, false);
  if (!call.ok) {
    assert.equal(call.error.code, code);
  }
}

test("rejects the whole batch when the three arrays differ in length", () => {
  let handlerCalls = 0;
  const dispatcher = new BatchDispatcher();
  dispatcher.registerModule(7, {
    0: {
      params: ["string"],
      handler: (params) => {
        handlerCalls += 1;
        return params[0];
      },
    },
  });

  const result = dispatcher.dispatchBatch([7, 7], [0, 0], [["one"]]);

  assert.equal(result.accepted, false);
  assert.equal(result.ok, false);
  assert.equal(result.batchError?.code, "LENGTH_MISMATCH");
  assert.equal(result.results.length, 0);
  assert.equal(handlerCalls, 0);
  assert.equal(
    result.securityEvents.some((event) => event.code === "LENGTH_MISMATCH"),
    true,
  );
});

test("rejects negative and unregistered ids per call and keeps going", () => {
  let handlerCalls = 0;
  const dispatcher = new BatchDispatcher();
  dispatcher.registerModule(7, {
    0: {
      params: ["string"],
      handler: (params) => {
        handlerCalls += 1;
        return params[0];
      },
    },
  });

  const result = dispatcher.dispatchBatch(
    [7, -1, 99, 7, 7],
    [0, 0, 0, -2, 0],
    [["first"], ["negative-module"], ["out-of-range"], ["negative-method"], ["last"]],
  );

  assert.equal(result.accepted, true);
  assert.equal(result.ok, false);
  assert.equal(result.results[0].ok, true);
  assertFailedWith(result, 1, "INVALID_MODULE_ID");
  assertFailedWith(result, 2, "UNKNOWN_MODULE_ID");
  assertFailedWith(result, 3, "INVALID_METHOD_ID");
  assert.equal(result.results[4].ok, true);
  assert.equal(handlerCalls, 2);
});

test("checks argument count and declared scalar/array/map types", () => {
  let handlerCalls = 0;
  const dispatcher = new BatchDispatcher();
  dispatcher.registerModule(11, {
    0: {
      params: [
        "string",
        "number",
        "boolean",
        { type: "array", items: "number" },
        { type: "map", values: "string" },
      ],
      handler: () => {
        handlerCalls += 1;
        return "ok";
      },
    },
  });

  const valid = dispatcher.dispatchBatch(
    [11],
    [0],
    [["text", 1.5, true, [1, 2], { key: "value" }]],
  );
  assert.equal(valid.ok, true);
  assert.equal(handlerCalls, 1);

  const invalid = dispatcher.dispatchBatch(
    [11, 11, 11],
    [0, 0, 0],
    [
      ["missing"],
      ["text", "not-a-number", true, [1, 2], { key: "value" }],
      ["text", 1, true, [1, "two"], { key: "value" }],
    ],
  );
  assertFailedWith(invalid, 0, "ARG_COUNT_MISMATCH");
  assertFailedWith(invalid, 1, "ARG_TYPE_MISMATCH");
  assertFailedWith(invalid, 2, "ARG_TYPE_MISMATCH");
  assert.equal(handlerCalls, 1);
});

test("enforces a nesting depth limit of eight for arrays and maps", () => {
  const dispatcher = new BatchDispatcher({ maxDepth: 8 });
  let handlerCalls = 0;
  dispatcher.registerModule(12, {
    0: {
      params: ["array"],
      handler: () => {
        handlerCalls += 1;
        return "ok";
      },
    },
    1: {
      params: ["map"],
      handler: () => {
        handlerCalls += 1;
        return "ok";
      },
    },
  });

  const atLimit = dispatcher.dispatchBatch(
    [12, 12],
    [0, 1],
    [[nestArray(8)], [{ a: { b: { c: { d: { e: { f: { g: { h: 1 } } } } } } } }]],
  );
  assert.equal(atLimit.ok, true);
  assert.equal(handlerCalls, 2);

  const overLimit = dispatcher.dispatchBatch(
    [12, 12],
    [0, 1],
    [
      [nestArray(9)],
      [{ a: { b: { c: { d: { e: { f: { g: { h: { i: 1 } } } } } } } } }],
    ],
  );
  assertFailedWith(overLimit, 0, "ARG_DEPTH_EXCEEDED");
  assertFailedWith(overLimit, 1, "ARG_DEPTH_EXCEEDED");
  assert.equal(handlerCalls, 2);
});

test("issues integer callback ids, triggers once, and records replays", () => {
  let sinkCalls = 0;
  let received: unknown[] = [];
  const dispatcher = new BatchDispatcher({
    callbackSink: (_callbackId, args) => {
      sinkCalls += 1;
      received = [...args];
    },
  });
  dispatcher.registerModule(20, {
    0: {
      params: ["string", "callback"],
      handler: (params, context) => {
        const callback = params[1] as CallbackHandle;
        const first = callback("result");
        const second = callback("replayed");
        return { nativeCallback: context.nativeParams[1], first, second };
      },
    },
  });

  const result = dispatcher.dispatchBatch([20], [0], [["request", null]]);
  assert.equal(result.ok, true);

  const call = result.results[0];
  assert.equal(call.ok, true);
  if (!call.ok) {
    return;
  }
  const callbackId = call.callbackIds[0];
  assert.equal(Number.isSafeInteger(callbackId), true);
  assert.equal(callbackId >= 0, true);
  assert.deepEqual(call.value, {
    nativeCallback: callbackId,
    first: { ok: true, callbackId, invocationCount: 1 },
    second: {
      ok: false,
      callbackId,
      code: "CALLBACK_REPLAY",
      message: `Callback ${callbackId} was already invoked; replay ignored`,
    },
  });
  assert.equal(sinkCalls, 1);
  assert.deepEqual(received, ["result"]);
  assert.equal(
    result.securityEvents.filter((event) => event.code === "CALLBACK_REPLAY").length,
    1,
  );
  assert.equal(
    result.callbackEvents.filter((event) => !event.accepted).length,
    1,
  );

  const externalReplay = dispatcher.invokeCallback(callbackId, "again");
  assert.equal(externalReplay.ok, false);
  if (!externalReplay.ok) {
    assert.equal(externalReplay.code, "CALLBACK_REPLAY");
  }
  assert.equal(sinkCalls, 1);
  assert.equal(
    dispatcher.getSecurityEvents().some(
      (event) =>
        event.code === "CALLBACK_REPLAY" && event.callbackId === callbackId,
    ),
    true,
  );
});

test("rejects replayed callback argument tokens without stopping the batch", () => {
  let handlerCalls = 0;
  const callback = () => undefined;
  const dispatcher = new BatchDispatcher();
  dispatcher.registerModule(21, {
    0: {
      params: ["callback"],
      handler: () => {
        handlerCalls += 1;
        return "ok";
      },
    },
  });

  const result = dispatcher.dispatchBatch(
    [21, 21, 21, 21],
    [0, 0, 0, 0],
    [[callback], [callback], [100], [100]],
  );

  assert.equal(result.results[0].ok, true);
  assertFailedWith(result, 1, "CALLBACK_TOKEN_REPLAY");
  assert.equal(result.results[2].ok, true);
  assertFailedWith(result, 3, "CALLBACK_TOKEN_REPLAY");
  assert.equal(handlerCalls, 2);
});

test("a handler failure does not block later valid calls", () => {
  let handlerCalls = 0;
  const dispatcher = new BatchDispatcher();
  dispatcher.registerModule(30, {
    0: {
      params: ["number"],
      handler: (params) => {
        handlerCalls += 1;
        if (params[0] === 13) {
          throw new Error("boom");
        }
        return params[0];
      },
    },
  });

  const result = dispatcher.dispatchBatch(
    [30, 30],
    [0, 0],
    [[13], [42]],
  );

  assertFailedWith(result, 0, "HANDLER_ERROR");
  assert.equal(result.results[1].ok, true);
  assert.equal(handlerCalls, 2);
});

test("rejects cycles and invalid callback argument values safely", () => {
  const dispatcher = new BatchDispatcher();
  dispatcher.registerModule(31, {
    0: {
      params: ["map"],
      handler: () => "ok",
    },
  });
  const cyclic: Record<string, unknown> = {};
  cyclic.self = cyclic;

  const result = dispatcher.dispatchBatch([31], [0], [[cyclic]]);
  assertFailedWith(result, 0, "CYCLIC_VALUE");

  const badCallback = dispatcher.dispatchBatch([31], [0], [[{ cb: () => undefined }]]);
  assertFailedWith(badCallback, 0, "ARG_TYPE_MISMATCH");
});

test("isolates exceptions raised while inspecting a malicious parameter", () => {
  let handlerCalls = 0;
  const dispatcher = new BatchDispatcher();
  dispatcher.registerModule(32, {
    0: {
      params: ["map"],
      handler: () => "ok",
    },
    1: {
      params: ["string"],
      handler: (params) => {
        handlerCalls += 1;
        return params[0];
      },
    },
  });

  const malicious: Record<string, unknown> = {};
  Object.defineProperty(malicious, "explode", {
    enumerable: true,
    get() {
      throw new Error("getter exploded");
    },
  });

  const result = dispatcher.dispatchBatch(
    [32, 32],
    [0, 1],
    [[malicious], ["still-works"]],
  );

  assertFailedWith(result, 0, "CALL_INTERNAL_ERROR");
  assert.equal(result.results[1].ok, true);
  assert.equal(handlerCalls, 1);
});
