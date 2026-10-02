import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { FormValidator } from "../src/form-validator.ts";
import type { FormEvent } from "../src/form-validator.ts";
import {
  custom,
  email,
  matchesField,
  minLength,
  required,
  requiredWhen,
} from "../src/rules.ts";
import { registrationSchema } from "../examples/registration-form.ts";

const collectEvents = (form: FormValidator): FormEvent[] => {
  const events: FormEvent[] = [];
  form.subscribe((event) => events.push(event));
  return events;
};

describe("basic field rules", () => {
  it("required flags empty values and clears when filled", () => {
    const form = new FormValidator({ name: { rules: [required()] } });
    assert.equal(form.validateAll(), false);
    assert.deepEqual(form.getState("name").errors, ["该字段为必填项"]);

    form.setValue("name", "delin");
    assert.equal(form.getState("name").valid, true);
    assert.equal(form.isValid(), true);
  });

  it("minLength only applies to non-empty values", () => {
    const form = new FormValidator({
      bio: { rules: [minLength(5, "太短了")] },
    });
    assert.equal(form.getState("bio").valid, true); // empty is not required
    form.setValue("bio", "abc");
    assert.deepEqual(form.getState("bio").errors, ["太短了"]);
    form.setValue("bio", "abcde");
    assert.equal(form.getState("bio").valid, true);
  });

  it("email pattern validates format", () => {
    const form = new FormValidator({ mail: { rules: [required(), email()] } });
    form.setValue("mail", "not-an-email");
    assert.deepEqual(form.getState("mail").errors, ["邮箱格式不正确"]);
    form.setValue("mail", "a@b.co");
    assert.equal(form.getState("mail").valid, true);
  });
});

describe("cross-field: password / confirmPassword", () => {
  const build = (): FormValidator =>
    new FormValidator({
      password: { rules: [required(), minLength(8)] },
      confirmPassword: {
        rules: [required(), matchesField("password", "两次输入的密码不一致")],
      },
    });

  it("mismatch is reported once both fields are filled", () => {
    const form = build();
    form.setValue("password", "abcdefgh");
    form.setValue("confirmPassword", "abcdefgX");
    assert.deepEqual(form.getState("confirmPassword").errors, [
      "两次输入的密码不一致",
    ]);
  });

  it("editing password re-triggers confirm comparison automatically", () => {
    const form = build();
    const events = collectEvents(form);

    form.setValue("password", "abcdefgh");
    form.setValue("confirmPassword", "abcdefgh");
    assert.equal(form.getState("confirmPassword").valid, true);

    // Change only the password: confirm must flip to invalid without touching it.
    form.setValue("password", "abcdefgX");
    assert.deepEqual(form.getState("confirmPassword").errors, [
      "两次输入的密码不一致",
    ]);

    const revalidated = events
      .filter((e) => e.type === "field")
      .map((e) => (e.type === "field" ? e.field : ""));
    assert.ok(revalidated.includes("confirmPassword"));

    // And it recovers when the password is changed back.
    form.setValue("password", "abcdefgh");
    assert.equal(form.getState("confirmPassword").valid, true);
  });
});

describe("dynamic rules: checkbox adds a required rule", () => {
  const build = (): FormValidator =>
    new FormValidator({
      married: { initial: false },
      spouseName: {
        rules: [
          requiredWhen(
            (values) => values.married === true,
            ["married"],
            "已勾选已婚，配偶姓名必填",
          ),
        ],
      },
    });

  it("spouseName is optional until the checkbox is ticked", () => {
    const form = build();
    assert.equal(form.getState("spouseName").valid, true);
    assert.equal(form.validateAll(), true);
  });

  it("ticking the checkbox instantly makes spouseName required", () => {
    const form = build();
    const events = collectEvents(form);

    form.setValue("married", true);
    assert.deepEqual(form.getState("spouseName").errors, [
      "已勾选已婚，配偶姓名必填",
    ]);
    assert.equal(form.isValid(), false);

    const spouseEvent = events.find(
      (e) => e.type === "field" && e.field === "spouseName",
    );
    assert.ok(spouseEvent, "expected a re-validation event for spouseName");

    form.setValue("spouseName", "王芳");
    assert.equal(form.getState("spouseName").valid, true);
    assert.equal(form.isValid(), true);
  });

  it("unticking the checkbox removes the rule and its error instantly", () => {
    const form = build();
    form.setValue("married", true);
    assert.equal(form.getState("spouseName").valid, false);

    form.setValue("married", false);
    assert.equal(form.getState("spouseName").valid, true);
    assert.equal(form.isValid(), true);
  });
});

