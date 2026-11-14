/**
 * Credential fill policy for a TypeScript login portal.
 *
 * The policy is deliberately synchronous: transient user activation has to be
 * checked and the credentials have to be written while the gesture is still
 * active. A fill is allowed only when all of the following hold:
 *
 * - the document origin exactly equals the credential origin;
 * - every frame ancestor has that same exact origin;
 * - the form's effective action (including submit-button overrides) stays on
 *   that origin;
 * - the call is made from a trusted user gesture and transient activation is
 *   still active;
 * - an unambiguous password field can be found.
 *
 * The policy never writes the username or password to the audit log.
 */

export interface SavedCredential {
  /** Optional identifier used in audit records. Never put a secret here. */
  readonly id?: string;
  /**
   * Absolute URL or origin for which the credential was saved. Only its exact
   * origin (scheme, host and port) is used for matching.
   */
  readonly origin: string;
  readonly username: string;
  readonly password: string;
  /**
   * Optional exact form endpoint pin. Relative values are resolved against the
   * credential origin. When supplied, the effective form action must match it
   * exactly (apart from the fragment).
   */
  readonly formAction?: string;
}

export interface FrameContext {
  /** URL of the document that contains the form. */
  readonly documentUrl?: string | URL;
  /**
   * Browser-provided ancestor origins, nearest first or outermost first.
   * An empty array means "top-level document". When omitted, the policy tries
   * to read `location.ancestorOrigins` and then walk `window.parent`.
   *
   * This value must come from the browser (for example
   * `location.ancestorOrigins`), never from page-controlled data.
   */
  readonly ancestorOrigins?: readonly string[];
}

export interface UserActivationLike {
  readonly isActive?: boolean;
}

export interface CredentialFillRequest {
  readonly credential: SavedCredential;
  readonly document: Document;
  /** URL of the document containing the form. */
  readonly frame?: FrameContext;
  /** Trusted event that represents the user's fill gesture. */
  readonly gesture?: Event | { readonly isTrusted?: boolean; readonly type?: string };
  /** Optional override, mainly useful in tests and embedded WebViews. */
  readonly userActivation?: UserActivationLike | null;
  /** Optional form, or a CSS selector for it. */
  readonly form?: HTMLFormElement | string;
  /** Optional username field, or a CSS selector for it. */
  readonly usernameField?: HTMLInputElement | string;
  /** Optional password field, or a CSS selector for it. */
  readonly passwordField?: HTMLInputElement | string;
}

export type FillBlockReason =
  | "invalid-credential"
  | "invalid-credential-origin"
  | "insecure-origin"
  | "document-url-unavailable"
  | "invalid-document-origin"
  | "frame-context-mismatch"
  | "document-origin-mismatch"
  | "frame-ancestors-unverifiable"
  | "invalid-frame-ancestor"
  | "cross-origin-frame-ancestor"
  | "user-gesture-required"
  | "untrusted-user-gesture"
  | "user-activation-inactive"
  | "form-not-found"
  | "invalid-form"
  | "field-form-mismatch"
  | "password-field-not-found"
  | "password-field-not-fillable"
  | "ambiguous-password-field"
  | "username-field-not-fillable"
  | "form-action-unavailable"
  | "invalid-form-action"
  | "invalid-form-action-pin"
  | "form-action-mismatch"
  | "form-action-changed"
  | "credential-cleanup-failed"
  | "fill-failed"
  | "unexpected-error";

export type FillReason = "ok" | FillBlockReason;

export interface FillAuditRecord {
  readonly sequence: number;
  readonly timestamp: string;
  readonly event: "credential-fill";
  readonly decision: "filled" | "blocked";
  readonly reason: FillReason;
  readonly message: string;
  readonly credentialId: string | null;
  readonly expectedOrigin: string | null;
  readonly documentOrigin: string | null;
  readonly ancestorOrigins: readonly string[];
  readonly frameAncestorsVerified: boolean;
  readonly formAction: string | null;
  readonly gesture: {
    readonly type: string | null;
    readonly trusted: boolean;
    readonly active: boolean | null;
  };
  readonly fields: {
    readonly username: boolean;
    readonly password: boolean;
  };
}

export interface FillResult {
  readonly ok: boolean;
  readonly decision: "filled" | "blocked";
  readonly reason: FillReason;
  readonly message: string;
  readonly credentialId: string | null;
  readonly audit: FillAuditRecord;
}

export interface CredentialFillPolicyOptions {
  /**
   * Require an HTTPS credential origin. HTTP is still accepted for loopback
   * hosts when `allowLoopbackHttp` is true. Defaults to true.
   */
  readonly requireSecureOrigin?: boolean;
  /** Allow HTTP for localhost/loopback development. Defaults to true. */
  readonly allowLoopbackHttp?: boolean;
  /** Require `navigator.userActivation.isActive` when that API exists. */
  readonly requireActiveUserActivation?: boolean;
  /** Injected clock, useful for deterministic audit records. */
  readonly now?: () => Date;
  /** Optional audit sink. Records are always appended to the internal log too. */
  readonly onAudit?: (record: FillAuditRecord) => void;
  /** Maximum retained internal audit records; <= 0 means unlimited. */
  readonly maxAuditRecords?: number;
}

