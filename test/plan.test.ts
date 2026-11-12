import { describe, it } from "node:test";
import assert from "node:assert/strict";

import { SubmitError } from "../src/types.ts";
import type { FormSnapshot } from "../src/types.ts";
import { createSubmitPlan } from "../src/plan.ts";
import { OriginAllowlistPolicy } from "../src/dom.ts";

const ORIGIN = "https://app.example.test";

function snapshot(overrides: Partial<FormSnapshot> = {}): FormSnapshot {
  return {
    documentGeneration: 1,
    origin: ORIGIN,
    baseUrl: `${ORIGIN}/checkout/`,
    action: "charge",
    method: "post",
    enctype: "application/x-www-form-urlencoded",
    entries: [{ name: "amount", value: "100" }],
    ...overrides,
  };
}

describe("approval freezes the target", () => {
  it("pins the absolute URL, method and enctype at approval time", () => {
    const plan = createSubmitPlan(snapshot());
    assert.equal(plan.target.absoluteUrl, "https://app.example.test/checkout/charge");
    assert.equal(plan.target.method, "POST");
    assert.equal(plan.target.enctype, "application/x-www-form-urlencoded");
    assert.equal(plan.documentOrigin, "https://app.example.test");
    assert.equal(plan.documentGeneration, 1);
  });

  it("ignores a later <base> mutation because the plan holds an absolute URL", () => {
    const plan = createSubmitPlan(snapshot());
    const poisoned = snapshot({ baseUrl: "https://evil.example.test/checkout/" });

    assert.equal(plan.target.absoluteUrl, "https://app.example.test/checkout/charge");
    assert.equal(
      createSubmitPlan(poisoned).target.absoluteUrl,
      "https://evil.example.test/checkout/charge",
    );
    assert.notEqual(
      plan.target.absoluteUrl,
      createSubmitPlan(poisoned).target.absoluteUrl,
    );
  });

  it("deep freezes the plan so callers cannot mutate the target", () => {
    const plan = createSubmitPlan(snapshot());
    assert.throws(() => {
      (plan.target as { absoluteUrl: string }).absoluteUrl = "https://evil.example.test/x";
    }, TypeError);
    assert.throws(() => {
      (plan.entries as unknown as Array<{ name: string; value: string }>).push({
        name: "x",
        value: "y",
      });
    }, TypeError);
  });

  it("defaults blank method/enctype to GET and urlencoded", () => {
    const plan = createSubmitPlan(snapshot({ method: "", enctype: "" }));
    assert.equal(plan.target.method, "GET");
    assert.equal(plan.target.enctype, "application/x-www-form-urlencoded");
  });

  it("follows HTML semantics by falling back to GET for unknown methods", () => {
    const plan = createSubmitPlan(snapshot({ method: "delete" }));
    assert.equal(plan.target.method, "GET");
  });
});

describe("multi-submit-button overrides", () => {
  it("applies a same-origin button formaction through the same decision", () => {
    const plan = createSubmitPlan(snapshot(), {
      name: "confirm",
      value: "true",
      formAction: "express",
      formMethod: "get",
      formEnctype: "text/plain",
    });

    assert.equal(plan.target.absoluteUrl, "https://app.example.test/checkout/express");
    assert.equal(plan.target.method, "GET");
    assert.equal(plan.target.enctype, "text/plain");
    assert.deepEqual(plan.entries.at(-1), { name: "confirm", value: "true" });
  });

  it("rejects a button that retargets to a cross-origin host under the default policy", () => {
    assert.throws(
      () =>
        createSubmitPlan(snapshot(), {
          name: "pay",
          formAction: "https://evil.example.test/collect",
        }),
      (error: unknown) => error instanceof SubmitError && error.code === "TARGET_DENIED",
    );
  });

  it("requires a fresh approval for every distinct button", () => {
    const regular = createSubmitPlan(snapshot(), { name: "save" });
    const express = createSubmitPlan(snapshot(), {
      name: "express",
      formAction: "express",
    });
    assert.notEqual(regular.id, express.id);
    assert.equal(regular.target.absoluteUrl, "https://app.example.test/checkout/charge");
    assert.equal(express.target.absoluteUrl, "https://app.example.test/checkout/express");
  });

  it("freezes the captured button override so later DOM edits do not affect it", () => {
    const override = { name: "pay", formAction: "/safe" };
    const plan = createSubmitPlan(snapshot(), override);
    override.formAction = "https://evil.example.test/steal";
    assert.equal(plan.target.absoluteUrl, "https://app.example.test/safe");
    assert.ok(Object.isFrozen(plan.submitter));
    assert.equal(plan.submitter?.formAction, "/safe");
  });

  it("honors an explicit allowlist policy for a permitted cross-origin button", () => {
    const policy = new OriginAllowlistPolicy([
      "https://app.example.test",
      "https://payments.example.test",
    ]);
    const allowed = createSubmitPlan(
      snapshot(),
      { formAction: "https://payments.example.test/v1/charge" },
      { policy },
    );
    assert.equal(allowed.target.origin, "https://payments.example.test");

    assert.throws(
      () =>
        createSubmitPlan(
          snapshot(),
          { formAction: "https://evil.example.test/collect" },
          { policy },
        ),
      (error: unknown) => error instanceof SubmitError && error.code === "TARGET_DENIED",
    );
  });
});

describe("hardening checks", () => {
  it("rejects methods and encodings outside the configured allow lists", () => {
    assert.throws(
      () =>
        createSubmitPlan(snapshot({ method: "post" }), null, {
          allowedMethods: ["GET"],
        }),
      (error: unknown) => error instanceof SubmitError && error.code === "METHOD_NOT_ALLOWED",
    );
    assert.throws(
      () =>
        createSubmitPlan(snapshot({ enctype: "multipart/form-data" }), null, {
          allowedEncTypes: ["application/x-www-form-urlencoded"],
        }),
      (error: unknown) => error instanceof SubmitError && error.code === "ENCTYPE_NOT_ALLOWED",
    );
  });

  it("rejects invalid generation tokens", () => {
    assert.throws(
      () => createSubmitPlan(snapshot({ documentGeneration: Number.NaN })),
      (error: unknown) => error instanceof SubmitError && error.code === "INVALID_SNAPSHOT",
    );
  });
});
