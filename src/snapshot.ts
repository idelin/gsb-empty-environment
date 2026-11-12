import type { ButtonOverride, FormEncoding, FormSnapshot, HttpMethod } from "./types.ts";
import { SubmitError } from "./types.ts";
import { parseHttpOrigin, resolveAction } from "./url.ts";

const METHOD_ALIASES: Record<string, HttpMethod> = {
  "": "GET",
  get: "GET",
  post: "POST",
};

const ENCODING_ALIASES: Record<string, FormEncoding> = {
  "": "application/x-www-form-urlencoded",
  "application/x-www-form-urlencoded": "application/x-www-form-urlencoded",
  "multipart/form-data": "multipart/form-data",
  "text/plain": "text/plain",
};

/** HTML normalizes method names case-insensitively; anything else falls back to GET. */
export function normalizeMethod(raw: string): HttpMethod {
  return METHOD_ALIASES[String(raw ?? "").trim().toLowerCase()] ?? "GET";
}

export function normalizeEnctype(raw: string): FormEncoding {
  return (
    ENCODING_ALIASES[String(raw ?? "").trim().toLowerCase()] ??
    "application/x-www-form-urlencoded"
  );
}

export interface ResolvedSubmission {
  actionUrl: URL;
  method: HttpMethod;
  enctype: FormEncoding;
  submitter: Readonly<ButtonOverride> | null;
}

/**
 * Resolve effective form attributes. Button-level overrides are merged here
 * (and only here), so a submitter's formaction/formmethod/formenctype always
 * participates in the exact same approval decision as the form attributes.
 */
export function resolveSubmission(
  snapshot: FormSnapshot,
  submitter: ButtonOverride | null,
): ResolvedSubmission {
  if (typeof snapshot.documentGeneration !== "number" || !Number.isSafeInteger(snapshot.documentGeneration)) {
    throw new SubmitError(
      "INVALID_SNAPSHOT",
      "documentGeneration must be a safe integer",
    );
  }
  parseHttpOrigin(snapshot.origin);

  const baseHref = snapshot.baseUrl === "" ? snapshot.origin : snapshot.baseUrl;
  const actionUrl = resolveAction(baseHref, snapshot.action === undefined ? "" : String(snapshot.action));
  if (actionUrl.protocol !== "http:" && actionUrl.protocol !== "https:") {
    throw new SubmitError(
      "UNSAFE_ACTION_URL",
      `resolved action must be http(s): ${actionUrl.href}`,
    );
  }

  let method = normalizeMethod(snapshot.method);
  let enctype = normalizeEnctype(snapshot.enctype);

  let frozenSubmitter: Readonly<ButtonOverride> | null = null;
  if (submitter) {
    const normalized: ButtonOverride = {};
    if (submitter.name !== undefined) normalized.name = String(submitter.name);
    if (submitter.value !== undefined) normalized.value = String(submitter.value);
    if (submitter.formAction !== undefined) {
      normalized.formAction = String(submitter.formAction);
    }
    if (submitter.formMethod !== undefined) {
      normalized.formMethod = String(submitter.formMethod);
    }
    if (submitter.formEnctype !== undefined) {
      normalized.formEnctype = String(submitter.formEnctype);
    }
    frozenSubmitter = Object.freeze(normalized);

    if (frozenSubmitter.formAction !== undefined) {
      const overridden = resolveAction(baseHref, frozenSubmitter.formAction);
      if (overridden.protocol !== "http:" && overridden.protocol !== "https:") {
        throw new SubmitError(
          "UNSAFE_ACTION_URL",
          `button formaction must be http(s): ${overridden.href}`,
        );
      }
      actionUrl.href = overridden.href;
    }
    if (frozenSubmitter.formMethod !== undefined && frozenSubmitter.formMethod !== "") {
      method = normalizeMethod(frozenSubmitter.formMethod);
    }
    if (frozenSubmitter.formEnctype !== undefined && frozenSubmitter.formEnctype !== "") {
      enctype = normalizeEnctype(frozenSubmitter.formEnctype);
    }
  }

  return { actionUrl, method, enctype, submitter: frozenSubmitter };
}

/** Deep-freeze helper for the approved plan. */
export function deepFreeze<T>(value: T): T {
  if (value === null || typeof value !== "object" || Object.isFrozen(value)) {
    return value;
  }
  Object.freeze(value);
  for (const key of Reflect.ownKeys(value as object)) {
    deepFreeze((value as Record<string | symbol, unknown>)[key]);
  }
  return value;
}