interface MutableAttempt {
  credentialId: string | null;
  expectedOrigin: string | null;
  documentOrigin: string | null;
  ancestorOrigins: string[];
  frameAncestorsVerified: boolean;
  formAction: string | null;
  gesture: {
    type: string | null;
    trusted: boolean;
    active: boolean | null;
  };
  fields: {
    username: boolean;
    password: boolean;
  };
}

interface AncestorResolution {
  readonly ok: boolean;
  readonly reason?: "frame-ancestors-unverifiable" | "invalid-frame-ancestor" | "cross-origin-frame-ancestor";
  readonly origins: string[];
}

const DEFAULT_MAX_AUDIT_RECORDS = 1000;
const MESSAGES: Record<FillReason, string> = {
  ok: "Credentials filled.",
  "invalid-credential": "The saved credential is malformed.",
  "invalid-credential-origin": "The saved credential does not have a valid web origin.",
  "insecure-origin": "Credentials may not be filled on an insecure origin.",
  "document-url-unavailable": "The document URL could not be determined.",
  "invalid-document-origin": "The document does not have a valid web origin.",
  "frame-context-mismatch": "The supplied frame context does not match the document.",
  "document-origin-mismatch": "The document origin does not exactly match the credential origin.",
  "frame-ancestors-unverifiable": "The document's frame ancestors could not be verified.",
  "invalid-frame-ancestor": "A frame ancestor has an invalid origin.",
  "cross-origin-frame-ancestor": "A frame ancestor has a different origin from the credential.",
  "user-gesture-required": "A trusted user gesture is required before filling credentials.",
  "untrusted-user-gesture": "The supplied gesture is not a trusted browser event.",
  "user-activation-inactive": "Transient user activation is no longer active.",
  "form-not-found": "No login form could be identified.",
  "invalid-form": "The supplied form is not valid.",
  "field-form-mismatch": "The credential fields do not belong to the validated form.",
  "password-field-not-found": "No fillable password field was found.",
  "password-field-not-fillable": "The password field is not eligible for credential filling.",
  "ambiguous-password-field": "More than one password field is present; refusing to guess.",
  "username-field-not-fillable": "The username field is not eligible for credential filling.",
  "form-action-unavailable": "The form action could not be determined.",
  "invalid-form-action": "The form action is not a valid HTTP(S) URL.",
  "invalid-form-action-pin": "The saved form action pin is invalid.",
  "form-action-mismatch": "The form action does not exactly match the credential origin or pinned action.",
  "form-action-changed": "The form action changed while the credentials were being filled.",
  "credential-cleanup-failed": "The credentials could not be removed after the fill was rejected.",
  "fill-failed": "The browser refused a credential field write.",
  "unexpected-error": "The fill attempt failed unexpectedly."
};

function safeGetAttribute(element: Element | null | undefined, name: string): string | null {
  if (!element || typeof element.getAttribute !== "function") return null;
  try {
    return element.getAttribute(name);
  } catch {
    return null;
  }
}

function hasAttribute(element: Element | null | undefined, name: string): boolean {
  return safeGetAttribute(element, name) !== null;
}

function ownStringProperty(value: unknown, key: string): string | null {
  if (!value || (typeof value !== "object" && typeof value !== "function")) return null;
  try {
    const property = (value as Record<string, unknown>)[key];
    return typeof property === "string" ? property : null;
  } catch {
    return null;
  }
}

function ownBooleanProperty(value: unknown, key: string): boolean | null {
  if (!value || (typeof value !== "object" && typeof value !== "function")) return null;
  try {
    const property = (value as Record<string, unknown>)[key];
    return typeof property === "boolean" ? property : null;
  } catch {
    return null;
  }
}

function parseWebUrl(value: string | URL | null | undefined): URL | null {
  if (value === null || value === undefined) return null;
  try {
    const url = value instanceof URL ? new URL(value.href) : new URL(value);
    if (url.origin === "null") return null;
    if (url.protocol !== "https:" && url.protocol !== "http:") return null;
    return url;
  } catch {
    return null;
  }
}

function normalizedOrigin(value: string | URL | null | undefined): string | null {
  const url = parseWebUrl(value);
  return url ? url.origin : null;
}

function isLoopbackHostname(hostname: string): boolean {
  const host = hostname.toLowerCase();
  return (
    host === "localhost" ||
    host.endsWith(".localhost") ||
    host === "127.0.0.1" ||
    host === "::1" ||
    host === "[::1]"
  );
}

function isAllowedTransport(url: URL, options: CredentialFillPolicyOptions): boolean {
  if (url.protocol === "https:") return true;
  if (url.protocol !== "http:") return false;
  if (options.requireSecureOrigin === false) return true;
  return options.allowLoopbackHttp !== false && isLoopbackHostname(url.hostname);
}

function stripFragment(url: URL): string {
  const copy = new URL(url.href);
  copy.hash = "";
  return copy.href;
}

