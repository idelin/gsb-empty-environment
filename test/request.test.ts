import { describe, it } from "node:test";
import assert from "node:assert/strict";

import type { FormSnapshot } from "../src/types.ts";
import { createSubmitPlan } from "../src/plan.ts";
import { prepareRequest } from "../src/request.ts";

const ORIGIN = "https://app.example.test";

function snapshot(overrides: Partial<FormSnapshot> = {}): FormSnapshot {
  return {
    documentGeneration: 1,
    origin: ORIGIN,
    baseUrl: `${ORIGIN}/checkout/`,
    action: "charge",
    method: "post",
    enctype: "application/x-www-form-urlencoded",
    entries: [
      { name: "amount", value: "100" },
      { name: "note", value: "a b&c" },
    ],
    ...overrides,
  };
}

describe("prepareRequest targets the pinned URL", () => {
  it("encodes GET entries into the query string and keeps the frozen host", async () => {
    const plan = createSubmitPlan(snapshot({ method: "get" }));
    const request = await prepareRequest(plan);

    assert.equal(request.method, "GET");
    assert.equal(request.body, null);
    const url = new URL(request.url);
    assert.equal(url.origin, ORIGIN);
    assert.equal(url.pathname, "/checkout/charge");
    assert.equal(url.searchParams.get("amount"), "100");
    assert.equal(url.searchParams.get("note"), "a b&c");
  });

  it("sends urlencoded POST bodies to the absolute approved target", async () => {
    const plan = createSubmitPlan(snapshot());
    const request = await prepareRequest(plan);

    assert.equal(request.url, "https://app.example.test/checkout/charge");
    assert.equal(request.method, "POST");
    assert.equal(
      request.headers["content-type"],
      "application/x-www-form-urlencoded;charset=UTF-8",
    );
    assert.equal(request.body, "amount=100&note=a+b%26c");
  });

  it("builds a multipart body with a boundary and carries blob entries", async () => {
    const blob = new Blob([new Uint8Array([1, 2, 3])], { type: "application/pdf" });
    const plan = createSubmitPlan(
      snapshot({
        enctype: "multipart/form-data",
        entries: [
          { name: "title", value: "receipt" },
          { name: "upload", value: blob, filename: "receipt.pdf" },
        ],
      }),
    );
    const request = await prepareRequest(plan);

    const contentType = request.headers["content-type"];
    assert.ok(contentType);
    assert.match(contentType, /^multipart\/form-data; boundary=----formplan/);
    const boundary = contentType.split("boundary=")[1] as string;
    const raw = new TextDecoder().decode(request.body as Uint8Array);

    assert.ok(raw.includes(`--${boundary}\r\n`));
    assert.ok(raw.includes('content-disposition: form-data; name="title"'));
    assert.ok(raw.includes('name="upload"; filename="receipt.pdf"'));
    assert.ok(raw.includes("content-type: application/pdf"));
    assert.ok(raw.includes(`\r\n--${boundary}--\r\n`));
    assert.equal((request.body as Uint8Array).at(-1), 0x0a);
  });

  it("emits text/plain bodies without percent encoding", async () => {
    const plan = createSubmitPlan(snapshot({ enctype: "text/plain" }));
    const request = await prepareRequest(plan);
    assert.equal(request.headers["content-type"], "text/plain;charset=UTF-8");
    assert.equal(request.body, "amount=100\r\nnote=a b&c");
  });

  it("does not re-resolve the URL against a changed base at request time", async () => {
    const plan = createSubmitPlan(snapshot());
    const request = await prepareRequest(plan);
    const live = snapshot({ baseUrl: "https://evil.example.test/checkout/" });
    const wouldBeTarget = createSubmitPlan(live);

    assert.equal(request.url, "https://app.example.test/checkout/charge");
    assert.equal(wouldBeTarget.target.absoluteUrl, "https://evil.example.test/checkout/charge");
  });
});
