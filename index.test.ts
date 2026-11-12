import { describe, test } from "node:test";
import assert from "node:assert/strict";

import {
  type BatchResult,
  type ErrorCode,
  ERROR_CODES,
  type ProcedureResult,
  createRouter,
  dispatch,
  dispatchBatch,
  handleFetchRequest,
  handleRequest,
  mutation,
  parseJsonStrict,
  query,
  subscription,
} from "./index.ts";

function assertFailureCode(
  result: ProcedureResult | BatchResult,
  code: ErrorCode,
): void {
  assert.equal(result.ok, false);
  if (result.ok) return;
  if ("error" in result && result.error) {
    assert.equal(result.error.code, code);
    return;
  }
  assert.fail("Expected an error result");
}

describe("procedure lookup", () => {
  test("uses only own procedures and rejects prototype path segments", async () => {
    const inherited = Object.create({
      inherited: query(() => ({ leaked: true })),
    });
    inherited.safe = {
      nested: query(() => ({ ok: true })),
    };

    const app = createRouter(inherited);
    const protectedPath = await app.dispatch({
      path: "__proto__.polluted",
    });
    const ownNested = await app.dispatch({ path: "safe.nested" });

    assertFailureCode(protectedPath, ERROR_CODES.NOT_FOUND);
    assert.equal(ownNested.ok, true);

    for (const path of [
      "__proto__",
      "constructor",
      "prototype",
      "safe.constructor",
      "safe.prototype",
      "%5F%5Fproto%5F%5F",
      "inherited",
      "toString",
    ]) {
      const result = await app.dispatch({ path });
      assertFailureCode(result, ERROR_CODES.NOT_FOUND);
    }
  });

  test("does not expose explicitly registered blocked segments", async () => {
    const definitions: Record<string, unknown> = {};
    for (const key of ["__proto__", "constructor", "prototype"]) {
      Object.defineProperty(definitions, key, {
        configurable: true,
        enumerable: true,
        value: query(() => ({ exposed: true })),
        writable: true,
      });
    }

    const app = createRouter(definitions);
    for (const path of ["__proto__", "constructor", "prototype"]) {
      assertFailureCode(
        await app.dispatch({ path }),
        ERROR_CODES.NOT_FOUND,
      );
    }
  });
});

describe("batch authorization", () => {
  test("authorizes each call independently and does not leak another input", async () => {
    let parsedSecret = false;
    const app = createRouter({
      publicQuery: query(() => ({ visible: true })),
      admin: {
        delete: {
          type: "mutation",
          protected: true,
          input: () => {
            parsedSecret = true;
            return {};
          },
          handler: () => ({ deleted: true }),
        },
      },
    });

    const tasks = app.dispatchBatch([
      { path: "publicQuery" },
      { path: "admin.delete", input: { secret: "TOP-SECRET" } },
      { path: "missing", input: { secret: "OTHER-SECRET" } },
    ]);

    const result = await tasks;
    assert.equal(result.ok, false);
    assert.equal(result.results.length, 3);
    assert.equal(result.results[0]?.ok, true);
    assert.equal(result.results[1]?.ok, false);
    const unauthorized = result.results[1];
    const missing = result.results[2];
    assert.ok(unauthorized && missing);
    assertFailureCode(unauthorized, ERROR_CODES.UNAUTHORIZED);
    assertFailureCode(missing, ERROR_CODES.NOT_FOUND);
    assert.equal(parsedSecret, false);

    const serializedErrors = JSON.stringify(result.results.slice(1));
    assert.equal(serializedErrors.includes("TOP-SECRET"), false);
    assert.equal(serializedErrors.includes("OTHER-SECRET"), false);
  });

  test("one unauthorized call does not stop authorized calls", async () => {
    const app = createRouter(
      {
        protectedQuery: {
          type: "query",
          protected: true,
          handler: ({ ctx }) => ({
            user: (ctx as { user?: string } | undefined)?.user,
          }),
        },
        publicQuery: query(() => "public-result"),
      },
      {
        authorize: ({ ctx }) =>
          (ctx as { user?: string } | undefined)?.user === "alice",
      },
    );

    const result = await app.dispatchBatch(
      [
        { path: "protectedQuery" },
        { path: "publicQuery" },
      ],
      { context: {} },
    );

    const unauthorized = result.results[0];
    assert.ok(unauthorized);
    assertFailureCode(unauthorized, ERROR_CODES.UNAUTHORIZED);
    assert.equal(result.results[1]?.ok, true);
    assert.equal(
      result.results[1]?.ok && result.results[1]?.data,
      "public-result",
    );
  });

  test("rejects batches larger than ten before dispatching", async () => {
    let called = 0;
    const app = createRouter({
      ping: query(() => {
        called += 1;
        return "pong";
      }),
    });

    const calls = Array.from({ length: 11 }, () => ({ path: "ping" }));
    const result = await dispatchBatch(app, calls);

    assertFailureCode(result, ERROR_CODES.BATCH_TOO_LARGE);
    assert.equal(called, 0);
  });
});