function readDocumentUrl(document: Document): string | null {
  if (!document) return null;
  try {
    const location: unknown = document.location;
    if (typeof location === "string" && location.length > 0) return location;
    if (
      location &&
      typeof location === "object" &&
      typeof (location as { href?: unknown }).href === "string" &&
      (location as { href: string }).href.length > 0
    ) {
      return (location as { href: string }).href;
    }
  } catch {
  }
  const documentUrl = ownStringProperty(document, "URL");
  return documentUrl && documentUrl.length > 0 ? documentUrl : null;
}

function readLocationOrigin(document: Document): "null" | string | null {
  try {
    const location = document.location as unknown;
    if (!location || typeof location !== "object") return null;
    const origin = (location as { origin?: unknown }).origin;
    if (typeof origin !== "string" || origin.length === 0) return null;
    if (origin === "null") return "null";
    return normalizedOrigin(origin);
  } catch {
    return null;
  }
}

function readBaseUrl(document: Document, documentUrl: string): URL | null {
  const baseUri = ownStringProperty(document, "baseURI");
  if (!baseUri) return new URL(documentUrl);
  return parseWebUrl(baseUri);
}

function collectListedOrigins(list: unknown): string[] | null {
  if (!list || (typeof list !== "object" && typeof list !== "function")) return null;
  try {
    const length = Number((list as { length?: unknown }).length);
    if (!Number.isFinite(length) || length < 0) return null;
    const origins: string[] = [];
    for (let index = 0; index < length; index += 1) {
      const item = (list as Record<number, unknown>)[index];
      if (typeof item !== "string") return null;
      origins.push(item);
    }
    return origins;
  } catch {
    return null;
  }
}

function resolveAncestors(document: Document, frame: FrameContext | undefined): AncestorResolution {
  if (frame && frame.ancestorOrigins !== undefined) {
    const origins: string[] = [];
    for (const ancestor of frame.ancestorOrigins) {
      const origin = normalizedOrigin(ancestor);
      if (!origin) {
        return { ok: false, reason: "invalid-frame-ancestor", origins };
      }
      origins.push(origin);
    }

    try {
      const view = document.defaultView;
      if (view && view.top !== view && origins.length === 0) {
        return { ok: false, reason: "frame-ancestors-unverifiable", origins };
      }
    } catch {
      return { ok: false, reason: "frame-ancestors-unverifiable", origins };
    }

    return { ok: true, origins };
  }

  let view: Window | null = null;
  try {
    view = document.defaultView;
  } catch {
    return { ok: false, reason: "frame-ancestors-unverifiable", origins: [] };
  }
  if (!view) {
    return { ok: false, reason: "frame-ancestors-unverifiable", origins: [] };
  }

  let listed: string[] | null = null;
  try {
    const locationWithAncestors = view.location as Location & {
      readonly ancestorOrigins?: ArrayLike<string>;
    };
    listed = collectListedOrigins(locationWithAncestors.ancestorOrigins);
  } catch {
    listed = null;
  }

  let isTopLevel = false;
  try {
    isTopLevel = view.top === view;
  } catch {
    return { ok: false, reason: "frame-ancestors-unverifiable", origins: [] };
  }

  if (listed !== null && (listed.length > 0 || isTopLevel)) {
    const origins: string[] = [];
    for (const ancestor of listed) {
      const origin = normalizedOrigin(ancestor);
      if (!origin) {
        return { ok: false, reason: "invalid-frame-ancestor", origins };
      }
      origins.push(origin);
    }
    return { ok: true, origins };
  }

  if (isTopLevel) return { ok: true, origins: [] };

  const origins: string[] = [];
  let current: Window = view;
  const seen = new Set<Window>();
  for (let depth = 0; depth < 32; depth += 1) {
    if (seen.has(current)) {
      return { ok: false, reason: "frame-ancestors-unverifiable", origins };
    }
    seen.add(current);

    let parent: Window;
    let top: Window;
    try {
      parent = current.parent;
      top = current.top;
    } catch {
      return { ok: false, reason: "frame-ancestors-unverifiable", origins };
    }

    if (parent === current) {
      return { ok: true, origins };
    }

    try {
      const parentLocation = parent.location;
      const origin =
        normalizedOrigin(parentLocation?.origin) ??
        normalizedOrigin(parentLocation?.href);
      if (!origin) {
        return { ok: false, reason: "invalid-frame-ancestor", origins };
      }
      origins.push(origin);
    } catch {
      return { ok: false, reason: "cross-origin-frame-ancestor", origins };
    }

    if (parent === top) {
      return { ok: true, origins };
    }
    current = parent;
  }

  return { ok: false, reason: "frame-ancestors-unverifiable", origins };
}

function elementType(element: HTMLInputElement): string {
  const attribute = safeGetAttribute(element, "type");
  const property = ownStringProperty(element, "type");
  return (attribute ?? property ?? "text").trim().toLowerCase();
}

function autocompleteTokens(element: HTMLInputElement): string[] {
  const value = safeGetAttribute(element, "autocomplete") ?? ownStringProperty(element, "autocomplete") ?? "";
  return value
    .toLowerCase()
    .split(/\s+/)
    .filter(Boolean);
}

