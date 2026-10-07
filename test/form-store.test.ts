import assert from "node:assert/strict";
import { test } from "node:test";
import { FormStore, SubmissionInFlightError } from "../src/index.ts";
import { createControllableSubmitter } from "../src/testing.ts";

function setup(initialValues = { a: 1, b: 2 }) {
  const { submitter, requests } = createControllableSubmitter();
  const form = new FormStore({ initialValues, submitter });
  return { form, requests };
}

test("submit freezes a snapshot of the values", () => {
  const { form, requests } = setup();
  form.submit();
  form.setValue("a", 99);
  form.deleteField("b");
  assert.deepEqual(requests[0].snapshot, { a: 1, b: 2 });
  assert.equal(Object.isFrozen(requests[0].snapshot), true);
});

test("confirmation applies only to the submitted version; edits during flight stay dirty", async () => {
  const { form, requests } = setup();
  const { promise } = form.submit();
  form.setValue("a", 99); // edited while in flight
  assert.equal(form.isFieldDirty("a"), true);

  requests[0].resolve({ ok: true });
  const record = await promise;

  assert.equal(record.status, "confirmed");
  assert.deepEqual(form.baseValues, { a: 1, b: 2 }); // base = submitted version
  assert.equal(form.isFieldDirty("a"), true); // 99 vs confirmed 1
  assert.equal(form.isFieldDirty("b"), false); // untouched -> clean
  assert.equal(form.isDirty, true);
});

test("server field errors apply only to fields still equal to the submitted value", async () => {
  const { form, requests } = setup();
  const { promise } = form.submit();
  form.setValue("b", 3); // changed after snapshot

  requests[0].resolve({
    ok: false,
    fieldErrors: { a: "bad a", b: "bad b" },
  });
  const record = await promise;

  assert.equal(record.status, "rejected");
  assert.equal(form.fieldError("a"), "bad a"); // unchanged field: applied
  assert.equal(form.fieldError("b"), undefined); // edited field: not applied
  assert.deepEqual(record.appliedFieldErrors, ["a"]);
  assert.deepEqual(record.fieldErrors, { a: "bad a", b: "bad b" }); // kept on record
});

test("non-field errors are kept on the submission record only", async () => {
  const { form, requests } = setup();
  const { promise } = form.submit();

  requests[0].resolve({
    ok: false,
    fieldErrors: { a: "bad a" },
    formErrors: ["quota exceeded", "try again later"],
  });
  const record = await promise;

  assert.deepEqual(record.formErrors, ["quota exceeded", "try again later"]);
  assert.equal(form.lastSubmission, record);
  assert.equal(form.fieldError("a"), "bad a");
  // form errors never land on fields
  assert.equal(form.fieldError("b"), undefined);
});

test("duplicate submit while in flight is rejected; submit allowed again after settle", async () => {
  const { form, requests } = setup();
  const { promise } = form.submit();
  assert.equal(form.submitting, true);

  assert.throws(() => form.submit(), SubmissionInFlightError);
  assert.equal(requests.length, 1); // no second request went out

  requests[0].resolve({ ok: true });
  await promise;
  assert.equal(form.submitting, false);

  form.setValue("a", 2);
  form.submit();
  assert.equal(requests.length, 2);
  assert.deepEqual(requests[1].snapshot, { a: 2, b: 2 });
});

test("cancel marks the submission cancelled and a late response is ignored", async () => {
  const { form, requests } = setup();
  const { promise } = form.submit();

  assert.equal(form.cancelSubmit(), true);
  assert.equal(form.submitting, false);
  assert.equal(requests[0].signal.aborted, true);

  // Late success for the cancelled version must not touch the base values.
  requests[0].resolve({ ok: true, confirmedValues: { a: 100, b: 200 } });
  const record = await promise;

  assert.equal(record.status, "cancelled");
  assert.deepEqual(form.baseValues, { a: 1, b: 2 });
  assert.equal(form.isDirty, false);

  // A new submission is allowed immediately after cancelling.
  form.submit();
  assert.equal(requests.length, 2);
});

test("late rejection after cancel is ignored too", async () => {
  const { form, requests } = setup();
  const { promise } = form.submit();
  form.cancelSubmit();

  requests[0].resolve({ ok: false, fieldErrors: { a: "bad a" } });
  const record = await promise;

  assert.equal(record.status, "cancelled");
  assert.equal(form.fieldError("a"), undefined);
  assert.deepEqual(record.fieldErrors, {});
});

test("field deleted during flight: its server error is kept on the record but never applied", async () => {
  const { form, requests } = setup();
  const { promise } = form.submit();
  form.deleteField("b");

  requests[0].resolve({
    ok: false,
    fieldErrors: { a: "bad a", b: "bad b" },
  });
  const record = await promise;

  assert.equal(form.fieldError("a"), "bad a");
  assert.equal(form.fieldError("b"), undefined); // no live field anymore
  assert.deepEqual(record.appliedFieldErrors, ["a"]);
  assert.deepEqual(record.fieldErrors, { a: "bad a", b: "bad b" });
});

test("field deleted during flight: confirmation does not resurrect it", async () => {
  const { form, requests } = setup();
  const { promise } = form.submit();
  form.deleteField("b");
  assert.equal(form.isDirty, true); // deletion itself is a change

  requests[0].resolve({ ok: true, confirmedValues: { a: 1, b: 2 } });
  await promise;

  assert.equal(form.hasField("b"), false);
  assert.deepEqual(form.baseValues, { a: 1 }); // b dropped from base as well
  assert.equal(form.isDirty, false);
});

test("editing a field clears its applied server error", async () => {
  const { form, requests } = setup();
  const { promise } = form.submit();
  requests[0].resolve({ ok: false, fieldErrors: { a: "bad a" } });
  await promise;
  assert.equal(form.fieldError("a"), "bad a");

  form.setValue("a", 5);
  assert.equal(form.fieldError("a"), undefined);
});

test("submitter throwing marks the submission failed with the error on the record", async () => {
  const { form, requests } = setup();
  const { promise } = form.submit();
  const boom = new Error("network down");
  requests[0].reject(boom);
  const record = await promise;

  assert.equal(record.status, "failed");
  assert.equal(record.error, boom);
  assert.equal(form.submitting, false);
  assert.deepEqual(form.baseValues, { a: 1, b: 2 });
});
