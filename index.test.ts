import assert from "node:assert/strict";
import test from "node:test";

import {
  CredentialFillPolicy,
  captureFrameContext,
  createCredentialFillPolicy,
  type CredentialFillRequest,
  type SavedCredential
} from "./index.ts";

type AttributeMap = Record<string, string>;

class FakeInput {
  value = "";
  form: FakeForm | null = null;
  onDispatch: (() => void) | undefined;
  private readonly attributes: AttributeMap;

  constructor(attributes: AttributeMap = {}) {
    this.attributes = { ...attributes };
    this.value = attributes.value ?? "";
  }

  get type(): string {
    return this.attributes.type ?? "text";
  }

  set type(value: string) {
    this.attributes.type = value;
  }

  get name(): string {
    return this.attributes.name ?? "";
  }

  get id(): string {
    return this.attributes.id ?? "";
  }

  get autocomplete(): string {
    return this.attributes.autocomplete ?? "";
  }

  get disabled(): boolean {
    return this.attributes.disabled !== undefined;
  }

  get readOnly(): boolean {
    return this.attributes.readonly !== undefined;
  }

  getAttribute(name: string): string | null {
    return this.attributes[name] ?? null;
  }

  setAttribute(name: string, value: string): void {
    this.attributes[name] = value;
  }

  closest(selector: string): FakeForm | null {
    return selector === "form" ? this.form : null;
  }

  dispatchEvent(): boolean {
    this.onDispatch?.();
    return true;
  }
}

class FakeForm {
  private readonly attributes: AttributeMap;
  readonly inputs: FakeInput[];
  readonly controls: FakeInput[];
  readonly baseUrl: string;

  constructor(
    attributes: AttributeMap = {},
    inputs: FakeInput[] = [],
    controls: FakeInput[] = [],
    baseUrl = "https://login.example.com/signin"
  ) {
    this.attributes = { ...attributes };
    this.inputs = inputs;
    this.controls = controls;
    this.baseUrl = baseUrl;
    for (const input of [...inputs, ...controls]) input.form = this;
  }

  get action(): string {
    const raw = this.attributes.action;
    if (raw === undefined) return this.baseUrl;
    if (raw.trim() === "") return this.baseUrl;
    return new URL(raw, this.baseUrl).href;
  }

  getAttribute(name: string): string | null {
    return this.attributes[name] ?? null;
  }

  setAttribute(name: string, value: string): void {
    this.attributes[name] = value;
  }

  removeAttribute(name: string): void {
    delete this.attributes[name];
  }

  contains(element: unknown): boolean {
    return this.inputs.includes(element as FakeInput) || this.controls.includes(element as FakeInput);
  }

  querySelectorAll(selector: string): FakeInput[] {
    if (selector === "input") return this.inputs;
    if (selector === "button[formaction], input[formaction]") {
      return this.controls.filter((control) => control.getAttribute("formaction") !== null);
    }
    return [];
  }

  querySelector(selector: string): FakeInput | null {
    return this.querySelectorAll(selector)[0] ?? null;
  }
}

class FakeDocument {
  readonly inputs: FakeInput[];
  readonly forms: FakeForm[];
  readonly actionControls: FakeInput[] = [];
  readonly location: { href: string; origin: string };
  readonly baseURI: string;
  defaultView: unknown = undefined;

  constructor(
    url: string,
    inputs: FakeInput[] = [],
    forms: FakeForm[] = [],
    baseURI = url
  ) {
    this.location = { href: url, origin: new URL(url).origin };
    this.inputs = inputs;
    this.forms = forms;
    this.baseURI = baseURI;
  }

  querySelectorAll(selector: string): FakeInput[] {
    if (selector === "input") return this.inputs;
    if (selector === "button[formaction], input[formaction]") return this.actionControls;
    return [];
  }

  querySelector(selector: string): FakeInput | FakeForm | null {
    if (/^#/.test(selector)) {
      const id = selector.slice(1);
      return (
        this.inputs.find((input) => input.id === id) ??
        this.forms.find((form) => form.getAttribute("id") === id) ??
        null
      );
    }
    return this.inputs[0] ?? null;
  }
}