function hasToken(element: HTMLInputElement, token: string): boolean {
  return autocompleteTokens(element).includes(token);
}

function isDisabled(element: HTMLInputElement): boolean {
  return ownBooleanProperty(element, "disabled") === true || hasAttribute(element, "disabled");
}

function isReadOnly(element: HTMLInputElement): boolean {
  return ownBooleanProperty(element, "readOnly") === true || hasAttribute(element, "readonly");
}

function isEligibleInput(element: HTMLInputElement, allowPassword: boolean): boolean {
  if (!element || typeof element !== "object") return false;
  const type = elementType(element);
  if (type === "hidden" || type === "submit" || type === "button" || type === "reset" ||
      type === "checkbox" || type === "radio" || type === "file" || type === "image") {
    return false;
  }
  if (!allowPassword && type === "password") return false;
  if (isDisabled(element) || isReadOnly(element)) return false;
  if (hasToken(element, "one-time-code") || hasToken(element, "new-password")) return false;
  return true;
}

function collectInputs(document: Document): HTMLInputElement[] {
  const inputs: HTMLInputElement[] = [];
  try {
    const nodes = document.querySelectorAll?.("input");
    if (nodes) {
      for (const node of Array.from(nodes) as HTMLInputElement[]) {
        inputs.push(node);
      }
    }
  } catch {
  }
  return inputs;
}

function resolveElement<T extends Element>(
  document: Document,
  value: T | string | undefined,
  expected: "form" | "input"
): T | string | null {
  if (value === undefined) return null;
  if (typeof value !== "string") {
    if (expected === "input" && !("value" in (value as object))) return null;
    return value;
  }
  try {
    const element = document.querySelector(value);
    if (!element) return null;
    if (expected === "input" && !("value" in element)) return null;
    return element as T;
  } catch {
    return null;
  }
}

function owningForm(field: HTMLInputElement): HTMLFormElement | null {
  try {
    if (field.form) return field.form;
  } catch {
  }
  try {
    if (typeof field.closest === "function") {
      return field.closest("form");
    }
  } catch {
  }
  return null;
}

function belongsToForm(field: HTMLInputElement, form: HTMLFormElement): boolean {
  try {
    if (field.form) return field.form === form;
  } catch {
    return false;
  }
  try {
    if (typeof form.contains === "function") return form.contains(field);
  } catch {
    return false;
  }
  return false;
}

function belongsToDocument(element: Element, document: Document): boolean {
  try {
    const owner = element.ownerDocument;
    if (owner) return owner === document;
  } catch {
    return false;
  }
  return true;
}

function inputName(element: HTMLInputElement): string {
  return (
    safeGetAttribute(element, "name") ??
    ownStringProperty(element, "name") ??
    safeGetAttribute(element, "id") ??
    ownStringProperty(element, "id") ??
    ""
  );
}

function usernameScore(element: HTMLInputElement): number {
  if (!isEligibleInput(element, false)) return 0;
  const type = elementType(element);
  const name = inputName(element).toLowerCase();
  const tokens = autocompleteTokens(element);

  if (tokens.includes("username")) return 100;
  if (tokens.includes("email")) return 90;
  if (type === "email") return 80;
  if (/^(user(name)?|login|email|account|identifier|uid)$/i.test(name)) return 70;
  if (/(user|login|email|account|identifier)/i.test(name)) return 60;
  return 0;
}

function textLikeInputs(inputs: HTMLInputElement[]): HTMLInputElement[] {
  return inputs.filter((input) => {
    if (!isEligibleInput(input, false)) return false;
    const type = elementType(input);
    return type === "text" || type === "email" || type === "tel" || type === "";
  });
}

function writeInputValue(input: HTMLInputElement, value: string): boolean {
  try {
    const prototype = Object.getPrototypeOf(input);
    const descriptor = prototype ? Object.getOwnPropertyDescriptor(prototype, "value") : undefined;
    if (descriptor?.set) {
      descriptor.set.call(input, value);
    } else {
      input.value = value;
    }
    if (input.value !== value) return false;
  } catch {
    return false;
  }

  try {
    const view = input.ownerDocument?.defaultView;
    const EventConstructor = view?.Event;
    if (typeof input.dispatchEvent === "function") {
      if (typeof EventConstructor === "function") {
        input.dispatchEvent(new EventConstructor("input", { bubbles: true, composed: true }));
        input.dispatchEvent(new EventConstructor("change", { bubbles: true }));
      } else {
        input.dispatchEvent({ type: "input", bubbles: true } as unknown as Event);
        input.dispatchEvent({ type: "change", bubbles: true } as unknown as Event);
      }
    }
  } catch {
  }
  return true;
}

function clearInputValue(input: HTMLInputElement): boolean {
  if (!writeInputValue(input, "")) {
    try {
      input.value = "";
    } catch {
      return false;
    }
  }
  try {
    return input.value === "";
  } catch {
    return false;
  }
}

