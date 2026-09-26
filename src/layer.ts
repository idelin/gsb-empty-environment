import { IDENTITY, type Rect, type Transform } from './geometry.ts';

export type BlendMode = 'normal' | 'multiply' | 'additive';

export interface Shadow {
  radius: number;
  offsetX: number;
  offsetY: number;
  color: [number, number, number, number];
}

let nextId = 0;

export class Layer {
  readonly id: string;
  readonly width: number;
  readonly height: number;
  readonly pixels: Uint8ClampedArray;
  transform: Transform = { ...IDENTITY };
  opacity = 1;
  blendMode: BlendMode = 'normal';
  /** Clip rect in local content space; also clips this layer's shadow and subtree. */
  clip: Rect | null = null;
  shadow: Shadow | null = null;
  readonly children: Layer[] = [];
  parent: Layer | null = null;

  constructor(width: number, height: number, id?: string) {
    this.id = id ?? `layer-${nextId++}`;
    this.width = width;
    this.height = height;
    this.pixels = new Uint8ClampedArray(width * height * 4);
  }

  static solid(
    width: number,
    height: number,
    rgba: [number, number, number, number],
    id?: string,
  ): Layer {
    const layer = new Layer(width, height, id);
    for (let i = 0; i < layer.pixels.length; i += 4) {
      layer.pixels[i] = rgba[0];
      layer.pixels[i + 1] = rgba[1];
      layer.pixels[i + 2] = rgba[2];
      layer.pixels[i + 3] = rgba[3];
    }
    return layer;
  }

  setPixel(x: number, y: number, r: number, g: number, b: number, a: number): void {
    const i = (y * this.width + x) * 4;
    this.pixels[i] = r;
    this.pixels[i + 1] = g;
    this.pixels[i + 2] = b;
    this.pixels[i + 3] = a;
  }

  add(child: Layer): Layer {
    child.parent = this;
    this.children.push(child);
    return child;
  }

  *walk(): Generator<Layer> {
    yield this;
    for (const child of this.children) yield* child.walk();
  }
}

export class Scene {
  readonly width: number;
  readonly height: number;
  readonly roots: Layer[] = [];

  constructor(width: number, height: number) {
    this.width = width;
    this.height = height;
  }

  add(layer: Layer): Layer {
    this.roots.push(layer);
    return layer;
  }

  *walk(): Generator<Layer> {
    for (const root of this.roots) yield* root.walk();
  }
}
