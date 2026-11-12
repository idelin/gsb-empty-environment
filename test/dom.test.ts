import { describe, it } from "node:test";
import assert from "node:assert/strict";

import {
  captureSnapshot,
  collectEntries,
  readButtonOverride,
} from "../src/dom.ts";
import type { DomDocument, DomForm, DomFormControl } from "../src/dom.ts";

class FakeElement {
  private readonly attrs: Map<string, string>;

  constructor(attrs: Record<string, string> = {}) {
    this.attrs = new Map(Object.entries(attrs));
  }

  getAttribute(name: string): string | null {
    return this.attrs.has(name) ? (this.attrs.get(name) as string) : null;
  }
}

function control(partial: Partial<DomFormControl>): DomFormControl {
  return { name: "", value: "", ...partial };
}

function fakeForm(elements: DomFormControl[], attrs: Record<string, string> = {}): DomForm {
  const form = new FakeElement(attrs) as unknown as DomForm & {
    elements: DomFormControl[];
  };
  form.elements = elements;
  return form as DomForm;
}

describe("collectEntries", () => {
  it("skips disabled, nameless and button controls", () => {
    const form = fakeForm([
      control({ name: "a", value: "1" }),
      control({ name: "b", value: "2", disabled: true }),
      control({ name: "", value: "ignored" }),
      control({ name: "go", value: "submit", type: "submit" }),
    ]);

    assert.deepEqual(collectEntries(form), [{ name: "a", value: "1" }]);
  });

  it("includes file controls as blob entries", () => {
    const blob = new Blob(["data"]);
    const form = fakeForm([
      control({
        name: "upload",
        type: "file",
        files: [{ name: "a.txt", type: "text/plain", blob }],
      }),
    ]);

    assert.deepEqual(collectEntries(form), [
      { name: "upload", value: blob, filename: "a.txt" },
    ]);
  });
});

describe("captureSnapshot and readButtonOverride", () => {
  it("copies raw attributes plus the live base and generation", () => {
    const document: DomDocument = {
      generation: 7,
      location: { origin: "https://app.example.test", href: "https://app.example.test/form" },
      baseURI: "https://cdn.example.test/base/",
    };
    const form = new FakeElement({
      action: "/pay",
      method: "POST",
      enctype: "multipart/form-data",
    }) as unknown as DomForm;

    const snapshot = captureSnapshot(document, form);
    assert.equal(snapshot.documentGeneration, 7);
    assert.equal(snapshot.baseUrl, "https://cdn.example.test/base/");
    assert.equal(snapshot.action, "/pay");
    assert.equal(snapshot.method, "POST");
    assert.equal(snapshot.enctype, "multipart/form-data");

    const button = new FakeElement({
      name: "express",
      value: "1",
      formaction: "/express",
      formmethod: "get",
      formenctype: "text/plain",
    });
    assert.deepEqual(readButtonOverride(button), {
      name: "express",
      value: "1",
      formAction: "/express",
      formMethod: "get",
      formEnctype: "text/plain",
    });
  });
});
