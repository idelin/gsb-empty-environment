import {
  CLEAN,
  type CaptureOptions,
  type PropagationOptions,
  type TaintOrigin,
  type TaintTrace,
  type TaintValue,
  arrayElementTaint,
  assignTaint,
  concatTaint,
  describeTaintTrace,
  hashValueTaint,
  interpolateTaint,
  joinTaint,
  mergeTaint,
  regexCaptureTaint,
  substrTaint,
  taintSource,
  withTaintStep,
} from "./taint.ts";
import { type PerlToken, tokenizePerl } from "./perl-tokenizer.ts";

export type TaintSinkKind = "system" | "exec" | "backtick" | "open";

export interface TaintFinding {
  sink: TaintSinkKind;
  line: number;
  column: number;
  expression: string;
  tainted: true;
  traces: TaintTrace[];
  chain: string[];
  sources: TaintOrigin[];
  message: string;
}

export interface VariableSnapshot {
  variable: string;
  tainted: boolean;
  chain: string[];
}

export interface PerlAnalysisResult {
  findings: TaintFinding[];
  sources: TaintOrigin[];
  variables: VariableSnapshot[];
}

export interface PerlAnalysisOptions {
  file?: string;
}

type Expr =
  | { kind: "literal"; value: string }
  | { kind: "variable"; name: string }
  | {
      kind: "element";
      base: Expr;
      container: string;
      hash: boolean;
      key: Expr;
      canonical: string;
    }
  | { kind: "source"; expression: string; sourceKind: string }
  | { kind: "interpolate"; parts: Expr[]; raw: string }
  | { kind: "concat"; left: Expr; right: Expr }
  | {
      kind: "list";
      items: Array<{
        key?: Expr;
        value: Expr;
        valueText: string;
        valueTaint: TaintValue;
      }>;
    }
  | { kind: "call"; name: string; args: Expr[] }
  | { kind: "regex"; pattern: string; flags: string }
  | { kind: "binary"; op: string; left: Expr; right?: Expr }
  | { kind: "backtick"; expression: string }
  | { kind: "unknown"; text: string };

interface EvalResult {
  value: TaintValue;
  expr?: Expr;
  text: string;
}

type LValue =
  | { kind: "variable"; name: string; text: string }
  | {
      kind: "element";
      container: string;
      hash: boolean;
      key: Expr;
      canonical: string;
      text: string;
    }
  | { kind: "list"; children: LValue[]; text: string }
  | { kind: "ignored"; text: string };

interface ListItem {
  value: EvalResult;
  key?: EvalResult;
}

const DECLARATION_WORDS = new Set(["my", "our", "local", "state"]);
const ASSIGNMENT_OPERATORS = new Set([
  "=",
  ".=",
  "+=",
  "-=",
  "*=",
  "/=",
  "%=",
  "||=",
  "//=",
  "&&=",
]);
const COMPARISON_OPERATORS = new Set([
  "==",
  "!=",
  "<",
  ">",
  "<=",
  ">=",
  "eq",
  "ne",
  "lt",
  "gt",
  "le",
  "ge",
  "<=>",
  "cmp",
]);
const CONTROL_WORDS = new Set([
  "if",
  "unless",
  "elsif",
  "while",
  "until",
  "for",
  "foreach",
  "else",
]);
const SOURCE_FUNCTIONS = new Set([
  "param",
  "CGI::param",
  "readline",
  "getline",
]);
const TRANSPARENT_FUNCTIONS = new Set([
  "lc",
  "uc",
  "lcfirst",
  "ucfirst",
  "chomp",
  "chop",
  "quotemeta",
  "trim",
  "sprintf",
  "split",
  "map",
  "grep",
  "reverse",
]);

class PerlTaintContext {
  readonly findings: TaintFinding[] = [];
  readonly locations = new Map<string, TaintValue>();
  readonly captures = new Map<number, TaintValue>();
  wholeMatches: TaintValue = CLEAN;
  postMatches: TaintValue = CLEAN;
  preMatches: TaintValue = CLEAN;
  readonly file?: string;

  constructor(file?: string) {
    this.file = file;
  }

  contextFor(token: PerlToken, label: string): PropagationOptions {
    return {
      ...(this.file ? { file: this.file } : {}),
      line: token.line,
      column: token.column,
      label,
    };
  }

  source(expression: string, token: PerlToken, kind = "external-input"): TaintValue {
    return taintSource(expression, {
      ...(this.file ? { file: this.file } : {}),
      line: token.line,
      column: token.column,
      kind,
      label: expression,
    });
  }

  location(name: string): TaintValue {
    return this.locations.get(name) ?? CLEAN;
  }

  setLocation(name: string, value: TaintValue): void {
    this.locations.set(name, value);
  }

  clearLocation(name: string): void {
    this.locations.delete(name);
  }

  clearContainer(container: string): void {
    for (const key of [...this.locations.keys()]) {
      if (key.startsWith(`${container}{`) || key.startsWith(`${container}[`)) {
        this.locations.delete(key);
      }
    }
  }

  recordFinding(
    sink: TaintSinkKind,
    value: TaintValue,
    token: PerlToken,
    expression: string,
  ): void {
    if (!value.tainted) return;
    const sinkValue = withTaintStep(value, "sink", this.contextFor(token, sink));
    const traces = sinkValue.traces.map((trace) => ({
      origin: trace.origin,
      steps: [...trace.steps],
    }));
    const chain = traces.map((trace) => describeTaintTrace(trace));
    const sources: TaintOrigin[] = [];
    const seen = new Set<string>();
    for (const trace of traces) {
      if (seen.has(trace.origin.id)) continue;
      seen.add(trace.origin.id);
      sources.push(trace.origin);
    }
    const duplicate = this.findings.some(
      (finding) =>
        finding.sink === sink &&
        finding.line === token.line &&
        finding.column === token.column &&
        finding.expression === expression,
    );
    if (duplicate) return;
    this.findings.push({
      sink,
      line: token.line,
      column: token.column,
      expression,
      tainted: true,
      traces,
      chain,
      sources,
      message: `Tainted data reaches ${sink} at ${this.file ? `${this.file}:` : ""}${
        token.line
      }:${token.column}`,
    });
  }
}