function clearCredentialFields(
  passwordField: HTMLInputElement,
  usernameField: HTMLInputElement | null
): boolean {
  const passwordCleared = clearInputValue(passwordField);
  const usernameCleared = usernameField ? clearInputValue(usernameField) : true;
  return passwordCleared && usernameCleared;
}

function resolveSubmitAction(
  rawAction: string | null,
  form: HTMLFormElement,
  documentUrl: string,
  baseUrl: URL
): { url: URL | null; reason?: FillBlockReason } {
  let raw = rawAction;
  if (raw === null) {
    raw = ownStringProperty(form, "action");
  }

  const value = raw === null || raw.trim() === "" ? documentUrl : raw.trim();
  let url: URL | null = null;
  try {
    url = parseWebUrl(new URL(value, baseUrl));
  } catch {
    url = null;
  }
  if (!url) return { url: null, reason: "invalid-form-action" };
  if (url.username || url.password) return { url: null, reason: "invalid-form-action" };

  const propertyAction = ownStringProperty(form, "action");
  if (propertyAction !== null && propertyAction.trim() !== "") {
    let propertyUrl: URL | null = null;
    try {
      propertyUrl = parseWebUrl(new URL(propertyAction.trim(), baseUrl));
    } catch {
      propertyUrl = null;
    }
    if (!propertyUrl || propertyUrl.username || propertyUrl.password) {
      return { url: null, reason: "invalid-form-action" };
    }
    if (propertyUrl.origin !== url.origin) {
      return { url: null, reason: "form-action-mismatch" };
    }
  }

  return { url };
}

function actionOverrideControls(form: HTMLFormElement, document: Document): Element[] {
  const controls = new Set<Element>();
  const inside = form.querySelectorAll?.("button[formaction], input[formaction]");
  if (inside) {
    for (const control of Array.from(inside) as Element[]) controls.add(control);
  }

  const all = document.querySelectorAll?.("button[formaction], input[formaction]");
  if (all) {
    const formId = safeGetAttribute(form, "id");
    for (const control of Array.from(all) as Element[]) {
      if (controls.has(control)) continue;
      let associated = false;
      try {
        associated = (control as Element & { form?: unknown }).form === form;
      } catch {
        associated = false;
      }
      if (!associated && formId) {
        associated = safeGetAttribute(control, "form") === formId;
      }
      if (!associated) {
        associated = typeof form.contains === "function" && form.contains(control);
      }
      if (associated) controls.add(control);
    }
  }

  return Array.from(controls);
}

function validateFormAction(
  form: HTMLFormElement,
  document: Document,
  documentUrl: string,
  expectedOrigin: string,
  credential: SavedCredential,
  options: CredentialFillPolicyOptions
): { url: URL | null; reason?: FillBlockReason; observed?: string | null } {
  const baseUrl = readBaseUrl(document, documentUrl);
  if (!baseUrl) return { url: null, reason: "invalid-form-action" };
  const action = resolveSubmitAction(safeGetAttribute(form, "action"), form, documentUrl, baseUrl);
  if (!action.url) return { url: null, reason: action.reason ?? "form-action-unavailable" };
  if (action.url.origin !== expectedOrigin) {
    return { url: null, reason: "form-action-mismatch", observed: action.url.href };
  }
  if (!isAllowedTransport(action.url, options)) {
    return { url: null, reason: "insecure-origin", observed: action.url.href };
  }

  let pinned: URL | null = null;
  if (credential.formAction !== undefined) {
    try {
      pinned = parseWebUrl(credential.formAction);
      if (!pinned) {
        pinned = parseWebUrl(new URL(credential.formAction, `${expectedOrigin}/`));
      }
    } catch {
      pinned = null;
    }
    if (!pinned) {
      return { url: null, reason: "invalid-form-action-pin", observed: action.url.href };
    }
    if (pinned.origin !== expectedOrigin || !isAllowedTransport(pinned, options)) {
      return { url: null, reason: "invalid-form-action-pin", observed: action.url.href };
    }
    if (stripFragment(action.url) !== stripFragment(pinned)) {
      return { url: null, reason: "form-action-mismatch", observed: action.url.href };
    }
  }

  try {
    for (const control of actionOverrideControls(form, document)) {
      const rawOverride = safeGetAttribute(control, "formaction");
      if (rawOverride === null || rawOverride.trim() === "") continue;
      let overrideUrl: URL | null = null;
      try {
        overrideUrl = parseWebUrl(new URL(rawOverride.trim(), baseUrl));
      } catch {
        overrideUrl = null;
      }
      if (!overrideUrl || overrideUrl.username || overrideUrl.password) {
        return { url: null, reason: "invalid-form-action", observed: action.url.href };
      }
      if (overrideUrl.origin !== expectedOrigin) {
        return { url: null, reason: "form-action-mismatch", observed: action.url.href };
      }
      if (!isAllowedTransport(overrideUrl, options)) {
        return { url: null, reason: "insecure-origin", observed: action.url.href };
      }
      if (pinned && stripFragment(overrideUrl) !== stripFragment(pinned)) {
        return { url: null, reason: "form-action-mismatch", observed: action.url.href };
      }
    }
  } catch {
    return { url: null, reason: "form-action-unavailable", observed: action.url.href };
  }

  return { url: action.url };
}

