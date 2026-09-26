/** Vertex in homogeneous clip space; attrs are flat (e.g. [r,g,b,a] in 0..255). */
export interface ClipVertex {
  x: number;
  y: number;
  z: number;
  w: number;
  attrs: number[];
}

export interface Face {
  id: number;
  verts: [ClipVertex, ClipVertex, ClipVertex];
}

export interface Viewport {
  x: number;
  y: number;
  width: number;
  height: number;
}

/** Vertex after viewport transform: 16.16 fixed screen pos, 24-bit depth, 1/w and attrs/w. */
export interface ScreenVertex {
  fx: number;
  fy: number;
  depth: number;
  invW: number;
  attrsOverW: number[];
}

export type Tri = [ScreenVertex, ScreenVertex, ScreenVertex];

/** Face after near/far clipping + projection. Pixel bbox: min inclusive, max exclusive. */
export interface ProjectedFace {
  id: number;
  transparent: boolean;
  tris: Tri[];
  minX: number;
  minY: number;
  maxX: number;
  maxY: number;
}