function literalString(expr: Expr | undefined): string | undefined {
  if (!expr) return undefined;
  switch (expr.kind) {
    case "literal":
    case "source":
    case "unknown":
      return expr.kind === "source" ? expr.expression : expr.kind === "unknown" ? expr.text : expr.value;
    case "interpolate": {
      const values: string[] = [];
      for (const part of expr.parts) {
        if (part.kind !== "literal") return undefined;
        values.push(part.value);
      }
      return values.join("");
    }
    case "concat": {
      const left = literalString(expr.left);
      const right = literalString(expr.right);
      return left === undefined || right === undefined ? undefined : left + right;
    }
    default:
      return undefined;
  }
}

function literalPrefix(expr: Expr | undefined, maximum = 4): string {
  if (!expr) return "";
  switch (expr.kind) {
    case "literal":
      return expr.value.slice(0, maximum);
    case "interpolate": {
      let prefix = "";
      for (const part of expr.parts) {
        if (part.kind !== "literal") break;
        prefix += part.value;
        if (prefix.length >= maximum) break;
      }
      return prefix.slice(0, maximum);
    }
    case "concat": {
      const left = literalPrefix(expr.left, maximum);
      if (left.length >= maximum) return left;
      return (left + literalPrefix(expr.right, maximum - left.length)).slice(0, maximum);
    }
    default:
      return "";
  }
}

function isWriteMode(mode: string): boolean {
  const trimmed = mode.trimStart();
  return (
    trimmed.startsWith(">") ||
    trimmed.startsWith("+>") ||
    trimmed.startsWith(">|") ||
    trimmed.startsWith("+>>")
  );
}

function isReadMode(mode: string): boolean {
  const trimmed = mode.trimStart();
  return trimmed.startsWith("<") || trimmed.startsWith("|");
}

function countCaptureGroups(pattern: string): number {
  let count = 0;
  let inClass = false;
  for (let index = 0; index < pattern.length; index += 1) {
    const char = pattern[index];
    if (char === "\\") {
      index += 1;
      continue;
    }
    if (char === "[") {
      inClass = true;
      continue;
    }
    if (char === "]") {
      inClass = false;
      continue;
    }
    if (char !== "(" || inClass) continue;
    if (pattern[index + 1] !== "?") {
      count += 1;
      continue;
    }
    const special = pattern.slice(index, index + 4);
    if (
      special.startsWith("(?<") ||
      special.startsWith("(?'") ||
      pattern.slice(index, index + 3) === "(?P"
    ) {
      count += 1;
    }
  }
  return count;
}

function keyText(expr: Expr | undefined): string {
  const literal = literalString(expr);
  if (literal !== undefined) return literal;
  if (expr?.kind === "variable") return expr.name;
  return "*";
}

function elementCanonical(container: string, key: string, hash: boolean): string {
  const base = container.startsWith("$") ? container : `$${container.slice(1)}`;
  return `${base}${hash ? "{" : "["}${key}${hash ? "}" : "]"}`;
}

