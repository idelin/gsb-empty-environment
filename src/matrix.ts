export interface Matrix {
  a: number;
  b: number;
  c: number;
  d: number;
  e: number;
  f: number;
}

export function identity(): Matrix {
  return { a: 1, b: 0, c: 0, d: 1, e: 0, f: 0 };
}

export function multiply(m: Matrix, n: Matrix): Matrix {
  return {
    a: m.a * n.a + m.c * n.b,
    b: m.b * n.a + m.d * n.b,
    c: m.a * n.c + m.c * n.d,
    d: m.b * n.c + m.d * n.d,
    e: m.a * n.e + m.c * n.f + m.e,
    f: m.b * n.e + m.d * n.f + m.f,
  };
}

export function translation(tx: number, ty: number): Matrix {
  return { a: 1, b: 0, c: 0, d: 1, e: tx, f: ty };
}

export function rotation(radians: number): Matrix {
  const cos = Math.cos(radians);
  const sin = Math.sin(radians);
  return { a: cos, b: sin, c: -sin, d: cos, e: 0, f: 0 };
}

export function scaling(sx: number, sy: number): Matrix {
  return { a: sx, b: 0, c: 0, d: sy, e: 0, f: 0 };
}

export function applyToPoint(m: Matrix, x: number, y: number): { x: number; y: number } {
  return { x: m.a * x + m.c * y + m.e, y: m.b * x + m.d * y + m.f };
}

export class TransformStack {
  private stack: Matrix[] = [identity()];

  get current(): Matrix {
    return this.stack[this.stack.length - 1];
  }

  save(): void {
    this.stack.push({ ...this.current });
  }

  restore(): void {
    if (this.stack.length > 1) this.stack.pop();
  }

  transform(m: Matrix): void {
    this.stack[this.stack.length - 1] = multiply(this.current, m);
  }

  translate(tx: number, ty: number): void {
    this.transform(translation(tx, ty));
  }

  rotate(radians: number): void {
    this.transform(rotation(radians));
  }

  scale(sx: number, sy: number): void {
    this.transform(scaling(sx, sy));
  }
}