function makeLoginPage(options: {
  readonly url?: string;
  readonly action?: string;
  readonly baseURI?: string;
  readonly submitAction?: string;
  readonly passwordAutocomplete?: string;
} = {}): {
  document: FakeDocument;
  form: FakeForm;
  username: FakeInput;
  password: FakeInput;
  submit: FakeInput;
} {
  const url = options.url ?? "https://login.example.com/signin";
  const username = new FakeInput({
    type: "text",
    name: "username",
    autocomplete: "username",
    id: "username"
  });
  const password = new FakeInput({
    type: "password",
    name: "password",
    autocomplete: options.passwordAutocomplete ?? "current-password",
    id: "password"
  });
  const submitAttributes: AttributeMap = { type: "submit" };
  if (options.submitAction !== undefined) {
    submitAttributes.formaction = options.submitAction;
  }
  const submit = new FakeInput(submitAttributes);
  const formAttributes: AttributeMap = {};
  if (options.action !== undefined) formAttributes.action = options.action;
  const form = new FakeForm(formAttributes, [username, password, submit], [submit], url);
  const document = new FakeDocument(url, [username, password, submit], [form], options.baseURI ?? url);
  return { document, form, username, password, submit };
}

function credential(overrides: Partial<SavedCredential> = {}): SavedCredential {
  return {
    id: "cred-1",
    origin: "https://login.example.com",
    username: "alice@example.com",
    password: "correct-horse-battery-staple",
    ...overrides
  };
}

function trustedClick(): { type: string; isTrusted: boolean } {
  return { type: "click", isTrusted: true };
}

function request(
  page: ReturnType<typeof makeLoginPage>,
  overrides: Partial<CredentialFillRequest> = {}
): CredentialFillRequest {
  return {
    credential: credential(),
    document: page.document as unknown as Document,
    frame: { documentUrl: page.document.location.href, ancestorOrigins: [] },
    gesture: trustedClick(),
    userActivation: { isActive: true },
    ...overrides
  };
}

test("fills credentials on the exact saved origin", () => {
  const page = makeLoginPage({ action: "/session" });
  const policy = createCredentialFillPolicy();

  const result = policy.fill(request(page));

  assert.equal(result.ok, true);
  assert.equal(result.reason, "ok");
  assert.equal(page.username.value, "alice@example.com");
  assert.equal(page.password.value, "correct-horse-battery-staple");
  assert.deepEqual(result.audit.fields, { username: true, password: true });
  assert.equal(result.audit.formAction, "https://login.example.com/session");
});

test("rejects a lookalike domain without touching fields", () => {
  const page = makeLoginPage({
    url: "https://login.example.com.attacker.test/signin",
    action: "/session"
  });
  const policy = createCredentialFillPolicy();

  const result = policy.fill(request(page));

  assert.equal(result.ok, false);
  assert.equal(result.reason, "document-origin-mismatch");
  assert.equal(page.username.value, "");
  assert.equal(page.password.value, "");
});

test("rejects unicode and numeric lookalike hosts", () => {
  for (const origin of [
    "https://lоgin.example.com",
    "https://login.examp1e.com",
    "https://login.example.com:8443"
  ]) {
    const page = makeLoginPage({ url: `${origin}/signin`, action: "/session" });
    const policy = createCredentialFillPolicy();
    const result = policy.fill(request(page));
    assert.equal(result.ok, false);
    assert.equal(page.password.value, "");
  }
});

test("rejects a cross-origin frame ancestor", () => {
  const page = makeLoginPage({ action: "/session" });
  const policy = createCredentialFillPolicy();

  const result = policy.fill(
    request(page, {
      frame: {
        documentUrl: page.document.location.href,
        ancestorOrigins: ["https://attacker.test"]
      }
    })
  );

  assert.equal(result.ok, false);
  assert.equal(result.reason, "cross-origin-frame-ancestor");
  assert.equal(page.password.value, "");
});

test("accepts same-origin ancestors but rejects an invalid ancestor origin", () => {
  const page = makeLoginPage({ action: "/session" });
  const policy = createCredentialFillPolicy();

  const sameOrigin = policy.fill(
    request(page, {
      frame: {
        documentUrl: page.document.location.href,
        ancestorOrigins: ["https://login.example.com", "https://login.example.com"]
      }
    })
  );
  assert.equal(sameOrigin.ok, true);

  page.username.value = "";
  page.password.value = "";
  const invalid = policy.fill(
    request(page, {
      frame: {
        documentUrl: page.document.location.href,
        ancestorOrigins: ["null"]
      }
    })
  );
  assert.equal(invalid.ok, false);
  assert.equal(invalid.reason, "invalid-frame-ancestor");
  assert.equal(page.password.value, "");
});

test("rejects a replaced form action", () => {
  for (const action of [
    "https://attacker.test/session",
    "https://login.example.com.attacker.test/session",
    "//attacker.test/session"
  ]) {
    const page = makeLoginPage({ action });
    const policy = createCredentialFillPolicy();
    const result = policy.fill(request(page));
    assert.equal(result.ok, false);
    assert.equal(result.reason, "form-action-mismatch");
    assert.equal(
      result.audit.formAction,
      new URL(action, "https://login.example.com/signin").href
    );
    assert.equal(page.username.value, "");
    assert.equal(page.password.value, "");
  }
});

