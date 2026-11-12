export type PropagationKind =
  | "assignment"
  | "concat"
  | "interpolation"
  | "join"
  | "substr"
  | "hash-value"
  | "array-element"
  | "regex-capture"
  | "regex-capture-retained"
  | "binary"
  | "sink";

export interface SourceLocation {
  file?: string;
  line: number;
  column: number;
}

export interface TaintOrigin {
  id: string;
  kind: string;
  label: string;
  expression: string;
  location: SourceLocation;
}

export interface TaintStep {
  kind: PropagationKind;
  label: string;
  detail?: string;
  location?: SourceLocation;
}

export interface TaintTrace {
  origin: TaintOrigin;
  steps: TaintStep[];
}

export interface TaintValue {
  readonly tainted: boolean;
  readonly traces: readonly TaintTrace[];
}

export interface SourceOptions {
  file?: string;
  line?: number;
  column?: number;
  kind?: string;
  label?: string;
}

export interface PropagationOptions {
  file?: string;
  line?: number;
  column?: number;
  label?: string;
  detail?: string;
}

export interface CaptureOptions extends PropagationOptions {
  group?: number;
  flags?: string;
  matchSucceeded?: boolean;
}

let originSequence = 0;

export const CLEAN: TaintValue = Object.freeze({
  tainted: false,
  traces: Object.freeze([]) as readonly TaintTrace[],
});

function location(options?: PropagationOptions | SourceOptions): SourceLocation {
  return {
    ...(options?.file === undefined ? {} : { file: options.file }),
    line: options?.line ?? 1,
    column: options?.column ?? 1,
  };
}

function traceKey(trace: TaintTrace): string {
  return [
    trace.origin.id,
    ...trace.steps.map(
      (step) =>
        `${step.kind}:${step.label}:${step.location?.line ?? 0}:${step.location?.column ?? 0}`,
    ),
  ].join("|");
}

export function mergeTaint(parts: readonly (TaintValue | undefined | null)[]): TaintValue {
  const traces: TaintTrace[] = [];
  const seen = new Set<string>();

  for (const part of parts) {
    if (!part?.tainted) continue;
    for (const trace of part.traces) {
      const key = traceKey(trace);
      if (seen.has(key)) continue;
      seen.add(key);
      traces.push(trace);
    }
  }

  return traces.length === 0 ? CLEAN : { tainted: true, traces };
}

export function taintSource(expression: string, options: SourceOptions = {}): TaintValue {
  originSequence += 1;
  const origin: TaintOrigin = {
    id: `source-${originSequence}`,
    kind: options.kind ?? "external-input",
    label: options.label ?? expression,
    expression,
    location: location(options),
  };
  return {
    tainted: true,
    traces: [{ origin, steps: [] }],
  };
}

export function withTaintStep(
  value: TaintValue,
  kind: PropagationKind,
  options: PropagationOptions = {},
): TaintValue {
  if (!value.tainted || value.traces.length === 0) return CLEAN;
  const step: TaintStep = {
    kind,
    label: options.label ?? kind,
    ...(options.detail === undefined ? {} : { detail: options.detail }),
    ...(options.line === undefined && options.column === undefined && options.file === undefined
      ? {}
      : { location: location(options) }),
  };
  return {
    tainted: true,
    traces: value.traces.map((trace) => ({
      origin: trace.origin,
      steps: [...trace.steps, step],
    })),
  };
}

export function assignTaint(
  value: TaintValue,
  options: PropagationOptions = {},
): TaintValue {
  return withTaintStep(value, "assignment", {
    label: options.label ?? "assignment",
    ...options,
  });
}

export function concatTaint(
  parts: readonly TaintValue[],
  options: PropagationOptions = {},
): TaintValue {
  const merged = mergeTaint(parts);
  return withTaintStep(merged, "concat", {
    label: options.label ?? "concatenation",
    ...options,
  });
}

export function interpolateTaint(
  parts: readonly TaintValue[],
  options: PropagationOptions = {},
): TaintValue {
  const merged = mergeTaint(parts);
  return withTaintStep(merged, "interpolation", {
    label: options.label ?? "string interpolation",
    ...options,
  });
}

export function joinTaint(
  separator: TaintValue | undefined,
  elements: readonly TaintValue[],
  options: PropagationOptions = {},
): TaintValue {
  const merged = mergeTaint([separator ?? CLEAN, ...elements]);
  return withTaintStep(merged, "join", {
    label: options.label ?? "join",
    ...options,
  });
}