function readActivation(
  request: CredentialFillRequest,
  document: Document
): boolean | null {
  if (Object.prototype.hasOwnProperty.call(request, "userActivation")) {
    const activation = request.userActivation;
    if (activation && typeof activation.isActive === "boolean") return activation.isActive;
    return null;
  }

  try {
    const activation = document.defaultView?.navigator?.userActivation;
    if (activation && typeof activation.isActive === "boolean") return activation.isActive;
  } catch {
    return null;
  }
  return null;
}

function gestureType(gesture: CredentialFillRequest["gesture"]): string | null {
  const type = ownStringProperty(gesture, "type");
  return type && type.length > 0 ? type : null;
}

function gestureTrusted(
  gesture: CredentialFillRequest["gesture"],
  document: Document
): boolean {
  if (!gesture) return false;
  try {
    if (gesture.isTrusted !== true) return false;
    const EventConstructor = document.defaultView?.Event;
    if (typeof EventConstructor === "function" && !(gesture instanceof EventConstructor)) {
      return false;
    }
    return true;
  } catch {
    return false;
  }
}

/**
 * Stateful credential fill policy. Each attempt, successful or blocked, creates
 * an immutable audit record.
 */
export class CredentialFillPolicy {
  private readonly options: CredentialFillPolicyOptions;
  private readonly records: FillAuditRecord[] = [];
  private sequence = 0;

  constructor(options: CredentialFillPolicyOptions = {}) {
    this.options = { ...options };
  }

