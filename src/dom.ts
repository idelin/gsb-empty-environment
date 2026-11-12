import type { ButtonOverride, FormEntry, FormSnapshot, PolicyContext, TargetPolicy } from "./types.ts";

/**
 * Minimal structural DOM types. The package does not depend on the `dom`
 * TypeScript library, so hosts compile it in any environment.
 */
export interface DomElement {
  getAttribute(name: string): string | null;
}

export interface DomForm extends DomElement {
  elements: ArrayLike<DomFormControl> | Iterable<DomFormControl>;
}

export interface DomFormControl {
  type?: string;
  name?: string;
  value?: string;
  disabled?: boolean;
  files?: ArrayLike<DomFile> | null;
}

export interface DomFile {
  name: string;
  type: string;
  blob: Blob;
}

export interface DomDocument {
  generation: number;
  location: { origin: string; href: string };
  baseURI: string;
}

function attr(element: DomElement | null | undefined, name: string): string {
  return element?.getAttribute(name) ?? "";
}

const SUBMITTABLE_TYPES = new Set(["submit", "image", "reset", "button"]);

/**
 * Collect successful form controls using the HTML construction algorithm.
 * The field *values* are read here at approval capture time; field names are
 * therefore fixed alongside the target, matching the pinned-plan contract.
 */
export function collectEntries(form: DomForm): FormEntry[] {
  const entries: FormEntry[] = [];
  const controls = Array.from(form.elements as Iterable<DomFormControl>);

  for (const control of controls) {
    if (control.disabled || !control.name) {
      continue;
    }
    const type = (control.type ?? "").toLowerCase();
    if (SUBMITTABLE_TYPES.has(type) || type === "file") {
      if (type === "file" && control.files && control.files.length > 0) {
        for (const file of Array.from(control.files as Iterable<DomFile>)) {
          entries.push({ name: control.name, value: file.blob, filename: file.name });
        }
      }
      continue;
    }
    entries.push({ name: control.name, value: control.value ?? "" });
  }

  return entries;
}

/** Read a submit button's form-level overrides as a plain frozen record. */
export function readButtonOverride(button: DomElement): ButtonOverride {
  const override: ButtonOverride = {};
  const name = attr(button, "name");
  const value = attr(button, "value");
  const formAction = attr(button, "formaction");
  const formMethod = attr(button, "formmethod");
  const formEnctype = attr(button, "formenctype");
  if (name) override.name = name;
  if (value) override.value = value;
  if (formAction) override.formAction = formAction;
  if (formMethod) override.formMethod = formMethod;
  if (formEnctype) override.formEnctype = formEnctype;
  return Object.freeze(override);
}

/** Capture the immutable snapshot required by {@link createSubmitPlan}. */
export function captureSnapshot(document: DomDocument, form: DomForm): FormSnapshot {
  return {
    documentGeneration: document.generation,
    origin: document.location.origin,
    baseUrl: document.baseURI || document.location.href,
    action: attr(form, "action"),
    method: attr(form, "method"),
    enctype: attr(form, "enctype"),
    entries: collectEntries(form),
  };
}

/** Convenience policy for hosts that allow an explicit set of target origins. */
export class OriginAllowlistPolicy implements TargetPolicy {
  private readonly allowed: ReadonlySet<string>;

  constructor(allowedOrigins: readonly string[]) {
    this.allowed = new Set(allowedOrigins.map((origin) => new URL(origin).origin));
  }

  allow(context: PolicyContext): boolean {
    return this.allowed.has(context.targetOrigin);
  }
}
