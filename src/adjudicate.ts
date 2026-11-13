import { RAW_TEXT_ELEMENTS, parseFragment, type ParseOptions } from "./parser.ts";
import { serialize } from "./serialize.ts";
import {
  compareElementTrees,
  diffElementTrees,
  toElementTree,
  type ElementTree,
} from "./tree.ts";
import type { DocumentFragmentNode } from "./nodes.ts";

export interface ReparseResult {
  ok: boolean;
  stable: boolean;
  parent: string | null;
  payload: string | null;
  source: string;
  serialized: string;
  before: DocumentFragmentNode;
  after: DocumentFragmentNode;
  beforeTree: ElementTree[];
  afterTree: ElementTree[];
  addedElements: ElementTree[];
  removedElements: ElementTree[];
  addedElementNames: string[];
}

export class RawtextReentryError extends Error {
  readonly result: ReparseResult;

  constructor(result: ReparseResult) {
    super(
      `Re-parsing added elements: ${result.addedElementNames.join(", ")}`,
    );
    this.name = "RawtextReentryError";
    this.result = result;
  }
}

function adjudicateSource(
  source: string,
  options: ParseOptions,
  parent: string | null,
  payload: string | null,
): ReparseResult {
  const before = parseFragment(source, options);
  const serialized = serialize(before);
  const after = parseFragment(serialized, options);
  const beforeTree = toElementTree(before);
  const afterTree = toElementTree(after);
  const diff = diffElementTrees(beforeTree, afterTree);

  return {
    ok: diff.added.length === 0,
    stable: diff.equal,
    parent,
    payload,
    source,
    serialized,
    before,
    after,
    beforeTree,
    afterTree,
    addedElements: diff.added,
    removedElements: diff.removed,
    addedElementNames: [...new Set(diff.added.map((tree) => tree.tag))],
  };
}

export function adjudicateReentry(
  source: string,
  options?: ParseOptions,
): ReparseResult;
export function adjudicateReentry(
  parent: string,
  payload: string,
  options?: ParseOptions,
): ReparseResult;
export function adjudicateReentry(
  sourceOrParent: string,
  payloadOrOptions?: string | ParseOptions,
  maybeOptions: ParseOptions = {},
): ReparseResult {
  if (typeof payloadOrOptions === "string") {
    const parent = sourceOrParent.toLowerCase();
    const source = `<${parent}>${payloadOrOptions}</${parent}>`;
    return adjudicateSource(
      source,
      maybeOptions,
      parent,
      payloadOrOptions,
    );
  }

  return adjudicateSource(
    sourceOrParent,
    payloadOrOptions ?? {},
    null,
    null,
  );
}

export const reparse = reparseFragment;
export const adjudicate = adjudicateReentry;

export type ReparsedFragment = DocumentFragmentNode & {
  readonly ok: boolean;
  readonly stable: boolean;
  readonly result: ReparseResult;
  readonly before: DocumentFragmentNode;
  readonly serialized: string;
  readonly addedElements: ElementTree[];
  readonly removedElements: ElementTree[];
  readonly addedElementNames: string[];
};

function attachResult(
  fragment: DocumentFragmentNode,
  result: ReparseResult,
): ReparsedFragment {
  const keys: Array<keyof ReparseResult> = [
    "ok",
    "stable",
    "parent",
    "payload",
    "source",
    "serialized",
    "before",
    "beforeTree",
    "afterTree",
    "addedElements",
    "removedElements",
    "addedElementNames",
    "after",
  ];

  for (const key of keys) {
    Object.defineProperty(fragment, key, {
      value: result[key],
      enumerable: false,
      configurable: true,
    });
  }
  Object.defineProperty(fragment, "result", {
    value: result,
    enumerable: false,
    configurable: true,
  });
  return fragment as ReparsedFragment;
}

export function reparseFragment(
  source: string,
  options?: ParseOptions,
): ReparsedFragment;
export function reparseFragment(
  parent: string,
  payload: string,
  options?: ParseOptions,
): ReparsedFragment;
export function reparseFragment(
  sourceOrParent: string,
  payloadOrOptions?: string | ParseOptions,
  maybeOptions: ParseOptions = {},
): ReparsedFragment {
  const result =
    typeof payloadOrOptions === "string"
      ? adjudicateReentry(sourceOrParent, payloadOrOptions, maybeOptions)
      : adjudicateReentry(sourceOrParent, payloadOrOptions ?? {});
  return attachResult(result.after, result);
}

