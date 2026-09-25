export class PathSyntaxError extends Error {
  readonly input: string;
  readonly offset: number;
  constructor(input: string, offset: number, detail: string) {
    super(`Invalid path ${JSON.stringify(input)} at offset ${offset}: ${detail}`);
    this.name = 'PathSyntaxError';
    this.input = input;
    this.offset = offset;
  }
}

export class RuleError extends Error {
  readonly ruleIndex: number;
  constructor(ruleIndex: number, from: string, to: string, detail: string) {
    super(`Invalid rule #${ruleIndex} (${JSON.stringify(from)} -> ${JSON.stringify(to)}): ${detail}`);
    this.name = 'RuleError';
    this.ruleIndex = ruleIndex;
  }
}

export class MoveConflictError extends Error {
  readonly targetPath: string;
  readonly sourcePaths: string[];
  constructor(targetPath: string, sourcePaths: string[], detail: string) {
    super(`Conflict at target ${JSON.stringify(targetPath)} (from ${sourcePaths.map((s) => JSON.stringify(s)).join(', ')}): ${detail}`);
    this.name = 'MoveConflictError';
    this.targetPath = targetPath;
    this.sourcePaths = sourcePaths;
  }
}
