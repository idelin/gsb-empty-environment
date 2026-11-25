import { hashCanonical, type Finding } from "./core.ts";
import { Contexts, type EncoderStateBinding, type EncoderSet, type EncoderTable } from "./types.ts";

export function encoderSetHash(encoders: EncoderSet): string {
  return hashCanonical(encoders);
}

export function getEncoderTable(encoders: EncoderSet, encoderId: string): EncoderTable | undefined {
  return encoders[encoderId];
}

export interface EncodingResult {
  output: string;
  stateIdAfter: string;
  codePoints: number;
}

export function encodeValue(
  table: EncoderTable,
  stateIdBefore: string | undefined,
  value: string,
): EncodingResult {
  let stateId = stateIdBefore ?? table.initialStateId;
  let output = "";
  let codePoints = 0;

  for (const char of value) {
    const state = table.states[stateId];
    if (!state) {
      throw new Error(`Encoder ${table.id} has no state ${stateId}`);
    }
    output += state.escape?.[char] ?? char;
    stateId = state.next?.[char] ?? stateId;
    codePoints += 1;
  }

  if (!table.states[stateId]) {
    throw new Error(`Encoder ${table.id} ended in unknown state ${stateId}`);
  }
  return { output, stateIdAfter: stateId, codePoints };
}

export function encoderStatesEquivalent(
  oldTable: EncoderTable,
  oldStateId: string,
  newTable: EncoderTable,
  newStateId: string,
): boolean {
  const startOld = oldTable.states[oldStateId];
  const startNew = newTable.states[newStateId];
  if (!startOld || !startNew) return false;

  const seen = new Set<string>();
  const queue: Array<[string, string]> = [[oldStateId, newStateId]];

  while (queue.length > 0) {
    const [currentOldId, currentNewId] = queue.shift()!;
    const pairKey = `${currentOldId}\u0000${currentNewId}`;
    if (seen.has(pairKey)) continue;
    seen.add(pairKey);

    const oldState = oldTable.states[currentOldId];
    const newState = newTable.states[currentNewId];
    if (!oldState || !newState) return false;
    if (oldState.contextId !== newState.contextId) return false;

    const chars = new Set<string>([
      ...Object.keys(oldState.escape ?? {}),
      ...Object.keys(newState.escape ?? {}),
      ...Object.keys(oldState.next ?? {}),
      ...Object.keys(newState.next ?? {}),
    ]);

    for (const char of chars) {
      const oldOutput = oldState.escape?.[char] ?? char;
      const newOutput = newState.escape?.[char] ?? char;
      if (oldOutput !== newOutput) return false;

      const oldNext = oldState.next?.[char] ?? currentOldId;
      const newNext = newState.next?.[char] ?? currentNewId;
      if (!oldTable.states[oldNext] || !newTable.states[newNext]) return false;
      queue.push([oldNext, newNext]);
    }
  }

  return true;
}

export interface EscapeObligation {
  char: string;
  requirement: string;
  validate: (replacement: string | undefined) => boolean;
}

