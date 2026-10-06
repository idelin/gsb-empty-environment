import { Instr } from './interpreter';

/** Tiny assembler so tests can write programs with named labels. */
export class ProgramBuilder {
  private readonly instrs: Instr[] = [];
  private readonly labels = new Map<string, number>();
  private readonly fixups: { index: number; name: string }[] = [];

  label(name: string): this {
    if (this.labels.has(name)) {
      throw new Error(`duplicate label: ${name}`);
    }
    this.labels.set(name, this.instrs.length);
    return this;
  }

  yield(value: unknown): this {
    this.instrs.push({ op: 'yield', value });
    return this;
  }

  ret(value: unknown): this {
    this.instrs.push({ op: 'return', value });
    return this;
  }

  throwErr(error: unknown): this {
    this.instrs.push({ op: 'throw', error });
    return this;
  }

  pushFinally(handlerLabel: string): this {
    this.fixups.push({ index: this.instrs.length, name: handlerLabel });
    this.instrs.push({ op: 'pushFinally', handler: -1 });
    return this;
  }

  popFinally(): this {
    this.instrs.push({ op: 'popFinally' });
    return this;
  }

  endFinally(): this {
    this.instrs.push({ op: 'endFinally' });
    return this;
  }

  build(): Instr[] {
    for (const fixup of this.fixups) {
      const target = this.labels.get(fixup.name);
      if (target === undefined) {
        throw new Error(`unknown label: ${fixup.name}`);
      }
      this.instrs[fixup.index] = { op: 'pushFinally', handler: target };
    }
    return this.instrs;
  }
}

export function program(define: (b: ProgramBuilder) => void): Instr[] {
  const builder = new ProgramBuilder();
  define(builder);
  return builder.build();
}
