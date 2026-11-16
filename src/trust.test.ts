import { test } from "node:test";
import assert from "node:assert/strict";

import {
  canonicalizeNotebook,
  computeSignature,
  isNotebookTrusted,
  openNotebook,
  sanitizeUntrustedNotebook,
  saveNotebook,
  type Notebook,
  type NotebookOutput,
} from "./trust.ts";

const SERVER_KEY = "server-secret-key";

function richOutput(overrides: Partial<NotebookOutput> = {}): NotebookOutput {
  return {
    output_type: "display_data",
    data: {
      "text/html": ["<script>alert(1)</script>"],
      "text/plain": ["<safe placeholder>"],
    },
    metadata: {},
    ...overrides,
  };
}

function makeNotebook(): Notebook {
  return {
    nbformat: 4,
    nbformat_minor: 5,
    metadata: {
      kernelspec: { display_name: "Python 3", language: "python", name: "python3" },
    },
    cells: [
      {
        cell_type: "code",
        execution_count: 1,
        metadata: {},
        source: ["print('hello')"],
        outputs: [
          richOutput(),
          {
            output_type: "stream",
            name: "stdout",
            text: ["hello\n"],
          },
        ],
      },
      {
        cell_type: "markdown",
        metadata: {},
        source: ["# title"],
      },
    ],
  };
}

function codeCell(notebook: Notebook) {
  const cell = notebook.cells?.[0];
  assert.ok(cell);
  return cell;
}

test("forged trusted flag in output metadata is ignored and dangerous outputs are stripped", () => {
  const attackerNotebook = makeNotebook();
  codeCell(attackerNotebook).outputs![0].metadata = { trusted: true };

  const result = openNotebook(attackerNotebook, SERVER_KEY, new Set());

  assert.equal(result.trusted, false);
  const output = codeCell(result.notebook).outputs?.find(
    (item) => item.output_type === "display_data",
  );
  assert.ok(output);
  assert.deepEqual(output.data, { "text/plain": ["<safe placeholder>"] });
  assert.equal("text/html" in (output.data ?? {}), false);
  assert.equal("trusted" in (output.metadata ?? {}), false);

  const streamOutput = codeCell(result.notebook).outputs?.find(
    (item) => item.output_type === "stream",
  );
  assert.deepEqual(streamOutput?.text, ["hello\n"]);

  // The original notebook object passed in is not mutated.
  assert.equal(
    "trusted" in codeCell(attackerNotebook).outputs![0].metadata!,
    true,
  );
});

test("output that only contains dangerous MIME types is dropped entirely", () => {
  const notebook = makeNotebook();
  codeCell(notebook).outputs = [
    richOutput({ data: { "image/svg+xml": ["<svg onload=alert(1)>"] } }),
  ];

  const sanitized = sanitizeUntrustedNotebook(notebook);
  assert.deepEqual(codeCell(sanitized).outputs, []);
});

test("save registers the recomputed signature and later content changes invalidate it", () => {
  const registered = new Set<string>();
  const notebook = makeNotebook();

  const saved = saveNotebook(notebook, SERVER_KEY, registered);
  assert.equal(registered.has(saved.signature), true);
  assert.equal(isNotebookTrusted(notebook, SERVER_KEY, registered), true);

  // Attacker edits cell source after the signature was registered but keeps
  // the old signature field.
  const tampered = makeNotebook();
  tampered.metadata = {
    ...tampered.metadata,
    signature: saved.signature,
  };
  (tampered.cells?.[0] as { source: string[] }).source = ["print('evil')"];

  const reopened = openNotebook(tampered, SERVER_KEY, registered);
  assert.equal(reopened.trusted, false);
  assert.notEqual(reopened.signature, saved.signature);
  assert.equal(registered.has(reopened.signature), false);

  const output = codeCell(reopened.notebook).outputs?.[0];
  assert.equal("text/html" in (output?.data ?? {}), false);
});