function parseInterpolation(content: string): { parts: Expr[]; hasInterpolation: boolean } {
  const parts: Expr[] = [];
  let literal = "";
  let hasInterpolation = false;

  const flushLiteral = (): void => {
    if (literal.length === 0) return;
    parts.push({ kind: "literal", value: literal });
    literal = "";
  };

  for (let index = 0; index < content.length; index += 1) {
    const char = content[index];
    if (char === "\\") {
      literal += char;
      if (index + 1 < content.length) {
        literal += content[index + 1];
        index += 1;
      }
      continue;
    }
    if (char !== "$" && char !== "@") {
      literal += char;
      continue;
    }

    let cursor = index + 1;
    let name = "";
    if (content[cursor] === "{") {
      cursor += 1;
      const start = cursor;
      while (cursor < content.length && content[cursor] !== "}") cursor += 1;
      name = content.slice(start, cursor);
      if (content[cursor] === "}") cursor += 1;
    } else {
      const special = content[cursor];
      if (special !== undefined && /[0-9&`'/+]/.test(special)) {
        name = special;
        cursor += 1;
      } else {
        const start = cursor;
        while (cursor < content.length && /[A-Za-z0-9_:]/.test(content[cursor])) {
          cursor += 1;
        }
        name = content.slice(start, cursor);
      }
    }
    if (name.length === 0) {
      literal += char;
      continue;
    }

    hasInterpolation = true;
    flushLiteral();
    let variable: Expr = { kind: "variable", name: char + name };
    let suffix = "";
    while (cursor < content.length && (content[cursor] === "{" || content[cursor] === "[")) {
      const opening = content[cursor];
      const closing = opening === "{" ? "}" : "]";
      const start = cursor + 1;
      cursor += 1;
      while (cursor < content.length && content[cursor] !== closing) cursor += 1;
      const key = content.slice(start, cursor);
      if (content[cursor] === closing) cursor += 1;
      const hash = opening === "{";
      const container = hash
        ? `%${name}`
        : variable.kind === "variable" && variable.name.startsWith("@")
          ? variable.name
          : `@${name.replace(/^\$/, "")}`;
      const baseCanonical =
        variable.kind === "element" ? variable.canonical : char + name;
      const canonical = elementCanonical(baseCanonical, key, hash);
      suffix += `${opening}${key}${closing}`;
      variable = {
        kind: "element",
        base: variable,
        container,
        hash,
        key: { kind: "literal", value: key },
        canonical,
      };
    }
    parts.push(variable);
    index = cursor - 1;
  }
  flushLiteral();
  return { parts, hasInterpolation };
}

export class PerlTaintAnalyzer {
  private readonly tokens: PerlToken[];
  private index = 0;
  private nesting = 0;
  private readonly context: PerlTaintContext;

  constructor(source: string, options: PerlAnalysisOptions = {}) {
    this.tokens = tokenizePerl(source);
    this.context = new PerlTaintContext(options.file);
  }

  analyze(): PerlAnalysisResult {
    this.parseProgram(false);
    const sources: TaintOrigin[] = [];
    const seen = new Set<string>();
    for (const finding of this.context.findings) {
      for (const origin of finding.sources) {
        if (seen.has(origin.id)) continue;
        seen.add(origin.id);
        sources.push(origin);
      }
    }
    const variables = [...this.context.locations.entries()].map(([variable, value]) => ({
      variable,
      tainted: value.tainted,
      chain: value.traces.map((trace) => describeTaintTrace(trace)),
    }));
    return {
      findings: this.context.findings,
      sources,
      variables,
    };
  }

  private parseProgram(inBlock: boolean): void {
    while (!this.atEnd()) {
      this.skipStatementTrivia();
      if (this.atEnd()) break;
      const nextToken = this.peek();
      if (inBlock && nextToken?.type === "punct" && nextToken.value === "}") {
        this.consume();
        break;
      }
      if (nextToken?.type === "punct" && nextToken.value === "{") {
        this.parseBlock();
        continue;
      }
      const token = this.peek();
      if (token?.type === "word" && CONTROL_WORDS.has(token.value)) {
        this.parseControl();
        continue;
      }
      this.parseSimpleStatement();
    }
  }

  private parseBlock(): void {
    this.consume();
    this.parseProgram(true);
    this.skipStatementTrivia();
  }

  private parseControl(): void {
    const keyword = this.peek();
    if (!keyword) return;
    this.consume();
    if (keyword.value === "else") {
      this.skipNewlines();
      if (this.peek()?.value === "{") this.parseBlock();
      return;
    }
    if (keyword.value === "elsif") {
      this.parseCondition();
      this.skipNewlines();
      if (this.peek()?.value === "{") this.parseBlock();
      return;
    }
    if (keyword.value === "for" || keyword.value === "foreach") {
      this.parseForControl(keyword);
      return;
    }
    this.parseCondition();
    this.skipNewlines();
    if (this.peek()?.value === "{") this.parseBlock();
  }

  private parseCondition(): void {
    this.skipNewlines();
    const nextToken = this.peek();
    if (nextToken?.type === "punct" && nextToken.value === "(") {
      this.consume();
      this.nesting += 1;
      const condition = this.parseExpression();
      this.skipNewlines();
      if (this.peek()?.value === ")") this.consume();
      this.nesting -= 1;
      if (condition) {
        this.context.setLocation("$_", condition.value);
      }
    }
  }

  private parseForControl(keyword: PerlToken): void {
    this.skipNewlines();
    if (this.peek()?.value !== "(") return;
    this.consume();
    this.nesting += 1;
    this.skipNewlines();
    const nextToken = this.peek();
    if (nextToken?.type === "word" && DECLARATION_WORDS.has(nextToken.value)) {
      this.consume();
    }
    let loopVariable: string | undefined;
    if (this.peek()?.type === "variable") {
      loopVariable = this.consume().value;
    }
    this.skipNewlines();
    let iterable: EvalResult | undefined;
    if (this.peek()?.value === "(") {
      iterable = this.parseParenthesizedList();
    } else if (this.peek()?.value === "..") {
      iterable = { value: CLEAN, text: "" };
    }
    this.skipNewlines();
    if (this.peek()?.value === ")") this.consume();
    this.nesting -= 1;
    if (iterable) {
      const target = loopVariable ?? "$_";
      this.context.setLocation(
        target,
        assign(this.context, iterable.value, keyword, target),
      );
    }
    this.skipNewlines();
    if (this.peek()?.value === "{") this.parseBlock();
  }

  private parseSimpleStatement(): void {
    const start = this.index;
    const nextToken = this.peek();
    const declaration =
      nextToken?.type === "word" && DECLARATION_WORDS.has(nextToken.value);
    if (declaration) this.consume();
    const lvalue = this.parseLValue();
    const next = this.peek();
    if (lvalue && next?.type === "op" && ASSIGNMENT_OPERATORS.has(next.value)) {
      const operator = this.consume();
      const value = this.parseExpression();
      if (value) this.assignLValue(lvalue, value, operator, next);
      this.finishStatement();
      return;
    }
    if (declaration && lvalue) {
      this.clearLValue(lvalue);
      this.finishStatement();
      return;
    }
    this.index = start;

    const first = this.peek();
    if (
      first?.type === "word" &&
      ["push", "unshift", "pop", "shift"].includes(first.value)
    ) {
      this.parseArrayMutationStatement();
      return;
    }
    if (
      first?.type === "word" &&
      (first.value === "system" || first.value === "exec" || first.value === "open")
    ) {
      this.parseSinkStatement();
      return;
    }
    const expression = this.parseExpression();
    if (!expression && this.peek()) this.consume();
    this.finishStatement();
  }

  private parseArrayMutationStatement(): void {
    const token = this.consume();
    let items: ListItem[] = [];
    this.skipNewlines();
    if (this.peek()?.value === "(") {
      items = this.parseCallArguments();
    } else {
      items = this.parseUnparenthesizedList();
    }
    const args = items.map((item) => item.value);
    if ((token.value === "push" || token.value === "unshift") && args.length > 0) {
      const container = args[0].expr;
      if (container?.kind === "variable" && container.name.startsWith("@")) {
        const previous = this.context.location(container.name);
        const added = mergeTaint(args.slice(1).map((argument) => argument.value));
        const value = mergeTaint([previous, added]);
        this.context.setLocation(
          container.name,
          assignTaint(value, this.context.contextFor(token, container.name)),
        );
      }
    }
    this.finishStatement();
  }

  private parseSinkStatement(): void {
    const token = this.consume();
    let items: ListItem[] = [];
    this.skipNewlines();
    if (this.peek()?.value === "(") {
      items = this.parseCallArguments();
    } else {
      items = this.parseUnparenthesizedList();
    }
    this.handleSink(token, items);
    this.finishStatement();
  }

  private handleSink(token: PerlToken, items: ListItem[]): void {
    const args = items.map((item) => item.value);
    if (token.value === "system" || token.value === "exec") {
      const value = mergeTaint(args.map((argument) => argument.value));
      const expression = args.map((argument) => argument.text).join(", ");
      this.context.recordFinding(token.value, value, token, expression);
      return;
    }
    if (token.value === "open") {
      this.handleOpen(token, args);
    }
  }

  private handleOpen(token: PerlToken, args: EvalResult[]): void {
    if (args.length < 2) return;
    if (args.length >= 3) {
      const mode = args[1];
      const modeText = literalString(mode.expr) ?? literalPrefix(mode.expr);
      if (mode.value.tainted) {
        this.context.recordFinding("open", mode.value, token, mode.text);
      }
      if (mode.value.tainted || isWriteMode(modeText)) {
        for (const argument of args.slice(2)) {
          if (argument.value.tainted) {
            this.context.recordFinding("open", argument.value, token, argument.text);
          }
        }
      }
      return;
    }

    const combined = args[1];
    const prefix = literalString(combined.expr) ?? literalPrefix(combined.expr);
    if (isReadMode(prefix) && !combined.value.tainted) return;
    if (isReadMode(prefix) && combined.value.tainted) {
      return;
    }
    if (combined.value.tainted && !isReadMode(prefix)) {
      this.context.recordFinding("open", combined.value, token, combined.text);
    }
  }

  private parseUnparenthesizedList(): ListItem[] {
    const items: ListItem[] = [];
    this.skipNewlines();
    while (
      !this.atEnd() &&
      this.peek()?.type !== "newline" &&
      this.peek()?.value !== ";" &&
      this.peek()?.value !== "}"
    ) {
      const value = this.parseExpression();
      if (!value) break;
      if (this.peek()?.value === "=>") {
        this.consume();
        const actual = this.parseExpression();
        if (actual) items.push({ key: value, value: actual });
      } else {
        items.push({ value });
      }
      if (this.peek()?.value === ",") {
        this.consume();
        this.skipNewlines();
        continue;
      }
      break;
    }
    return items;
  }

  private parseExpression(): EvalResult | undefined {
    const checkpoint = this.index;
    const declaration =
      this.peek()?.type === "word" && DECLARATION_WORDS.has(this.peek()?.value ?? "");
    if (declaration) this.consume();
    const lvalue = this.parseLValue();
    const operator = this.peek();
    if (
      lvalue &&
      operator?.type === "op" &&
      ASSIGNMENT_OPERATORS.has(operator.value)
    ) {
      this.consume();
      const assignedValue = this.parseExpression();
      if (assignedValue) {
        this.assignLValue(lvalue, assignedValue, operator, operator);
        return assignedValue;
      }
    }
    this.index = checkpoint;
    return this.parseConditionalExpression();
  }

  private parseConditionalExpression(): EvalResult | undefined {
    const condition = this.parseLogicalExpression();
    if (!condition) return undefined;
    this.skipNewlinesIfNested();
    if (this.peek()?.value !== "?") return condition;
    this.consume();
    this.skipNewlinesIfNested();
    const whenTrue = this.parseExpression();
    this.skipNewlinesIfNested();
    if (this.peek()?.value === ":") this.consume();
    this.skipNewlinesIfNested();
    const whenFalse = this.parseExpression();
    if (!whenTrue || !whenFalse) return whenTrue ?? whenFalse ?? condition;
    return {
      value: mergeTaint([whenTrue.value, whenFalse.value]),
      expr: {
        kind: "binary",
        op: "?:",
        left: condition.expr ?? { kind: "unknown", text: condition.text },
        right: whenTrue.expr,
      },
      text: `${condition.text} ? ${whenTrue.text} : ${whenFalse.text}`,
    };
  }

  private parseLogicalExpression(): EvalResult | undefined {
    let left = this.parseConcat();
    if (!left) return undefined;
    while (true) {
      this.skipNewlinesIfNested();
      const token = this.peek();
      if (
        token?.type === "op" &&
        ["||", "//", "&&", "and", "or", "xor"].includes(token.value)
      ) {
        this.consume();
        this.skipNewlinesIfNested();
        const right = this.parseConcat();
        if (!right) break;
        const merged = mergeTaint([left.value, right.value]);
        left = {
          value: merged,
          expr: { kind: "binary", op: token.value, left: left.expr ?? { kind: "unknown", text: left.text }, right: right.expr },
          text: `${left.text} ${token.value} ${right.text}`,
        };
        continue;
      }
      break;
    }
    return left;
  }

  private parseConcat(): EvalResult | undefined {
    let left = this.parseAdditive();
    if (!left) return undefined;
    while (true) {
      this.skipNewlinesIfNested();
      const token = this.peek();
      if (token?.type === "op" && token.value === ".") {
        this.consume();
        this.skipNewlinesIfNested();
        const right = this.parseAdditive();
        if (!right) break;
        const context = this.context.contextFor(token, "concatenation");
        left = {
          value: concatTaint([left.value, right.value], context),
          expr: {
            kind: "concat",
            left: left.expr ?? { kind: "unknown", text: left.text },
            right: right.expr ?? { kind: "unknown", text: right.text },
          },
          text: `${left.text} . ${right.text}`,
        };
        continue;
      }
      if (token?.type === "word" && token.value === "x") {
        this.consume();
        this.skipNewlinesIfNested();
        const right = this.parseAdditive();
        if (!right) break;
        const context = this.context.contextFor(token, "string repetition");
        left = {
          value: concatTaint([left.value, right.value], context),
          text: `${left.text} x ${right.text}`,
        };
        continue;
      }
      break;
    }
    return left;
  }

  private parseAdditive(): EvalResult | undefined {
    let left = this.parseComparison();
    if (!left) return undefined;
    while (true) {
      this.skipNewlinesIfNested();
      const token = this.peek();
      if (token?.type === "op" && ["+", "-", "*", "/", "%", "**"].includes(token.value)) {
        this.consume();
        this.skipNewlinesIfNested();
        const right = this.parseComparison();
        if (!right) break;
        const context = this.context.contextFor(token, token.value);
        left = {
          value: withTaintStep(mergeTaint([left.value, right.value]), "binary", context),
          expr: {
            kind: "binary",
            op: token.value,
            left: left.expr ?? { kind: "unknown", text: left.text },
            right: right.expr,
          },
          text: `${left.text} ${token.value} ${right.text}`,
        };
        continue;
      }
      break;
    }
    return left;
  }

  private parseComparison(): EvalResult | undefined {
    let left = this.parseMatch();
    if (!left) return undefined;
    while (true) {
      this.skipNewlinesIfNested();
      const token = this.peek();
      if (
        token &&
        ((token.type === "op" && COMPARISON_OPERATORS.has(token.value)) ||
          (token.type === "word" && COMPARISON_OPERATORS.has(token.value)))
      ) {
        this.consume();
        this.skipNewlinesIfNested();
        const right = this.parseMatch();
        if (!right) break;
        const context = this.context.contextFor(token, token.value);
        left = {
          value: withTaintStep(mergeTaint([left.value, right.value]), "binary", context),
          expr: {
            kind: "binary",
            op: token.value,
            left: left.expr ?? { kind: "unknown", text: left.text },
            right: right.expr,
          },
          text: `${left.text} ${token.value} ${right.text}`,
        };
        continue;
      }
      break;
    }
    return left;
  }

  private parseMatch(): EvalResult | undefined {
    const left = this.parseUnary();
    if (!left) return undefined;
    this.skipNewlinesIfNested();
    const token = this.peek();
    if (token?.type === "op" && (token.value === "=~" || token.value === "!~")) {
      this.consume();
      this.skipNewlinesIfNested();
      const right = this.parseUnary();
      if (!right) return left;
      if (token.value === "=~") {
        const pattern =
          right.expr?.kind === "regex"
            ? right.expr.pattern
            : literalString(right.expr) ?? right.text;
        const flags = right.expr?.kind === "regex" ? right.expr.flags : "";
        this.applyMatch(left.value, pattern, flags, token);
      }
      return {
        value: CLEAN,
        expr: {
          kind: "binary",
          op: token.value,
          left: left.expr ?? { kind: "unknown", text: left.text },
          right: right.expr,
        },
        text: `${left.text} ${token.value} ${right.text}`,
      };
    }
    return left;
  }

  private parseUnary(): EvalResult | undefined {
    this.skipNewlinesIfNested();
    const token = this.peek();
    if (!token) return undefined;
    if (
      (token.type === "op" && ["!", "-", "+", "~", "\\", "++", "--"].includes(token.value)) ||
      (token.type === "word" && ["not", "scalar", "defined", "ref", "exists", "delete"].includes(token.value))
    ) {
      this.consume();
      const operand = this.parseUnary();
      if (!operand) return { value: CLEAN, text: token.raw };
      if (token.value === "++" || token.value === "--") {
        if (operand.expr?.kind === "variable") {
          const value = this.context.location(operand.expr.name);
          this.context.setLocation(operand.expr.name, value);
        }
      }
      return operand;
    }
    if (token.type === "word" && DECLARATION_WORDS.has(token.value)) {
      this.consume();
      return this.parseUnary();
    }
    return this.parsePrimary();
  }

  private parsePrimary(): EvalResult | undefined {
    this.skipNewlinesIfNested();
    const token = this.peek();
    if (!token) return undefined;

    if (token.type === "variable") {
      this.consume();
      return this.parseVariableValue(token);
    }
    if (token.type === "input") {
      this.consume();
      return {
        value: this.context.source(token.raw, token, "stdin-input"),
        expr: { kind: "source", expression: token.raw, sourceKind: "stdin-input" },
        text: token.raw,
      };
    }
    if (token.type === "string" || token.type === "backtick") {
      this.consume();
      return this.evaluateStringToken(token);
    }
    if (token.type === "regex") {
      this.consume();
      return {
        value: CLEAN,
        expr: { kind: "regex", pattern: token.value, flags: token.flags ?? "" },
        text: token.raw,
      };
    }
    if (token.type === "number") {
      this.consume();
      return { value: CLEAN, expr: { kind: "literal", value: token.value }, text: token.value };
    }
    if (token.type === "word") {
      this.consume();
      this.skipNewlinesIfNested();
      const nextToken = this.peek();
      if (nextToken?.type === "punct" && nextToken.value === "(") {
        return this.evaluateCall(token);
      }
      if (token.value === "shift" || token.value === "pop") {
        const next = this.peek();
        if (
          next &&
          next.type !== "newline" &&
          next.type !== "eof" &&
          !(next.type === "punct" && [")", "]", "}", ",", ";"].includes(next.value))
        ) {
          const argument = this.parseUnary();
          if (argument) return argument;
        }
        return {
          value: this.context.source(token.raw, token, "external-input"),
          expr: { kind: "source", expression: token.raw, sourceKind: "external-input" },
          text: token.raw,
        };
      }
      if (token.value === "undef") {
        return { value: CLEAN, expr: { kind: "literal", value: "" }, text: token.value };
      }
      return { value: CLEAN, expr: { kind: "literal", value: token.value }, text: token.value };
    }
    if (token.type === "punct" && token.value === "(") {
      return this.parseParenthesizedList();
    }
    if (token.type === "punct" && token.value === "[") {
      return this.parseBracketedList();
    }
    return undefined;
  }

  private parseVariableValue(token: PerlToken): EvalResult {
    let expr: Expr = { kind: "variable", name: token.value };
    let value = this.resolveBaseVariable(token);
    let text = token.value;
    while (true) {
      this.skipNewlinesIfNested();
      const next = this.peek();
      if (
        next?.type === "punct" &&
        (next.value === "{" || next.value === "[")
      ) {
        const opening = this.consume();
        const closing = opening.value === "{" ? "}" : "]";
        const hash = opening.value === "{";
        this.nesting += 1;
        const key = this.parseExpression();
        this.skipNewlines();
        if (this.peek()?.value === closing) this.consume();
        this.nesting -= 1;
        const baseName = expr.kind === "variable" ? expr.name : text;
        const container = hash
          ? `%${baseName.replace(/^[$@%]/, "")}`
          : baseName.startsWith("@")
            ? baseName
            : `@${baseName.replace(/^[$@%]/, "")}`;
        const keyLiteral = keyText(key?.expr);
        const canonical = elementCanonical(
          expr.kind === "element" ? expr.canonical : baseName,
          keyLiteral,
          hash,
        );
        const nextExpr: Expr = {
          kind: "element",
          base: expr,
          container,
          hash,
          key: key?.expr ?? { kind: "unknown", text: keyLiteral },
          canonical,
        };
        expr = nextExpr;
        text = canonical;
        value = this.resolveElement(token, expr, key?.value, baseName);
        continue;
      }
      if (next?.type === "op" && next.value === "->") {
        this.consume();
        if (this.peek()?.value === "{") {
          continue;
        }
        if (this.peek()?.type === "variable") {
          const key = this.consume();
          const keyExpr: Expr = { kind: "literal", value: key.value.replace(/^\$/, "") };
          const canonical = `${text}{${keyExpr.value}}`;
          expr = {
            kind: "element",
            base: expr,
            container: expr.kind === "element" ? expr.container : `%${text.replace(/^\$/, "")}`,
            hash: true,
            key: keyExpr,
            canonical,
          };
          text = canonical;
          value = this.resolveElement(token, expr, undefined, text);
          continue;
        }
      }
      break;
    }
    if (expr.kind === "element" && value === CLEAN) {
      value = this.context.location(expr.canonical);
    }
    return { value, expr, text };
  }

  private resolveBaseVariable(token: PerlToken): TaintValue {
    const name = token.value;
    if (/^\$\d+$/.test(name)) {
      return this.context.captures.get(Number(name.slice(1))) ?? CLEAN;
    }
    if (name === "$&") return this.context.wholeMatches;
    if (name === "$`") return this.context.preMatches;
    if (name === "$'") return this.context.postMatches;
    if (name.startsWith("$#")) return CLEAN;
    if (
      name === "$ARGV" ||
      name === "@ARGV" ||
      name === "$ENV" ||
      name === "%ENV"
    ) {
      return this.context.source(name, token, "external-input");
    }
    return this.context.location(name);
  }

  private resolveElement(
    token: PerlToken,
    expr: Extract<Expr, { kind: "element" }>,
    keyValue: TaintValue | undefined,
    baseName: string,
  ): TaintValue {
    if (
      baseName === "$ARGV" ||
      baseName === "@ARGV" ||
      baseName === "$ENV" ||
      baseName === "%ENV" ||
      (expr.base.kind === "element" &&
        (expr.base.canonical.startsWith("$ARGV") || expr.base.canonical.startsWith("$ENV")))
    ) {
      return this.context.source(expr.canonical, token, "external-input");
    }
    if (this.context.locations.has(expr.canonical)) {
      const exact = this.context.location(expr.canonical);
      return withTaintStep(exact, expr.hash ? "hash-value" : "array-element", {
        ...this.context.contextFor(token, expr.hash ? "hash value" : "array element"),
        detail: keyText(expr.key),
      });
    }
    const container = this.context.location(expr.container);
    if (expr.hash) {
      return hashValueTaint(
        mergeTaint([container, keyValue]),
        undefined,
        {
          ...this.context.contextFor(token, "hash value"),
          detail: keyText(expr.key),
        },
      );
    }
    return arrayElementTaint(
      mergeTaint([container, keyValue]),
      undefined,
      {
        ...this.context.contextFor(token, "array element"),
        detail: keyText(expr.key),
      },
    );
  }

  private parseParenthesizedList(): EvalResult {
    const open = this.consume();
    this.nesting += 1;
    this.skipNewlines();
    const items: ListItem[] = [];
    while (!this.atEnd() && this.peek()?.value !== ")") {
      const value = this.parseExpression();
      if (!value) break;
      if (this.peek()?.value === "=>") {
        this.consume();
        this.skipNewlines();
        const actual = this.parseExpression();
        if (actual) items.push({ key: value, value: actual });
      } else {
        items.push({ value });
      }
      this.skipNewlines();
      if (this.peek()?.value === ",") {
        this.consume();
        this.skipNewlines();
        continue;
      }
      break;
    }
    if (this.peek()?.value === ")") this.consume();
    this.nesting -= 1;
    return this.buildListResult(items, open);
  }

  private parseBracketedList(): EvalResult {
    const open = this.consume();
    this.nesting += 1;
    this.skipNewlines();
    const items: ListItem[] = [];
    while (!this.atEnd() && this.peek()?.value !== "]") {
      const value = this.parseExpression();
      if (!value) break;
      items.push({ value });
      this.skipNewlines();
      if (this.peek()?.value === ",") {
        this.consume();
        this.skipNewlines();
        continue;
      }
      break;
    }
    if (this.peek()?.value === "]") this.consume();
    this.nesting -= 1;
    return this.buildListResult(items, open);
  }

  private buildListResult(items: ListItem[], token: PerlToken): EvalResult {
    const values = items.map((item) => item.value.value);
    return {
      value: mergeTaint(values),
      expr: {
        kind: "list",
        items: items.map((item) => ({
          ...(item.key?.expr === undefined ? {} : { key: item.key.expr }),
          value: item.value.expr ?? { kind: "unknown", text: item.value.text },
          valueText: item.value.text,
          valueTaint: item.value.value,
        })),
      },
      text: token.raw,
    };
  }

  private evaluateStringToken(token: PerlToken): EvalResult {
    const interpolated =
      token.quote === '"' ||
      token.quote === "qq" ||
      token.quote === "qx" ||
      token.type === "backtick";
    if (!interpolated || token.quote === "'" || token.quote === "q" || token.quote === "qw") {
      return {
        value: CLEAN,
        expr: { kind: "literal", value: token.value },
        text: token.raw,
      };
    }
    const parsed = parseInterpolation(token.value);
    const parts = parsed.parts.map((part) => this.evaluateInterpolationPart(part, token));
    const value = interpolateTaint(
      parts.map((part) => part.value),
      this.context.contextFor(token, "string interpolation"),
    );
    if (token.type === "backtick") {
      const commandValue = this.context.source(token.raw, token, "command-output");
      this.context.recordFinding("backtick", value, token, token.raw);
      return {
        value: commandValue,
        expr: { kind: "backtick", expression: token.raw },
        text: token.raw,
      };
    }
    return {
      value,
      expr: {
        kind: "interpolate",
        parts: parts.map((part) => part.expr ?? { kind: "unknown", text: part.text }),
        raw: token.raw,
      },
      text: token.raw,
    };
  }

  private evaluateInterpolationPart(expr: Expr, token: PerlToken): EvalResult {
    if (expr.kind === "literal") {
      return { value: CLEAN, expr, text: expr.value };
    }
    if (expr.kind === "variable") {
      const synthetic: PerlToken = {
        ...token,
        type: "variable",
        value: expr.name,
        raw: expr.name,
      };
      return {
        value: this.resolveBaseVariable(synthetic),
        expr,
        text: expr.name,
      };
    }
    if (expr.kind === "element") {
      const synthetic: PerlToken = {
        ...token,
        type: "variable",
        value:
          expr.base.kind === "variable" ? expr.base.name : expr.canonical,
        raw: expr.canonical,
      };
      const keyValue = this.evaluateInterpolationPart(expr.key, token).value;
      return {
        value: this.resolveElement(
          synthetic,
          expr,
          keyValue,
          expr.base.kind === "variable" ? expr.base.name : expr.base.canonical,
        ),
        expr,
        text: expr.canonical,
      };
    }
    return { value: CLEAN, expr, text: "" };
  }

  private evaluateCall(token: PerlToken): EvalResult {
    const items = this.parseCallArguments();
    const args = items.map((item) => item.value);
    const name = token.value;
    const values = args.map((argument) => argument.value);
    const text = `${name}(${args.map((argument) => argument.text).join(", ")})`;
    if (name === "system" || name === "exec") {
      this.context.recordFinding(name, mergeTaint(values), token, text);
      return { value: CLEAN, expr: { kind: "call", name, args: [] }, text };
    }
    if (name === "open") {
      this.handleOpen(token, values);
      return { value: CLEAN, expr: { kind: "call", name, args: [] }, text };
    }
    if (name === "join") {
      const separator = values[0];
      const elements = values.slice(1);
      return {
        value: joinTaint(separator, elements, this.context.contextFor(token, "join")),
        expr: { kind: "call", name, args: [] },
        text,
      };
    }
    if (name === "substr") {
      const sourceValue = values[0] ?? CLEAN;
      return {
        value: substrTaint(sourceValue, this.context.contextFor(token, "substr")),
        expr: { kind: "call", name, args: [] },
        text,
      };
    }
    if (SOURCE_FUNCTIONS.has(name)) {
      const sourceValue = this.context.source(text, token, "external-input");
      return {
        value: sourceValue,
        expr: { kind: "source", expression: text, sourceKind: "external-input" },
        text,
      };
    }
    if (name === "split") {
      const sourceValue = values[1] ?? CLEAN;
      return { value: sourceValue, expr: { kind: "call", name, args: [] }, text };
    }
    if (name === "shift" || name === "pop") {
      if (values.length === 0) {
        return {
          value: this.context.source(text, token, "external-input"),
          expr: { kind: "source", expression: text, sourceKind: "external-input" },
          text,
        };
      }
      return { value: values[0], expr: { kind: "call", name, args: [] }, text };
    }
    if (TRANSPARENT_FUNCTIONS.has(name)) {
      return {
        value: mergeTaint(values),
        expr: { kind: "call", name, args: [] },
        text,
      };
    }
    return {
      value: mergeTaint(values),
      expr: { kind: "call", name, args: [] },
      text,
    };
  }

  private parseCallArguments(): ListItem[] {
    if (this.peek()?.value !== "(") return [];
    this.consume();
    this.nesting += 1;
    this.skipNewlines();
    const items: ListItem[] = [];
    while (!this.atEnd() && this.peek()?.value !== ")") {
      const value = this.parseExpression();
      if (!value) break;
      if (this.peek()?.value === "=>") {
        this.consume();
        const actual = this.parseExpression();
        if (actual) items.push({ key: value, value: actual });
      } else {
        items.push({ value });
      }
      this.skipNewlines();
      if (this.peek()?.value === ",") {
        this.consume();
        this.skipNewlines();
        continue;
      }
      break;
    }
    if (this.peek()?.value === ")") this.consume();
    this.nesting -= 1;
    return items;
  }

  private parseLValue(): LValue | undefined {
    this.skipNewlinesIfNested();
    const token = this.peek();
    if (!token) return undefined;
    if (token.type === "word" && token.value === "undef") {
      this.consume();
      return { kind: "ignored", text: "undef" };
    }
    if (token.type === "variable") {
      this.consume();
      let base: LValue = { kind: "variable", name: token.value, text: token.value };
      while (true) {
        this.skipNewlinesIfNested();
        const next = this.peek();
        if (next?.type === "punct" && (next.value === "{" || next.value === "[")) {
          const opening = this.consume();
          const closing = opening.value === "{" ? "}" : "]";
          this.nesting += 1;
          const key = this.parseExpression();
          this.skipNewlines();
          if (this.peek()?.value === closing) this.consume();
          this.nesting -= 1;
          const hash = opening.value === "{";
          const baseName = base.kind === "variable" ? base.name : base.text;
          const container = hash
            ? `%${baseName.replace(/^[$@%]/, "")}`
            : baseName.startsWith("@")
              ? baseName
              : `@${baseName.replace(/^[$@%]/, "")}`;
          const canonical = elementCanonical(
            base.kind === "element" ? base.canonical : baseName,
            keyText(key?.expr),
            hash,
          );
          base = {
            kind: "element",
            container,
            hash,
            key: key?.expr ?? { kind: "unknown", text: "*" },
            canonical,
            text: canonical,
          };
          continue;
        }
        break;
      }
      return base;
    }
    if (token.type === "punct" && token.value === "(") {
      this.consume();
      this.nesting += 1;
      const children: LValue[] = [];
      this.skipNewlines();
      while (!this.atEnd() && this.peek()?.value !== ")") {
        const child = this.parseLValue();
        if (!child) break;
        children.push(child);
        this.skipNewlines();
        if (this.peek()?.value === ",") {
          this.consume();
          this.skipNewlines();
          continue;
        }
        break;
      }
      if (this.peek()?.value === ")") this.consume();
      this.nesting -= 1;
      return { kind: "list", children, text: "list" };
    }
    return undefined;
  }

  private clearLValue(lvalue: LValue): void {
    if (lvalue.kind === "variable") {
      this.context.setLocation(lvalue.name, CLEAN);
      if (lvalue.name.startsWith("@") || lvalue.name.startsWith("%")) {
        this.context.clearContainer(lvalue.name);
      }
      return;
    }
    if (lvalue.kind === "element") {
      this.context.setLocation(lvalue.canonical, CLEAN);
      return;
    }
    if (lvalue.kind === "list") {
      for (const child of lvalue.children) this.clearLValue(child);
    }
  }

  private assignLValue(
    lvalue: LValue,
    result: EvalResult,
    operator: PerlToken,
    assignmentToken: PerlToken,
  ): void {
    if (lvalue.kind === "ignored") return;
    if (lvalue.kind === "list") {
      const list = result.expr?.kind === "list" ? result.expr.items : undefined;
      lvalue.children.forEach((child, index) => {
        const item = list?.[index];
        const value = item
          ? {
              value: item.valueTaint,
              text: item.valueText,
              expr: item.value,
            }
          : result;
        this.assignSingle(child, value, operator, assignmentToken);
      });
      return;
    }
    this.assignSingle(lvalue, result, operator, assignmentToken);
  }

  private assignSingle(
    lvalue: Exclude<LValue, { kind: "list" } | { kind: "ignored" }>,
    result: EvalResult,
    operator: PerlToken,
    assignmentToken: PerlToken,
  ): void {
    if (lvalue.kind === "variable") {
      const name = lvalue.name;
      const oldValue = this.context.location(name);
      const value = this.combineAssignment(oldValue, result.value, operator);
      const assigned = assignTaint(value, this.context.contextFor(assignmentToken, name));
      this.context.setLocation(name, assigned);
      if (name.startsWith("%") || name.startsWith("@")) {
        this.context.clearContainer(name);
        if (result.expr?.kind === "list") {
          this.storeContainerItems(name, result.expr, assignmentToken);
        }
      }
      return;
    }
    const oldValue = this.context.location(lvalue.canonical);
    const combined = this.combineAssignment(oldValue, result.value, operator);
    const value = assignTaint(
      combined,
      this.context.contextFor(assignmentToken, lvalue.canonical),
    );
    this.context.setLocation(lvalue.canonical, value);
    const containerValue = mergeTaint([
      this.context.location(lvalue.container),
      value,
    ]);
    this.context.setLocation(
      lvalue.container,
      assignTaint(
        containerValue,
        this.context.contextFor(assignmentToken, lvalue.container),
      ),
    );
  }

  private storeContainerItems(
    container: string,
    list: Extract<Expr, { kind: "list" }>,
    token: PerlToken,
  ): void {
    const hash = container.startsWith("%");
    list.items.forEach((item, index) => {
      const key = hash && item.key ? keyText(item.key) : String(index);
      const canonical = elementCanonical(container, key, hash);
      const itemValue = item.valueTaint;
      this.context.setLocation(
        canonical,
        assignTaint(
          itemValue,
          this.context.contextFor(token, canonical),
        ),
      );
    });
  }

  private evaluateListItem(expr: Expr): TaintValue {
    if (expr.kind === "literal") return CLEAN;
    if (expr.kind === "source") {
      const token: PerlToken = {
        type: "input",
        value: expr.expression,
        raw: expr.expression,
        line: 1,
        column: 1,
      };
      return this.context.source(expr.expression, token, expr.sourceKind);
    }
    if (expr.kind === "variable") {
      return this.context.location(expr.name);
    }
    if (expr.kind === "element") {
      return this.resolveElement(
        {
          type: "variable",
          value: expr.canonical,
          raw: expr.canonical,
          line: 1,
          column: 1,
        },
        expr,
        this.evaluateListItem(expr.key),
        expr.base.kind === "variable" ? expr.base.name : expr.base.canonical,
      );
    }
    return CLEAN;
  }

  private combineAssignment(
    oldValue: TaintValue,
    newValue: TaintValue,
    operator: PerlToken,
  ): TaintValue {
    if (operator.value === ".=") {
      return concatTaint(
        [oldValue, newValue],
        this.context.contextFor(operator, "concatenation"),
      );
    }
    if (operator.value !== "=") {
      return withTaintStep(
        mergeTaint([oldValue, newValue]),
        "binary",
        this.context.contextFor(operator, operator.value),
      );
    }
    return newValue;
  }

  private applyMatch(
    input: TaintValue,
    pattern: string,
    flags: string,
    token: PerlToken,
  ): void {
    const captures = countCaptureGroups(pattern);
    const context: CaptureOptions = {
      ...this.context.contextFor(token, "regex capture"),
      flags,
    };
    for (let group = 1; group <= captures; group += 1) {
      const captureContext: CaptureOptions = {
        ...context,
        label: `regex capture $${group}`,
      };
      this.context.captures.set(
        group,
        regexCaptureTaint(input, pattern, group, captureContext),
      );
    }
    this.context.wholeMatches = withTaintStep(input, "regex-capture-retained", {
      ...this.context.contextFor(token, "regex match"),
      detail: pattern,
    });
    this.context.preMatches = withTaintStep(input, "regex-capture-retained", {
      ...this.context.contextFor(token, "regex pre-match"),
      detail: pattern,
    });
    this.context.postMatches = withTaintStep(input, "regex-capture-retained", {
      ...this.context.contextFor(token, "regex post-match"),
      detail: pattern,
    });
  }

  private finishStatement(): void {
    while (!this.atEnd()) {
      const token = this.peek();
      if (
        token?.type === "newline" ||
        (token?.type === "punct" && (token.value === ";" || token.value === "}"))
      ) {
        if (token.type === "punct" && token.value === ";") this.consume();
        while (this.peek()?.type === "newline") this.consume();
        break;
      }
      this.consume();
    }
  }

  private skipStatementTrivia(): void {
    while (
      this.peek()?.type === "newline" ||
      (this.peek()?.type === "punct" && this.peek()?.value === ";")
    ) {
      this.consume();
    }
  }

  private skipNewlinesIfNested(): void {
    if (this.nesting > 0) this.skipNewlines();
  }

  private skipNewlines(): void {
    while (this.peek()?.type === "newline") this.consume();
  }

  private peek(): PerlToken | undefined {
    return this.tokens[this.index];
  }

  private consume(): PerlToken {
    const token = this.tokens[this.index];
    this.index += 1;
    return token;
  }

  private atEnd(): boolean {
    return this.peek()?.type === "eof" || this.index >= this.tokens.length;
  }
}

function assign(
  context: PerlTaintContext,
  value: TaintValue,
  token: PerlToken,
  label: string,
): TaintValue {
  return withTaintStep(value, "assignment", context.contextFor(token, label));
}

export function analyzePerl(
  source: string,
  options: PerlAnalysisOptions = {},
): PerlAnalysisResult {
  return new PerlTaintAnalyzer(source, options).analyze();
}

export const analyze = analyzePerl;
export const analyzePerlScript = analyzePerl;
export const scanPerlTaint = analyzePerl;
export const checkPerlScript = analyzePerl;
