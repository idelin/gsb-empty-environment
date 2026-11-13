import assert from "node:assert/strict";
import test from "node:test";
import { createHmac } from "node:crypto";

import {
  NotebookTrustService,
  canonicalizeNotebook,
  computeNotebookSignature,
  createNotebookTrustService,
  openNotebook,
  sanitizeNotebookOutputs,
  saveNotebook,
  verifyNotebookSignature,
  type Notebook,
  type NotebookOutput,
} from "../index.ts";

const SECRET = "test-server-secret-with-enough-entropy";

function clone<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T;
}

function notebookWithOutputs(
  outputs: NotebookOutput[],
  source = "print('hello')",
): Notebook {
  return {
    nbformat: 4,
    nbformat_minor: 5,
    metadata: { title: "external notebook" },
    cells: [
      {
        cell_type: "code",
        execution_count: 1,
        metadata: {},
        source: [source],
        outputs,
      },
    ],
  };
}

test("canonical JSON sorts keys, strips metadata.signature, and has no formatting whitespace", () => {
  const first: Notebook = {
    metadata: { title: "x", signature: "old-signature" },
    nbformat: 4,
    cells: [
      {
        outputs: [
          {
            output_type: "execute_result",
            data: { "text/plain": "2" },
          },
        ],
        source: "1+1",
        cell_type: "code",
      },
    ],
  };

  const reordered: Notebook = {
    cells: [
      {
        cell_type: "code",
        source: "1+1",
        outputs: [
          {
            data: { "text/plain": "2" },
            output_type: "execute_result",
          },
        ],
      },
    ],
    nbformat: 4,
    metadata: { title: "x", signature: "a-different-old-signature" },
  };

  assert.equal(
    canonicalizeNotebook(first),
    '{"cells":[{"cell_type":"code","outputs":[{"data":{"text/plain":"2"},"output_type":"execute_result"}],"source":"1+1"}],"metadata":{"title":"x"},"nbformat":4}',
  );
  assert.equal(canonicalizeNotebook(first), canonicalizeNotebook(reordered));
  assert.equal(
    computeNotebookSignature(first, SECRET),
    computeNotebookSignature(reordered, SECRET),
  );
});

test("signature is HMAC-SHA256 over the canonical notebook JSON", () => {
  const notebook = notebookWithOutputs([
    {
      output_type: "display_data",
      data: { "text/plain": "plain only" },
    },
  ]);
  const canonical = canonicalizeNotebook(notebook);
  const expected = createHmac("sha256", SECRET)
    .update(canonical)
    .digest("hex");

  assert.equal(computeNotebookSignature(notebook, SECRET), expected);
  assert.equal(verifyNotebookSignature(notebook, SECRET, expected), true);
  assert.equal(
    verifyNotebookSignature(
      notebookWithOutputs([
        {
          output_type: "display_data",
          data: { "text/plain": "tampered" },
        },
      ]),
      SECRET,
      expected,
    ),
    false,
  );
});

test("forged output metadata trusted cannot make an unregistered notebook trusted", () => {
  const forged: Notebook = notebookWithOutputs([
    {
      output_type: "display_data",
      data: {
        "text/html": "<script>alert('xss')</script>",
        "application/javascript": "alert('xss')",
        "image/svg+xml": "<svg onload=\"alert('xss')\"></svg>",
      },
      metadata: { trusted: true, isolated: true },
    },
  ]);

  const registry = new Set<string>();
  const opened = openNotebook(forged, SECRET, registry);

  assert.equal(opened.trusted, false);
  assert.equal(opened.sanitized, true);
  assert.equal(registry.size, 0);
  assert.deepEqual(opened.notebook.cells?.[0]?.outputs, []);
  assert.deepEqual(opened.removedMimeTypes, [
    "application/javascript",
    "image/svg+xml",
    "text/html",
  ]);
  assert.equal(opened.removedOutputs, 1);
  assert.equal(canonicalizeNotebook(forged).includes("trusted"), true);
});