test("rejects non-HTTP and malformed form actions", () => {
  for (const action of ["javascript:alert(document.cookie)", "http://["]) {
    const page = makeLoginPage({ action });
    const policy = createCredentialFillPolicy();
    const result = policy.fill(request(page));
    assert.equal(result.ok, false);
    assert.equal(result.reason, "invalid-form-action");
    assert.equal(page.password.value, "");
  }
});

test("rejects a cross-origin submit-button formaction override", () => {
  const page = makeLoginPage({
    action: "/session",
    submitAction: "https://attacker.test/session"
  });
  const policy = createCredentialFillPolicy();

  const result = policy.fill(request(page));

  assert.equal(result.ok, false);
  assert.equal(result.reason, "form-action-mismatch");
  assert.equal(page.password.value, "");
});

test("rejects a cross-origin formaction override from outside the form", () => {
  const page = makeLoginPage({ action: "/session" });
  page.form.setAttribute("id", "login-form");
  const externalSubmit = new FakeInput({
    type: "submit",
    form: "login-form",
    formaction: "https://attacker.test/session"
  });
  page.document.actionControls.push(externalSubmit);
  const policy = createCredentialFillPolicy();

  const result = policy.fill(request(page));

  assert.equal(result.ok, false);
  assert.equal(result.reason, "form-action-mismatch");
  assert.equal(page.password.value, "");
});

test("resolves relative actions against the document base URL", () => {
  const page = makeLoginPage({
    action: "session",
    baseURI: "https://attacker.test/signin"
  });
  const policy = createCredentialFillPolicy();

  const result = policy.fill(request(page));

  assert.equal(result.ok, false);
  assert.equal(result.reason, "form-action-mismatch");
  assert.equal(page.password.value, "");
});

test("enforces an optional exact form action pin", () => {
  const page = makeLoginPage({ action: "/other-login" });
  const policy = createCredentialFillPolicy();

  const result = policy.fill(
    request(page, {
      credential: credential({ formAction: "https://login.example.com/session" })
    })
  );

  assert.equal(result.ok, false);
  assert.equal(result.reason, "form-action-mismatch");
  assert.equal(page.password.value, "");
});

test("clears credentials if the action changes during a field event", () => {
  const page = makeLoginPage({ action: "/session" });
  page.password.onDispatch = () => {
    page.form.setAttribute("action", "https://attacker.test/session");
  };
  const policy = createCredentialFillPolicy();

  const result = policy.fill(request(page));

  assert.equal(result.ok, false);
  assert.equal(result.reason, "form-action-changed");
  assert.equal(result.audit.fields.password, false);
  assert.equal(page.username.value, "");
  assert.equal(page.password.value, "");
});

test("requires a trusted user gesture", () => {
  const cases: Array<{
    name: string;
    overrides: Partial<CredentialFillRequest>;
    reason: string;
  }> = [
    { name: "missing gesture", overrides: { gesture: undefined }, reason: "user-gesture-required" },
    {
      name: "untrusted event",
      overrides: { gesture: { type: "click", isTrusted: false } },
      reason: "untrusted-user-gesture"
    },
    {
      name: "expired activation",
      overrides: { userActivation: { isActive: false } },
      reason: "user-activation-inactive"
    }
  ];

  for (const testCase of cases) {
    const page = makeLoginPage({ action: "/session" });
    const policy = createCredentialFillPolicy();
    const result = policy.fill(request(page, testCase.overrides));
    assert.equal(result.ok, false, testCase.name);
    assert.equal(result.reason, testCase.reason, testCase.name);
    assert.equal(page.username.value, "", testCase.name);
    assert.equal(page.password.value, "", testCase.name);
  }
});

test("does not fill a new-password field", () => {
  const page = makeLoginPage({
    action: "/session",
    passwordAutocomplete: "new-password"
  });
  const policy = createCredentialFillPolicy();

  const result = policy.fill(request(page));

  assert.equal(result.ok, false);
  assert.equal(result.reason, "password-field-not-found");
  assert.equal(page.password.value, "");
});

test("refuses to guess between multiple password fields", () => {
  const page = makeLoginPage({ action: "/session" });
  const secondPassword = new FakeInput({
    type: "password",
    name: "confirm",
    autocomplete: "current-password"
  });
  secondPassword.form = page.form;
  page.document.inputs.push(secondPassword);
  page.form.inputs.push(secondPassword);
  const policy = createCredentialFillPolicy();

  const result = policy.fill(request(page));

  assert.equal(result.ok, false);
  assert.equal(result.reason, "ambiguous-password-field");
  assert.equal(page.password.value, "");
  assert.equal(secondPassword.value, "");
});