export function substrTaint(
  source: TaintValue,
  options: PropagationOptions = {},
): TaintValue {
  return withTaintStep(source, "substr", {
    label: options.label ?? "substr",
    ...options,
  });
}

export function hashValueTaint(
  hashValue: TaintValue | undefined,
  key: TaintValue | string | undefined,
  options: PropagationOptions = {},
): TaintValue {
  const merged = mergeTaint([
    hashValue,
    typeof key === "string" ? undefined : key,
  ]);
  return withTaintStep(merged, "hash-value", {
    label: options.label ?? "hash value",
    ...(options.detail === undefined && typeof key === "string"
      ? { detail: key }
      : options.detail === undefined
        ? {}
        : { detail: options.detail }),
    ...options,
  });
}

export function arrayElementTaint(
  arrayValue: TaintValue | undefined,
  index: TaintValue | string | number | undefined,
  options: PropagationOptions = {},
): TaintValue {
  const merged = mergeTaint([
    arrayValue,
    typeof index === "number" || typeof index === "string" ? undefined : index,
  ]);
  return withTaintStep(merged, "array-element", {
    label: options.label ?? "array element",
    ...options,
  });
}

function hasUnescapedTrailingDollar(pattern: string): boolean {
  if (!pattern.endsWith("$")) return false;
  let backslashes = 0;
  for (let index = pattern.length - 2; index >= 0 && pattern[index] === "\\"; index -= 1) {
    backslashes += 1;
  }
  return backslashes % 2 === 0;
}

export function isAnchoredWholeStringPattern(pattern: string, flags = ""): boolean {
  if (!pattern.startsWith("^") || !hasUnescapedTrailingDollar(pattern)) return false;
  if (pattern.includes(".*")) return false;
  if (flags.includes("m")) return false;
  return true;
}

export const isCleansingCapturePattern = isAnchoredWholeStringPattern;

export function regexCaptureTaint(
  input: TaintValue,
  pattern: string,
  groupOrOptions: number | CaptureOptions = 1,
  maybeOptions: CaptureOptions = {},
): TaintValue {
  const options =
    typeof groupOrOptions === "number"
      ? { ...maybeOptions, group: groupOrOptions }
      : groupOrOptions;
  const group = options.group ?? 1;
  const flags = options.flags ?? "";
  const matchSucceeded = options.matchSucceeded ?? true;
  if (group <= 0 || !matchSucceeded) {
    return withTaintStep(input, "regex-capture-retained", {
      label: options.label ?? `regex capture $${group || "&"}`,
      detail: options.detail ?? pattern,
      ...options,
    });
  }
  if (isAnchoredWholeStringPattern(pattern, flags)) return CLEAN;
  return withTaintStep(input, "regex-capture-retained", {
    label: options.label ?? `regex capture $${group}`,
    detail: options.detail ?? pattern,
    ...options,
  });
}

export const assignmentTaint = assignTaint;
export const propagateAssignment = assignTaint;
export const concatenationTaint = concatTaint;
export const propagateConcatenation = concatTaint;
export const interpolationTaint = interpolateTaint;
export const propagateInterpolation = interpolateTaint;
export const propagationJoin = joinTaint;
export const propagationSubstr = substrTaint;
export const hashTaint = hashValueTaint;
export const arrayTaint = arrayElementTaint;
export const propagateHashValue = hashValueTaint;
export const propagateArrayElement = arrayElementTaint;
export const captureTaint = regexCaptureTaint;
export const propagateRegexCapture = regexCaptureTaint;

export function describeTaintTrace(trace: TaintTrace, sink?: string): string {
  const frames = [
    `${trace.origin.label} (${trace.origin.kind}, ${
      trace.origin.location.file
        ? `${trace.origin.location.file}:`
        : ""
    }${trace.origin.location.line}:${trace.origin.location.column})`,
    ...trace.steps.map(
      (step) =>
        `${step.label} (${step.kind}${
          step.location
            ? `, ${step.location.file ? `${step.location.file}:` : ""}${
                step.location.line
              }:${step.location.column}`
            : ""
        })`,
    ),
  ];
  return [...frames, ...(sink ? [sink] : [])].join(" -> ");
}

export function formatTaintChain(value: TaintValue, sink?: string): string[] {
  return value.traces.map((trace) => describeTaintTrace(trace, sink));
}