test("plain text output is preserved but its trusted metadata is ignored", () => {
  const plain: Notebook = notebookWithOutputs([
    {
      output_type: "display_data",
      data: { "text/plain": "safe plain text" },
      metadata: { trusted: true },
    },
    {
      output_type: "stream",
      name: "stdout",
      text: ["plain stream output"],
    },
  ]);

  const opened = openNotebook(plain, SECRET, new Set());
  const outputs = opened.notebook.cells?.[0]?.outputs ?? [];

  assert.equal(opened.trusted, false);
  assert.equal(outputs.length, 2);
  assert.deepEqual(outputs[0]?.data, { "text/plain": "safe plain text" });
  assert.equal(outputs[0]?.metadata, undefined);
  assert.deepEqual(outputs[1]?.text, ["plain stream output"]);
});

test("save registers a recomputed signature and later opens the saved notebook as trusted", () => {
  const executed: Notebook = notebookWithOutputs([
    {
      output_type: "display_data",
      data: {
        "text/html": "<strong>rendered after explicit execution</strong>",
        "text/plain": "rendered",
      },
    },
  ]);

  const registry = new Set<string>();
  const saved = saveNotebook(executed, SECRET, registry);

  assert.equal(registry.has(saved.signature), true);
  assert.equal(saved.notebook.metadata?.signature, saved.signature);

  const reopened = openNotebook(saved.notebook, SECRET, registry);
  assert.equal(reopened.trusted, true);
  assert.equal(reopened.sanitized, false);
  assert.deepEqual(reopened.notebook.cells?.[0]?.outputs?.[0]?.data, {
    "text/html": "<strong>rendered after explicit execution</strong>",
    "text/plain": "rendered",
  });
});

test("changing content after save invalidates the registered signature and strips dangerous output", () => {
  const executed: Notebook = notebookWithOutputs(
    [
      {
        output_type: "display_data",
        data: {
          "text/html": "<strong>original</strong>",
          "text/plain": "original",
        },
      },
    ],
    "print('original')",
  );

  const registry = new Set<string>();
  const saved = saveNotebook(executed, SECRET, registry);
  const changedCode = clone(saved.notebook);
  const changedOutput = clone(saved.notebook);

  changedCode.cells![0]!.source = ["fetch('https://attacker.example')"];
  changedOutput.cells![0]!.outputs![0]!.data!["text/plain"] = "changed text";

  assert.equal(openNotebook(changedCode, SECRET, registry).trusted, false);
  assert.equal(openNotebook(changedOutput, SECRET, registry).trusted, false);
  assert.deepEqual(
    openNotebook(changedCode, SECRET, registry).notebook.cells?.[0]?.outputs?.[0]
      ?.data,
    { "text/plain": "original" },
  );
  assert.equal(
    openNotebook(changedCode, SECRET, registry).notebook.cells?.[0]?.outputs?.[0]
      ?.data?.["text/html"],
    undefined,
  );
});

test("an embedded signature is never used for trust or registration", () => {
  const benign = notebookWithOutputs([
    {
      output_type: "display_data",
      data: { "text/plain": "benign" },
    },
  ]);
  const registry = new Set<string>();
  const saved = saveNotebook(benign, SECRET, registry);
  const oldSignature = saved.signature;

  const malicious = notebookWithOutputs([
    {
      output_type: "display_data",
      data: { "text/html": "<img src=x onerror=alert(1)>" },
    },
  ]);
  malicious.metadata = {
    title: "external notebook",
    signature: oldSignature,
  };

  const opened = openNotebook(malicious, SECRET, registry);
  assert.equal(opened.trusted, false);
  assert.equal(opened.signature, computeNotebookSignature(malicious, SECRET));
  assert.equal(opened.signature === oldSignature, false);
  assert.equal(registry.size, 1);
  assert.equal(openNotebook(malicious, SECRET, new Set([oldSignature])).trusted, false);
  assert.deepEqual(opened.notebook.cells?.[0]?.outputs, []);
});