test("audit records are immutable, ordered, and contain no credentials", () => {
  const page = makeLoginPage({ action: "/session" });
  const supplied: unknown[] = [];
  const policy = new CredentialFillPolicy({
    now: () => new Date("2026-11-14T09:00:00.000Z"),
    onAudit: (record) => supplied.push(record)
  });

  policy.fill(request(page));
  const blockedPage = makeLoginPage({
    url: "https://login.example.com.attacker.test/signin",
    action: "/session"
  });
  policy.fill(request(blockedPage));

  const records = policy.getAuditLog();
  assert.equal(records.length, 2);
  assert.equal(supplied.length, 2);
  assert.equal(records[0]?.sequence, 1);
  assert.equal(records[1]?.sequence, 2);
  assert.equal(records[0]?.decision, "filled");
  assert.equal(records[1]?.reason, "document-origin-mismatch");
  assert.equal(records[0]?.timestamp, "2026-11-14T09:00:00.000Z");
  assert.equal(Object.isFrozen(records[0]), true);
  assert.equal(Object.isFrozen(records[0]?.ancestorOrigins), true);

  const serialized = JSON.stringify(records);
  assert.equal(serialized.includes("correct-horse-battery-staple"), false);
  assert.equal(serialized.includes("alice@example.com"), false);

  policy.clearAuditLog();
  assert.equal(policy.getAuditLog().length, 0);
});

test("caps the retained audit log when configured", () => {
  const page = makeLoginPage({ action: "/session" });
  const policy = createCredentialFillPolicy({ maxAuditRecords: 2 });

  policy.fill(request(page));
  policy.fill(request(page));
  policy.fill(request(page));

  const records = policy.getAuditLog();
  assert.equal(records.length, 2);
  assert.deepEqual(records.map((record) => record.sequence), [2, 3]);
});

test("rejects insecure credential origins but allows loopback development", () => {
  const insecurePage = makeLoginPage({
    url: "http://login.example.com/signin",
    action: "/session"
  });
  const policy = createCredentialFillPolicy();
  const blocked = policy.fill(
    request(insecurePage, {
      credential: credential({ origin: "http://login.example.com" }),
      frame: { documentUrl: insecurePage.document.location.href, ancestorOrigins: [] }
    })
  );
  assert.equal(blocked.ok, false);
  assert.equal(blocked.reason, "insecure-origin");
  assert.equal(insecurePage.password.value, "");

  const localPage = makeLoginPage({
    url: "http://localhost:3000/signin",
    action: "/session"
  });
  const allowed = policy.fill(
    request(localPage, {
      credential: credential({ origin: "http://localhost:3000" }),
      frame: { documentUrl: localPage.document.location.href, ancestorOrigins: [] }
    })
  );
  assert.equal(allowed.ok, true);
  assert.equal(localPage.password.value, "correct-horse-battery-staple");
});

test("fails closed when frame ancestry cannot be verified", () => {
  const page = makeLoginPage({ action: "/session" });
  const policy = createCredentialFillPolicy();

  const result = policy.fill(
    request(page, {
      frame: undefined
    })
  );

  assert.equal(result.ok, false);
  assert.equal(result.reason, "frame-ancestors-unverifiable");
  assert.equal(page.password.value, "");
});

test("rejects a supplied frame URL that disagrees with the document", () => {
  const page = makeLoginPage({ action: "/session" });
  const policy = createCredentialFillPolicy();

  const result = policy.fill(
    request(page, {
      frame: {
        documentUrl: "https://attacker.test/signin",
        ancestorOrigins: []
      }
    })
  );

  assert.equal(result.ok, false);
  assert.equal(result.reason, "frame-context-mismatch");
  assert.equal(page.password.value, "");
});

test("rejects an opaque or inconsistent document origin", () => {
  for (const origin of ["null", "https://attacker.test"]) {
    const page = makeLoginPage({ action: "/session" });
    (page.document.location as { origin: string }).origin = origin;
    const policy = createCredentialFillPolicy();
    const result = policy.fill(request(page));
    assert.equal(result.ok, false);
    assert.equal(result.reason, "invalid-document-origin");
    assert.equal(page.password.value, "");
  }
});

test("captures the document URL and browser-provided ancestor origins", () => {
  const view = {
    location: {
      href: "https://login.example.com/signin",
      ancestorOrigins: ["https://login.example.com"]
    }
  } as unknown as Window;

  assert.deepEqual(captureFrameContext(view), {
    documentUrl: "https://login.example.com/signin",
    ancestorOrigins: ["https://login.example.com"]
  });
});