  fill(request: CredentialFillRequest): FillResult {
    const state: MutableAttempt = {
      credentialId: null,
      expectedOrigin: null,
      documentOrigin: null,
      ancestorOrigins: [],
      frameAncestorsVerified: false,
      formAction: null,
      gesture: {
        type: null,
        trusted: false,
        active: null
      },
      fields: { username: false, password: false }
    };

    const finish = (
      decision: "filled" | "blocked",
      reason: FillReason,
      message?: string
    ): FillResult => {
      const finalMessage = message ?? MESSAGES[reason];
      const audit = this.record(state, decision, reason, finalMessage);
      return {
        ok: decision === "filled",
        decision,
        reason,
        message: finalMessage,
        credentialId: state.credentialId,
        audit
      };
    };

    try {
      state.gesture.type = gestureType(request?.gesture);
      const requestDocument = request?.document;
      if (requestDocument && typeof requestDocument === "object") {
        state.gesture.trusted = gestureTrusted(request?.gesture, requestDocument);
        state.gesture.active = readActivation(request, requestDocument);
      }
      const sourceCredential = request?.credential;
      if (
        !request ||
        !sourceCredential ||
        typeof sourceCredential.origin !== "string" ||
        typeof sourceCredential.username !== "string" ||
        typeof sourceCredential.password !== "string" ||
        sourceCredential.password.length === 0
      ) {
        return finish("blocked", "invalid-credential");
      }
      const credentialId =
        typeof sourceCredential.id === "string" && sourceCredential.id.length > 0
          ? sourceCredential.id
          : null;
      const credential: SavedCredential = {
        origin: sourceCredential.origin,
        username: sourceCredential.username,
        password: sourceCredential.password,
        ...(credentialId ? { id: credentialId } : {}),
        ...(typeof sourceCredential.formAction === "string"
          ? { formAction: sourceCredential.formAction }
          : {})
      };
      state.credentialId = credentialId;

      const expectedUrl = parseWebUrl(credential.origin);
      if (!expectedUrl || expectedUrl.username || expectedUrl.password) {
        return finish("blocked", "invalid-credential-origin");
      }
      state.expectedOrigin = expectedUrl.origin;

      if (!isAllowedTransport(expectedUrl, this.options)) {
        return finish("blocked", "insecure-origin");
      }

      const document = request.document;
      if (!document || typeof document !== "object") {
        return finish("blocked", "document-url-unavailable");
      }

      const actualDocumentUrl = readDocumentUrl(document);
      if (!actualDocumentUrl) {
        return finish("blocked", "document-url-unavailable");
      }
      const documentUrl = parseWebUrl(actualDocumentUrl);
      if (!documentUrl || documentUrl.username || documentUrl.password) {
        return finish("blocked", "invalid-document-origin");
      }
      const locationOrigin = readLocationOrigin(document);
      if (locationOrigin === "null" || (locationOrigin !== null && locationOrigin !== documentUrl.origin)) {
        return finish("blocked", "invalid-document-origin");
      }
      state.documentOrigin = documentUrl.origin;

      if (request.frame?.documentUrl !== undefined) {
        const declaredUrl = parseWebUrl(request.frame.documentUrl);
        if (
          !declaredUrl ||
          declaredUrl.username ||
          declaredUrl.password ||
          declaredUrl.origin !== documentUrl.origin
        ) {
          return finish("blocked", "frame-context-mismatch");
        }
      }

      if (documentUrl.origin !== state.expectedOrigin) {
        return finish("blocked", "document-origin-mismatch");
      }

      if (!isAllowedTransport(documentUrl, this.options)) {
        return finish("blocked", "insecure-origin");
      }

      const ancestors = resolveAncestors(document, request.frame);
      state.ancestorOrigins = [...ancestors.origins];
      state.frameAncestorsVerified = ancestors.ok;
      if (!ancestors.ok) {
        return finish("blocked", ancestors.reason ?? "frame-ancestors-unverifiable");
      }
      for (const ancestor of ancestors.origins) {
        if (ancestor !== state.expectedOrigin) {
          return finish("blocked", "cross-origin-frame-ancestor");
        }
      }

      const active = readActivation(request, document);
      state.gesture.active = active;
      if (!request.gesture) {
        return finish("blocked", "user-gesture-required");
      }
      state.gesture.trusted = gestureTrusted(request.gesture, document);
      if (!state.gesture.trusted) {
        return finish("blocked", "untrusted-user-gesture");
      }
      if (
        this.options.requireActiveUserActivation !== false &&
        active === false
      ) {
        return finish("blocked", "user-activation-inactive");
      }

      const inputs = collectInputs(document);

      let explicitPassword: HTMLInputElement | null = null;
      if (request.passwordField !== undefined) {
        const resolved = resolveElement<HTMLInputElement>(document, request.passwordField, "input");
        if (!resolved || typeof resolved === "string") {
          return finish("blocked", "password-field-not-found");
        }
        explicitPassword = resolved;
        if (!belongsToDocument(explicitPassword, document)) {
          return finish("blocked", "password-field-not-fillable");
        }
        if (elementType(explicitPassword) !== "password") {
          return finish("blocked", "password-field-not-fillable");
        }
        if (!isEligibleInput(explicitPassword, true) || hasToken(explicitPassword, "new-password")) {
          return finish("blocked", "password-field-not-fillable");
        }
      }

      let explicitForm: HTMLFormElement | null = null;
      if (request.form !== undefined) {
        const resolved = resolveElement<HTMLFormElement>(document, request.form, "form");
        if (!resolved || typeof resolved === "string" || !("action" in resolved || "getAttribute" in resolved)) {
          return finish("blocked", "invalid-form");
        }
        explicitForm = resolved;
        if (!belongsToDocument(explicitForm, document)) {
          return finish("blocked", "invalid-form");
        }
      }

      let passwordField: HTMLInputElement | null = explicitPassword;
      let passwordCandidates: HTMLInputElement[] = [];
      if (!passwordField) {
        passwordCandidates = inputs.filter(
          (input) =>
            elementType(input) === "password" &&
            isEligibleInput(input, true) &&
            !hasToken(input, "new-password")
        );
        if (explicitForm) {
          passwordCandidates = passwordCandidates.filter((input) =>
            belongsToForm(input, explicitForm as HTMLFormElement)
          );
        }
        if (passwordCandidates.length === 0) {
          return finish("blocked", "password-field-not-found");
        }
        if (passwordCandidates.length > 1) {
          const currentPassword = passwordCandidates.filter((input) =>
            hasToken(input, "current-password")
          );
          if (currentPassword.length === 1) {
            passwordCandidates = currentPassword;
          } else {
            return finish("blocked", "ambiguous-password-field");
          }
        }
        passwordField = passwordCandidates[0] ?? null;
      }
      if (!passwordField) {
        return finish("blocked", "password-field-not-found");
      }

      const form = explicitForm ?? owningForm(passwordField);
      if (!form) {
        return finish("blocked", "form-not-found");
      }
      if (!belongsToForm(passwordField, form)) {
        return finish("blocked", "field-form-mismatch");
      }
      if (explicitForm && explicitPassword && !belongsToForm(explicitPassword, explicitForm)) {
        return finish("blocked", "field-form-mismatch");
      }

      let usernameField: HTMLInputElement | null = null;
      if (request.usernameField !== undefined) {
        const resolved = resolveElement<HTMLInputElement>(document, request.usernameField, "input");
        if (!resolved || typeof resolved === "string") {
          return finish("blocked", "username-field-not-fillable");
        }
        usernameField = resolved;
        if (
          usernameField === passwordField ||
          elementType(usernameField) === "password" ||
          !isEligibleInput(usernameField, false)
        ) {
          return finish("blocked", "username-field-not-fillable");
        }
        if (!belongsToDocument(usernameField, document)) {
          return finish("blocked", "username-field-not-fillable");
        }
        if (!belongsToForm(usernameField, form)) {
          return finish("blocked", "field-form-mismatch");
        }
      } else {
        let formInputs: HTMLInputElement[] = [];
        try {
          const nodes = form.querySelectorAll?.("input");
          if (nodes) {
            formInputs = (Array.from(nodes) as HTMLInputElement[]).filter((input) =>
              belongsToForm(input, form)
            );
          }
        } catch {
          formInputs = [];
        }
        if (formInputs.length === 0) {
          formInputs = inputs.filter((input) => belongsToForm(input, form));
        }

        const scored = formInputs
          .filter((input) => input !== passwordField)
          .map((input) => ({ input, score: usernameScore(input) }))
          .filter((entry) => entry.score > 0)
          .sort((left, right) => right.score - left.score);
        usernameField = scored[0]?.input ?? null;

        if (!usernameField) {
          const textInputs = textLikeInputs(formInputs).filter((input) => input !== passwordField);
          if (textInputs.length === 1) usernameField = textInputs[0] ?? null;
        }
      }

      const action = validateFormAction(
        form,
        document,
        documentUrl.href,
        state.expectedOrigin,
        credential,
        this.options
      );
      if (!action.url) {
        state.formAction = action.observed ?? null;
        return finish("blocked", action.reason ?? "form-action-unavailable");
      }
      state.formAction = action.url.href;

      const usernameWritten = usernameField
        ? writeInputValue(usernameField, credential.username)
        : false;
      if (usernameField && !usernameWritten) {
        return finish("blocked", "fill-failed");
      }
      if (usernameField && usernameWritten) {
        const afterUsername = validateFormAction(
          form,
          document,
          documentUrl.href,
          state.expectedOrigin,
          credential,
          this.options
        );
        if (!afterUsername.url) {
          state.formAction = afterUsername.observed ?? null;
          state.fields.username = true;
          const cleared = clearInputValue(usernameField);
          if (cleared) state.fields.username = false;
          return finish("blocked", cleared ? "form-action-changed" : "credential-cleanup-failed");
        }
        state.formAction = afterUsername.url.href;
      }
      if (!writeInputValue(passwordField, credential.password)) {
        state.fields.username = Boolean(usernameField && usernameWritten);
        state.fields.password = true;
        const cleared = usernameField && usernameWritten
          ? clearCredentialFields(passwordField, usernameField)
          : clearInputValue(passwordField);
        if (cleared) {
          state.fields.username = false;
          state.fields.password = false;
        }
        return finish("blocked", cleared ? "fill-failed" : "credential-cleanup-failed");
      }

      const recheckedAction = validateFormAction(
        form,
        document,
        documentUrl.href,
        state.expectedOrigin,
        credential,
        this.options
      );
      if (!recheckedAction.url) {
        state.formAction = recheckedAction.observed ?? null;
        state.fields.username = Boolean(usernameField && usernameWritten);
        state.fields.password = true;
        const cleared = clearCredentialFields(passwordField, usernameField);
        if (cleared) {
          state.fields.username = false;
          state.fields.password = false;
        }
        return finish("blocked", cleared ? "form-action-changed" : "credential-cleanup-failed");
      }

      state.fields.username = Boolean(usernameField && usernameWritten);
      state.fields.password = true;
      state.formAction = recheckedAction.url.href;
      return finish("filled", "ok");
    } catch {
      return finish("blocked", "unexpected-error");
    }
  }