test("saving ignores a file-provided signature and registers only the recomputed value", () => {
  const attackerSignature = "0123456789abcdef";
  const malicious = notebookWithOutputs([
    {
      output_type: "display_data",
      data: { "text/html": "<script>alert(1)</script>" },
    },
  ]);
  malicious.metadata = {
    title: "external notebook",
    signature: attackerSignature,
    trusted: true,
  };

  const registry = new Set<string>();
  const saved = saveNotebook(malicious, SECRET, registry);

  assert.notEqual(saved.signature, attackerSignature);
  assert.equal(registry.has(attackerSignature), false);
  assert.equal(registry.has(saved.signature), true);
  assert.equal(saved.notebook.metadata?.signature, saved.signature);
});

test("sanitizing a mixed output keeps safe MIME representations only", () => {
  const mixed = notebookWithOutputs([
    {
      output_type: "display_data",
      data: {
        "text/html": "<b>unsafe</b>",
        "application/javascript": "unsafe()",
        "image/svg+xml": "<svg/>",
        "text/plain": "safe fallback",
        "image/png": "base64-data",
      },
      metadata: { trusted: true, width: 100 },
    },
  ]);

  const sanitized = sanitizeNotebookOutputs(mixed);
  const output = sanitized.notebook.cells?.[0]?.outputs?.[0];

  assert.deepEqual(output?.data, {
    "text/plain": "safe fallback",
    "image/png": "base64-data",
  });
  assert.deepEqual(output?.metadata, { width: 100 });
  assert.equal(sanitized.removedOutputs, 0);
});

test("the service supports the execute-and-save then reopen workflow", () => {
  const service = createNotebookTrustService(SECRET);
  service.save(
    notebookWithOutputs([
      {
        output_type: "display_data",
        data: { "text/html": "<em>trusted</em>" },
      },
    ]),
  );

  const saved = service.save(
    notebookWithOutputs([
      {
        output_type: "display_data",
        data: { "text/html": "<em>trusted after save</em>" },
      },
    ]),
  );

  assert.equal(service.open(saved.notebook).trusted, true);
  assert.equal(service.registeredSignatures.size, 2);

  const changed = clone(saved.notebook);
  changed.cells![0]!.source = ["changed()"];
  assert.equal(service.open(changed).trusted, false);
});

test("a string notebook input is parsed before signing and opening", () => {
  const notebook = notebookWithOutputs([
    {
      output_type: "display_data",
      data: { "text/plain": "from JSON" },
    },
  ]);
  const registry = new Set<string>();
  saveNotebook(JSON.stringify(notebook), SECRET, registry);

  assert.equal(openNotebook(JSON.stringify(notebook), SECRET, registry).trusted, true);
  assert.equal(
    canonicalizeNotebook(JSON.stringify(notebook)),
    canonicalizeNotebook(notebook),
  );
});

test("the exported service class and factory produce the same decisions", () => {
  const notebook = notebookWithOutputs([
    {
      output_type: "display_data",
      data: { "text/html": "<span>ok</span>" },
    },
  ]);
  const direct = new NotebookTrustService(SECRET);
  const factory = createNotebookTrustService(SECRET);

  direct.save(notebook);
  factory.save(notebook);

  assert.equal(direct.open(notebook).trusted, true);
  assert.equal(factory.open(notebook).trusted, true);
});

test("options objects work and saving registers into a mutable array registry", () => {
  const notebook = notebookWithOutputs([
    {
      output_type: "display_data",
      data: { "text/plain": "array registry" },
    },
  ]);
  const registry: string[] = [];

  const saved = saveNotebook(notebook, {
    secret: SECRET,
    signatures: registry,
  });

  assert.deepEqual(registry, [saved.signature]);
  assert.equal(
    openNotebook(notebook, { secret: SECRET, registeredSignatures: registry })
      .trusted,
    true,
  );
});
