import type { Mask } from "./mask.ts";

export interface Point {
  x: number;
  y: number;
}

export type RingKind = "outer" | "hole";

/**
 * A closed, non-repeating walk along pixel edges ("cracks").
 *
 * - `vertices` are grid-line coordinates (0..width, 0..height); the ring is
 *   closed, i.e. the last vertex connects back to the first, and no vertex
 *   appears twice.
 * - Outer rings wind clockwise (screen coordinates, y axis pointing down),
 *   holes wind counter-clockwise, so `signedArea` is positive for outer
 *   rings and negative for holes.
 * - `pixels` lists the foreground pixels touching the boundary in walk order
 *   (for holes these are the foreground pixels surrounding the hole).
 */
export interface Ring {
  kind: RingKind;
  closed: true;
  vertices: Point[];
  pixels: Point[];
  signedArea: number;
  perimeter: number;
}

// Direction encoding: 0=E, 1=S, 2=W, 3=N (y axis points down).
const DX = [1, 0, -1, 0];
const DY = [0, 1, 0, -1];

interface Edge {
  x0: number;
  y0: number;
  x1: number;
  y1: number;
  dir: number;
  px: number; // foreground pixel on the right-hand side of the edge
  py: number;
}

/**
 * Extract outer contours and holes from a binary mask.
 *
 * Method: crack following. Every foreground pixel emits a directed edge along
 * each side that borders background or the image boundary, oriented so the
 * foreground stays on the right. At vertices where two outgoing edges meet
 * (diagonal pixel contact) the sharpest right turn is chosen, which keeps
 * foreground 4-connected and background 8-connected; this yields simple
 * closed rings with no repeated vertices and no ambiguous diagonal holes.
 */
export function extractContours(mask: Mask): Ring[] {
  const { width, height, data } = mask;
  const at = (x: number, y: number): number =>
    x < 0 || y < 0 || x >= width || y >= height ? 0 : data[y * width + x];

  // Collect directed boundary edges, foreground always on the right.
  const edges: Edge[] = [];
  // Map from start-vertex key to indices of outgoing edges.
  const outgoing = new Map<number, number[]>();
  const vertexKey = (x: number, y: number): number => y * (width + 1) + x;

  const addEdge = (x0: number, y0: number, dir: number, px: number, py: number): void => {
    const index = edges.length;
    edges.push({ x0, y0, x1: x0 + DX[dir], y1: y0 + DY[dir], dir, px, py });
    const key = vertexKey(x0, y0);
    const list = outgoing.get(key);
    if (list) list.push(index);
    else outgoing.set(key, [index]);
  };

  for (let y = 0; y < height; y++) {
    for (let x = 0; x < width; x++) {
      if (!at(x, y)) continue;
      if (!at(x, y - 1)) addEdge(x, y, 0, x, y); // top side, walk east
      if (!at(x + 1, y)) addEdge(x + 1, y, 1, x, y); // right side, walk south
      if (!at(x, y + 1)) addEdge(x + 1, y + 1, 2, x, y); // bottom side, walk west
      if (!at(x - 1, y)) addEdge(x, y + 1, 3, x, y); // left side, walk north
    }
  }

  // Fixed pairing at each vertex: for an incoming edge, continue on the
  // outgoing edge with the sharpest right turn (right > straight > left).
  const turnCost = (fromDir: number, toDir: number): number => {
    const turn = (toDir - fromDir + 4) % 4; // 0 straight, 1 right, 2 back, 3 left
    return turn === 1 ? 0 : turn === 0 ? 1 : turn === 3 ? 2 : 3;
  };
  const nextEdge = new Int32Array(edges.length).fill(-1);
  for (let i = 0; i < edges.length; i++) {
    const e = edges[i];
    const candidates = outgoing.get(vertexKey(e.x1, e.y1));
    if (!candidates) continue;
    let best = -1;
    let bestCost = Infinity;
    for (const c of candidates) {
      const cost = turnCost(e.dir, edges[c].dir);
      if (cost < bestCost) {
        bestCost = cost;
        best = c;
      }
    }
    nextEdge[i] = best;
  }

  // Follow the pairing; every edge belongs to exactly one closed ring.
  const visited = new Uint8Array(edges.length);
  const rings: Ring[] = [];
  for (let start = 0; start < edges.length; start++) {
    if (visited[start]) continue;
    const vertices: Point[] = [];
    const pixels: Point[] = [];
    let i = start;
    do {
      visited[i] = 1;
      const e = edges[i];
      vertices.push({ x: e.x0, y: e.y0 });
      const last = pixels[pixels.length - 1];
      if (!last || last.x !== e.px || last.y !== e.py) {
        pixels.push({ x: e.px, y: e.py });
      }
      i = nextEdge[i];
    } while (i !== start && i !== -1);
    // Drop a trailing pixel duplicate of the first (wrap-around artifact).
    if (pixels.length > 1) {
      const first = pixels[0];
      const last = pixels[pixels.length - 1];
      if (first.x === last.x && first.y === last.y) pixels.pop();
    }
    rings.push(makeRing(vertices, pixels));
  }
  return rings;
}

function makeRing(vertices: Point[], pixels: Point[]): Ring {
  let twiceArea = 0;
  for (let i = 0; i < vertices.length; i++) {
    const a = vertices[i];
    const b = vertices[(i + 1) % vertices.length];
    twiceArea += a.x * b.y - b.x * a.y;
  }
  const signedArea = twiceArea / 2;
  return {
    kind: signedArea > 0 ? "outer" : "hole",
    closed: true,
    vertices,
    pixels,
    signedArea,
    perimeter: vertices.length,
  };
}