  /** Returns a snapshot of the immutable audit records. */
  getAuditLog(): readonly FillAuditRecord[] {
    return Object.freeze(this.records.slice());
  }

  clearAuditLog(): void {
    this.records.length = 0;
  }

  private record(
    state: MutableAttempt,
    decision: "filled" | "blocked",
    reason: FillReason,
    message: string
  ): FillAuditRecord {
    this.sequence += 1;
    let timestamp: string;
    try {
      const now = this.options.now ? this.options.now() : new Date();
      timestamp = now.toISOString();
    } catch {
      timestamp = new Date(0).toISOString();
    }

    const audit: FillAuditRecord = Object.freeze({
      sequence: this.sequence,
      timestamp,
      event: "credential-fill",
      decision,
      reason,
      message,
      credentialId: state.credentialId,
      expectedOrigin: state.expectedOrigin,
      documentOrigin: state.documentOrigin,
      ancestorOrigins: Object.freeze([...state.ancestorOrigins]),
      frameAncestorsVerified: state.frameAncestorsVerified,
      formAction: state.formAction,
      gesture: Object.freeze({ ...state.gesture }),
      fields: Object.freeze({ ...state.fields })
    });

    const maximum = this.options.maxAuditRecords ?? DEFAULT_MAX_AUDIT_RECORDS;
    if (maximum > 0 && this.records.length >= maximum) {
      this.records.splice(0, this.records.length - maximum + 1);
    }
    this.records.push(audit);

    if (this.options.onAudit) {
      try {
        this.options.onAudit(audit);
      } catch {
      }
    }
    return audit;
  }
}

export function createCredentialFillPolicy(
  options: CredentialFillPolicyOptions = {}
): CredentialFillPolicy {
  return new CredentialFillPolicy(options);
}

/** Reads the browser's frame context without trusting page-provided values. */
export function captureFrameContext(view: Window): FrameContext {
  const context: { documentUrl: string; ancestorOrigins?: string[] } = {
    documentUrl: view.location.href
  };
  try {
    const origins = (view.location as Location & {
      readonly ancestorOrigins?: ArrayLike<string>;
    }).ancestorOrigins;
    const listed = collectListedOrigins(origins);
    if (listed) context.ancestorOrigins = listed;
  } catch {
  }
  return context;
}