describe("full-form validation", () => {
  it("validateAll marks every field touched and reports all errors", () => {
    const form = new FormValidator(registrationSchema);
    assert.equal(form.validateAll(), false);

    const errors = form.getErrors();
    assert.ok(errors.username);
    assert.ok(errors.email);
    assert.ok(errors.password);
    assert.ok(errors.confirmPassword);
    assert.equal(errors.spouseName, undefined); // married defaults to false

    for (const name of Object.keys(registrationSchema)) {
      assert.equal(form.getState(name).touched, true);
    }
  });

  it("validateAll returns true once everything is satisfied", () => {
    const form = new FormValidator(registrationSchema);
    form.setValue("username", "delin");
    form.setValue("email", "delin@example.com");
    form.setValue("password", "S3cret!pass");
    form.setValue("confirmPassword", "S3cret!pass");
    form.setValue("married", true);
    form.setValue("spouseName", "王芳");
    assert.equal(form.validateAll(), true);
    assert.deepEqual(form.getErrors(), {});
  });
});

describe("reactive plumbing", () => {
  it("emits form events with the current validity snapshot", () => {
    const form = new FormValidator({ name: { rules: [required()] } });
    const events = collectEvents(form);

    form.setValue("name", "x");
    const formEvents = events.filter((e) => e.type === "form");
    assert.ok(formEvents.length > 0);
    const last = formEvents[formEvents.length - 1];
    assert.equal(last.type === "form" && last.valid, true);
  });

  it("supports transitive dependencies through custom rules", () => {
    const form = new FormValidator({
      a: { initial: 1 },
      b: {
        initial: 2,
        rules: [
          custom(
            (value, ctx) =>
              Number(value) > Number(ctx.values.a) ? null : "b 必须大于 a",
            { dependsOn: ["a"] },
          ),
        ],
      },
      c: {
        initial: 3,
        rules: [
          custom(
            (value, ctx) =>
              Number(value) > Number(ctx.values.b) ? null : "c 必须大于 b",
            { dependsOn: ["b"] },
          ),
        ],
      },
    });
    assert.equal(form.isValid(), true);

    form.setValue("a", 5); // b (2 > 5?) fails, then c (3 > 2?) still ok
    assert.deepEqual(form.getState("b").errors, ["b 必须大于 a"]);
    assert.equal(form.getState("c").valid, true);

    form.setValue("b", 10); // b recovers, c (3 > 10?) now fails transitively
    assert.equal(form.getState("b").valid, true);
    assert.deepEqual(form.getState("c").errors, ["c 必须大于 b"]);
  });

  it("rejects dependencies on unknown fields", () => {
    assert.throws(
      () =>
        new FormValidator({
          x: { rules: [matchesField("nope")] },
        }),
      /unknown field "nope"/,
    );
  });

  it("reset restores initials and clears dirty/touched", () => {
    const form = new FormValidator({
      married: { initial: false },
      spouseName: {
        rules: [requiredWhen((v) => v.married === true, ["married"])],
      },
    });
    form.setValue("married", true);
    assert.equal(form.isValid(), false);

    form.reset();
    assert.equal(form.isValid(), true);
    assert.equal(form.getState("married").dirty, false);
    assert.equal(form.getState("married").touched, false);
  });
});
