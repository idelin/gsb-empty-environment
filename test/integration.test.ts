import { describe, it } from "node:test";
import assert from "node:assert/strict";

import { SubmitError } from "../src/types.ts";
import type {
  FormSnapshot,
  PreparedRequest,
  TransportResult,
} from "../src/types.ts";
import { createSubmitPlan, prepareRequest, submitPlan } from "../index.ts";

const ORIGIN = "https://shop.example.test";

class CaptureTransport {
  readonly sent: PreparedRequest[] = [];

  async dispatch(request: PreparedRequest): Promise<TransportResult> {
    this.sent.push(request);
    return { ok: true, status: 204, url: request.url };
  }
}

describe("end-to-end pinned submission", () => {
  it("keeps the approved target across base tampering and a second button", async () => {
    const live = {
      generation: 3,
      origin: ORIGIN,
      baseUrl: `${ORIGIN}/cart/`,
    };
    const snapshot: FormSnapshot = {
      documentGeneration: live.generation,
      origin: live.origin,
      baseUrl: live.baseUrl,
      action: "checkout",
      method: "post",
      enctype: "application/x-www-form-urlencoded",
      entries: [{ name: "cart", value: "42" }],
    };

    const plan = createSubmitPlan(snapshot, { name: "confirm", value: "1" });

    // Attacker rewrites <base> after approval; the frozen plan is unaffected.
    live.baseUrl = "https://attacker.example.test/cart/";
    const request = await prepareRequest(plan);
    assert.equal(request.url, "https://shop.example.test/cart/checkout");
    assert.equal(request.body, "cart=42&confirm=1");

    const transport = new CaptureTransport();
    const outcome = await submitPlan(
      plan,
      () => ({ ...live }),
      transport,
    );
    assert.equal(outcome.result.status, 204);
    assert.equal(transport.sent.length, 1);
    assert.equal(
      transport.sent[0]?.url,
      "https://shop.example.test/cart/checkout",
    );
  });

  it("refuses the whole dispatch once a navigation invalidates the generation", async () => {
    const live = { generation: 1, origin: ORIGIN, baseUrl: `${ORIGIN}/` };
    const plan = createSubmitPlan({
      documentGeneration: 1,
      origin: ORIGIN,
      baseUrl: live.baseUrl,
      action: "/pay",
      method: "post",
      enctype: "text/plain",
      entries: [],
    });

    live.generation = 2;
    const transport = new CaptureTransport();
    await assert.rejects(
      () => submitPlan(plan, () => ({ ...live }), transport),
      (error: unknown) => error instanceof SubmitError && error.code === "STALE_GENERATION",
    );
    assert.equal(transport.sent.length, 0);
  });
});