export const reparseHtml = reparseFragment;

export function isReparseStable(
  source: string,
  options?: ParseOptions,
): boolean {
  return adjudicateReentry(source, options).stable;
}

export const isReentrySafe = isReparseStable;

export function assertNoNewElements(
  source: string,
  options?: ParseOptions,
): ReparseResult;
export function assertNoNewElements(
  parent: string,
  payload: string,
  options?: ParseOptions,
): ReparseResult;
export function assertNoNewElements(
  sourceOrParent: string,
  payloadOrOptions?: string | ParseOptions,
  maybeOptions: ParseOptions = {},
): ReparseResult {
  const result =
    typeof payloadOrOptions === "string"
      ? adjudicateReentry(sourceOrParent, payloadOrOptions, maybeOptions)
      : adjudicateReentry(sourceOrParent, payloadOrOptions ?? {});

  if (!result.ok) {
    throw new RawtextReentryError(result);
  }
  return result;
}

export interface ParentPayloadComparison {
  payload: string;
  leftParent: string;
  rightParent: string;
  left: ReparseResult;
  right: ReparseResult;
  sameElementTree: boolean;
  sameChildElementTree: boolean;
}

export function comparePayloadParents(
  payload: string,
  leftParent = "noscript",
  rightParent = "p",
  options: ParseOptions = {},
): ParentPayloadComparison {
  const left = adjudicateReentry(
    leftParent.toLowerCase(),
    payload,
    options,
  );
  const right = adjudicateReentry(
    rightParent.toLowerCase(),
    payload,
    options,
  );

  return {
    payload,
    leftParent,
    rightParent,
    left,
    right,
    sameElementTree: compareElementTrees(
      left.beforeTree,
      right.beforeTree,
    ),
    sameChildElementTree: compareElementTrees(
      left.beforeTree,
      right.beforeTree,
      { ignoreRootTag: true },
    ),
  };
}

export interface ScriptingModeComparison {
  ok: boolean;
  stable: boolean;
  source: string;
  sanitizer: DocumentFragmentNode;
  browser: DocumentFragmentNode;
  sanitizerTree: ElementTree[];
  browserTree: ElementTree[];
  addedElements: ElementTree[];
  removedElements: ElementTree[];
  addedElementNames: string[];
  sameElementTree: boolean;
  sameChildElementTree: boolean;
}

export function compareScriptingModes(
  source: string,
  options: ParseOptions = {},
): ScriptingModeComparison {
  const sanitizer = parseFragment(source, {
    ...options,
    scriptingEnabled: false,
  });
  const browser = parseFragment(source, {
    ...options,
    scriptingEnabled: true,
  });
  const sanitizerTree = toElementTree(sanitizer);
  const browserTree = toElementTree(browser);
  const diff = diffElementTrees(sanitizerTree, browserTree);

  return {
    ok: diff.added.length === 0,
    stable: diff.equal,
    source,
    sanitizer,
    browser,
    sanitizerTree,
    browserTree,
    addedElements: diff.added,
    removedElements: diff.removed,
    addedElementNames: [...new Set(diff.added.map((tree) => tree.tag))],
    sameElementTree: compareElementTrees(sanitizerTree, browserTree),
    sameChildElementTree: compareElementTrees(
      sanitizerTree,
      browserTree,
      { ignoreRootTag: true },
    ),
  };
}

export const adjudicateScriptingTransition = compareScriptingModes;

export function hasScriptingModeMutation(source: string): boolean {
  return !compareScriptingModes(source).ok;
}

export const detectMutationXss = hasScriptingModeMutation;

export interface RawtextPayloadReport {
  parent: string;
  ok: boolean;
  stable: boolean;
  addedElementNames: string[];
  result: ReparseResult;
}

export function analyzeRawtextPayload(
  payload: string,
  options: ParseOptions = {},
): RawtextPayloadReport[] {
  return RAW_TEXT_ELEMENTS.map((parent) => {
    const result = adjudicateReentry(parent, payload, options);
    return {
      parent,
      ok: result.ok,
      stable: result.stable,
      addedElementNames: result.addedElementNames,
      result,
    };
  });
}

export const analyzePayload = analyzeRawtextPayload;

export function isPayloadSafe(
  payload: string,
  options: ParseOptions = {},
): boolean {
  return analyzeRawtextPayload(payload, options).every(
    (report) => report.ok && report.stable,
  );
}

export const isRawtextPayloadSafe = isPayloadSafe;