describe("HTTP method enforcement", () => {
  test("rejects mutations and subscriptions called with GET", async () => {
    const app = createRouter({
      create: mutation(() => "created"),
      stream: subscription(() => "stream"),
      read: query(() => "read"),
    });

    for (const path of ["create", "stream"]) {
      const result = await dispatch(
        app,
        { path },
        undefined,
        { method: "GET" },
      );
      assertFailureCode(result, ERROR_CODES.METHOD_NOT_SUPPORTED);
    }

    const read = await dispatch(
      app,
      { path: "read" },
      undefined,
      { method: "GET" },
    );
    assert.equal(read.ok, true);
  });

  test("applies method checks independently inside a GET batch", async () => {
    const app = createRouter({
      read: query(() => "read"),
      create: mutation(() => "created"),
    });
    const result = await app.dispatchBatch(
      [{ path: "read" }, { path: "create" }],
      { method: "GET" },
    );

    assert.equal(result.results[0]?.ok, true);
    const mutationResult = result.results[1];
    assert.ok(mutationResult);
    assertFailureCode(mutationResult, ERROR_CODES.METHOD_NOT_SUPPORTED);
  });

  test("handles a Fetch request URL and query input without dependencies", async () => {
    const app = createRouter({
      echo: query(({ input }) => input),
    });
    const response = await handleFetchRequest(
      app,
      new Request(
        "http://localhost/api/trpc/echo?input=%7B%22value%22%3A%22ok%22%7D",
        { method: "GET" },
      ),
    );
    const body = await response.json();

    assert.equal(response.status, 200);
    assert.equal(body.ok, true);
    assert.deepEqual(body.data, { value: "ok" });
  });
});

describe("authorization before input parsing", () => {
  test("does not parse input after an authorization failure", async () => {
    let parserCalls = 0;
    const app = createRouter(
      {
        protectedMutation: {
          type: "mutation",
          protected: true,
          input: (value: unknown) => {
            parserCalls += 1;
            return value;
          },
          handler: () => ({ ok: true }),
        },
      },
      {
        authorize: ({ ctx }) =>
          (ctx as { user?: string } | undefined)?.user === "admin",
      },
    );

    const denied = await app.dispatch(
      {
        path: "protectedMutation",
        inputJson: '{"secret":"DO-NOT-LEAK","secret":"duplicate"}',
      },
      undefined,
      { context: {} },
    );

    assertFailureCode(denied, ERROR_CODES.UNAUTHORIZED);
    assert.equal(parserCalls, 0);
    assert.equal(JSON.stringify(denied).includes("DO-NOT-LEAK"), false);

    const allowed = await app.dispatch(
      { path: "protectedMutation", input: { safe: true } },
      undefined,
      {
        context: { user: "admin" },
      },
    );
    assert.equal(allowed.ok, true);
    assert.equal(parserCalls, 1);
  });

  test("authorizes before parsing a single raw request body", async () => {
    let parserCalls = 0;
    const app = createRouter(
      {
        protectedQuery: {
          type: "query",
          protected: true,
          input: () => {
            parserCalls += 1;
            return "parsed";
          },
          handler: () => "handled",
        },
      },
      { authorize: () => false },
    );

    const result = await handleRequest(app, {
      method: "POST",
      path: "protectedQuery",
      body: '{"secret":"RAW-SECRET","secret":"duplicate"}',
      context: {},
    });

    assertFailureCode(result, ERROR_CODES.UNAUTHORIZED);
    assert.equal(parserCalls, 0);
    assert.equal(JSON.stringify(result).includes("RAW-SECRET"), false);
  });
});

describe("strict JSON", () => {
  test("rejects duplicate keys at any depth", () => {
    assert.throws(
      () => parseJsonStrict('{"a":1,"a":2}'),
      /Duplicate JSON key/,
    );
    assert.throws(
      () => parseJsonStrict('{"outer":{"x":1,"x":2}}'),
      /Duplicate JSON key/,
    );
    assert.throws(
      () => parseJsonStrict('{"a":1,"\\u0061":2}'),
      /Duplicate JSON key/,
    );
  });

  test("parses valid JSON and keeps __proto__ as an own data property", () => {
    const parsed = parseJsonStrict<{
      value: number;
      __proto__: { polluted: boolean };
    }>('{"value":1.5e2,"__proto__":{"polluted":true}}');

    assert.equal(parsed.value, 150);
    assert.equal(Object.hasOwn(parsed, "__proto__"), true);
    assert.equal(({} as { polluted?: boolean }).polluted, undefined);
    assert.equal(parsed.__proto__.polluted, true);
  });

  test("rejects duplicate-key request bodies without echoing the body", async () => {
    const app = createRouter({ ping: query(() => "pong") });
    const result = await handleRequest(app, {
      method: "POST",
      path: "ping",
      body: '{"secret":"BODY-SECRET","secret":"second"}',
    });

    assertFailureCode(result, ERROR_CODES.BAD_REQUEST);
    assert.equal(JSON.stringify(result).includes("BODY-SECRET"), false);
  });
});