function knownHtmlEntity(char: string): (replacement: string | undefined) => boolean {
  return (replacement) => {
    if (!replacement || replacement === char) return false;
    if (replacement.includes(char) && char !== "&") return false;
    if (/^&#\d+;$/.test(replacement) || /^&#x[0-9a-f]+;$/i.test(replacement)) return true;
    const named: Record<string, RegExp> = {
      "&": /^&(amp|colon|tab|newline);$/i,
      "<": /^&(lt);$/i,
      ">": /^&(gt);$/i,
      '"': /^&(quot);$/i,
      "'": /^&(apos|#39);$/i,
    };
    return named[char]?.test(replacement) ?? false;
  };
}

function percentEncoded(char: string): (replacement: string | undefined) => boolean {
  return (replacement) => {
    if (!replacement || replacement === char) return false;
    if (!/^%[0-9A-Fa-f]{2}(?:%[0-9A-Fa-f]{2})*$/.test(replacement)) return false;
    return char === "%" || !replacement.includes(char);
  };
}

function escapedWith(char: string, lead: string): (replacement: string | undefined) => boolean {
  return (replacement) => {
    return !!replacement && replacement !== char && replacement.startsWith(lead) && !replacement.slice(1).includes(char);
  };
}

export const DEFAULT_ESCAPE_OBLIGATIONS: Record<string, EscapeObligation[]> = {
  [Contexts.HtmlText]: ["&", "<", ">"].map((char) => ({
    char,
    requirement: "HTML text must encode &, <, and >",
    validate: knownHtmlEntity(char),
  })),
  [Contexts.HtmlDoubleAttribute]: ["&", "<", ">", '"'].map((char) => ({
    char,
    requirement: "double-quoted HTML attribute must encode quote and markup delimiters",
    validate: knownHtmlEntity(char),
  })),
  [Contexts.HtmlSingleAttribute]: ["&", "<", ">", "'"].map((char) => ({
    char,
    requirement: "single-quoted HTML attribute must encode quote and markup delimiters",
    validate: knownHtmlEntity(char),
  })),
  [Contexts.JsDoubleString]: [
    { char: "\\", requirement: "backslash must be escaped", validate: escapedWith("\\", "\\\\") },
    { char: '"', requirement: "double quote must be escaped", validate: escapedWith('"', '\\') },
    { char: "\n", requirement: "newline must be escaped", validate: (r) => r === "\\n" },
  ],
  [Contexts.UrlQueryComponent]: ["&", "=", "#", "+", "%"].map((char) => ({
    char,
    requirement: "URL query delimiters must be percent-encoded",
    validate: percentEncoded(char),
  })),
};

export function checkEncoderStatePolicy(
  table: EncoderTable,
  stateId: string,
  obligationsByContext: Record<string, EscapeObligation[]> = DEFAULT_ESCAPE_OBLIGATIONS,
): Finding[] {
  const state = table.states[stateId];
  if (!state) {
    return [
      {
        code: "ENCODER_STATE_MISSING",
        severity: "error",
        message: `Encoder ${table.id} is missing state ${stateId}`,
      },
    ];
  }

  const findings: Finding[] = [];
  const obligations = obligationsByContext[state.contextId] ?? [];
  for (const obligation of obligations) {
    const replacement = state.escape?.[obligation.char];
    if (!obligation.validate(replacement)) {
      findings.push({
        code: "ENCODER_ESCAPE_POLICY_VIOLATION",
        severity: "error",
        message: obligation.requirement,
        evidence: {
          encoderId: table.id,
          stateId,
          contextId: state.contextId,
          inputCodePoint: obligation.char.codePointAt(0)?.toString(16),
          replacement: replacement ?? null,
        },
      });
    }
  }
  return findings;
}

export function verifyEncoderBinding(
  binding: EncoderStateBinding,
  oldEncoders: EncoderSet,
  newEncoders: EncoderSet,
): Finding[] {
  const oldTable = oldEncoders[binding.oldEncoderId];
  const newTable = newEncoders[binding.newEncoderId];
  if (!oldTable || !newTable) {
    return [
      {
        code: "ENCODER_BINDING_UNKNOWN_TABLE",
        severity: "error",
        message: "Encoder binding references a missing frozen encoder table",
        evidence: { binding },
      },
    ];
  }

  const oldState = oldTable.states[binding.oldStateId];
  const newState = newTable.states[binding.newStateId];
  if (!oldState || !newState) {
    return [
      {
        code: "ENCODER_BINDING_UNKNOWN_STATE",
        severity: "error",
        message: "Encoder binding references a missing state",
        evidence: { binding },
      },
    ];
  }

  const findings: Finding[] = [];
  if (oldState.contextId !== newState.contextId) {
    findings.push({
      code: "ENCODER_RENAME_CONTEXT_MISMATCH",
      severity: "error",
      message: "Encoder state rename crosses lexical context",
      evidence: {
        binding,
        oldContextId: oldState.contextId,
        newContextId: newState.contextId,
      },
    });
  }

  if (!encoderStatesEquivalent(oldTable, binding.oldStateId, newTable, binding.newStateId)) {
    findings.push({
      code: "ENCODER_RENAME_SEMANTIC_MISMATCH",
      severity: "error",
      message: "Renamed encoder state is not transition- and output-equivalent",
      evidence: { binding },
    });
  }

  findings.push(...checkEncoderStatePolicy(oldTable, binding.oldStateId));
  findings.push(...checkEncoderStatePolicy(newTable, binding.newStateId));
  return findings;
}