test("different key ordering with identical content produces the same signature", () => {
  const notebookA = makeNotebook();

  const notebookB: Notebook = {
    cells: [
      {
        outputs: [
          {
            metadata: {},
            data: {
              "text/plain": ["<safe placeholder>"],
              "text/html": ["<script>alert(1)</script>"],
            },
            output_type: "display_data",
          },
          {
            text: ["hello\n"],
            name: "stdout",
            output_type: "stream",
          },
        ],
        source: ["print('hello')"],
        metadata: {},
        execution_count: 1,
        cell_type: "code",
      },
      {
        source: ["# title"],
        metadata: {},
        cell_type: "markdown",
      },
    ],
    metadata: {
      kernelspec: { name: "python3", language: "python", display_name: "Python 3" },
    },
    nbformat_minor: 5,
    nbformat: 4,
  };

  assert.equal(canonicalizeNotebook(notebookA), canonicalizeNotebook(notebookB));
  assert.equal(
    computeSignature(notebookA, SERVER_KEY),
    computeSignature(notebookB, SERVER_KEY),
  );
});

test("canonical form excludes metadata.signature so embedded signatures never affect it", () => {
  const plain = makeNotebook();
  const withEmbeddedSignature = makeNotebook();
  withEmbeddedSignature.metadata = {
    ...withEmbeddedSignature.metadata,
    signature: "attacker-controlled-value",
  };

  assert.equal(
    canonicalizeNotebook(plain),
    canonicalizeNotebook(withEmbeddedSignature),
  );
  assert.equal(
    computeSignature(plain, SERVER_KEY),
    computeSignature(withEmbeddedSignature, SERVER_KEY),
  );
});

test("self-contained signature field is ignored and never gets registered", () => {
  const registered = new Set<string>();
  const notebook = makeNotebook();
  const forgedSignature = "deadbeef".repeat(8);
  notebook.metadata = { ...notebook.metadata, signature: forgedSignature };

  const result = openNotebook(notebook, SERVER_KEY, registered);
  assert.equal(result.trusted, false);
  assert.equal(registered.size, 0);
  assert.equal(registered.has(forgedSignature), false);
  // The embedded value is preserved on the document but carries no authority.
  assert.equal(
    (result.notebook.metadata as { signature?: string }).signature,
    forgedSignature,
  );

  // Registering through the save flow must use the recomputed value instead.
  const saved = saveNotebook(notebook, SERVER_KEY, registered);
  assert.equal(registered.has(forgedSignature), false);
  assert.equal(registered.has(saved.signature), true);
  assert.notEqual(saved.signature, forgedSignature);
});

test("plain text outputs survive sanitization and render once trusted", () => {
  const notebook = makeNotebook();

  const sanitized = openNotebook(notebook, SERVER_KEY, new Set());
  const streamBefore = codeCell(sanitized.notebook).outputs?.find(
    (item) => item.output_type === "stream",
  );
  assert.deepEqual(streamBefore?.text, ["hello\n"]);

  const registered = new Set<string>();
  saveNotebook(notebook, SERVER_KEY, registered);

  const trusted = openNotebook(notebook, SERVER_KEY, registered);
  assert.equal(trusted.trusted, true);
  const htmlOutput = codeCell(trusted.notebook).outputs?.[0];
  assert.equal("text/html" in (htmlOutput?.data ?? {}), true);
  assert.deepEqual(htmlOutput?.data?.["text/plain"], ["<safe placeholder>"]);
});

test("signature changes when any output content changes", () => {
  const notebook = makeNotebook();
  const before = computeSignature(notebook, SERVER_KEY);

  const modified = makeNotebook();
  modified.cells?.[0].outputs?.push({
    output_type: "stream",
    name: "stderr",
    text: ["warning\n"],
  });

  assert.notEqual(before, computeSignature(modified, SERVER_KEY));
});

test("a different server key never matches the registered signature", () => {
  const registered = new Set<string>();
  const notebook = makeNotebook();
  const saved = saveNotebook(notebook, SERVER_KEY, registered);

  assert.equal(isNotebookTrusted(notebook, "other-key", registered), false);
  assert.equal(registered.has(computeSignature(notebook, "other-key")), false);
  assert.equal(registered.has(saved.signature), true);
});
