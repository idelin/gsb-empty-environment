import { RGBAImage } from "./image.ts";
import { projectFace } from "./project.ts";
import { rasterizeOpaque, rasterizeTransparent } from "./raster.ts";
import type { Face, ProjectedFace, Viewport } from "./types.ts";

const EMPTY = 0xffffffff;

/**
 * Tiled renderer. The framebuffer is split into fixed-size tiles; every tile
 * is rendered independently from the scene (opaque pass, then transparent
 * faces in ascending id order), so the output does not depend on the order
 * in which tiles are executed (e.g. by a parallel scheduler).
 *
 * Mesh edits (setFace / removeFace) re-render only the tiles covered by the
 * old and/or new screen bbox of the face. Because tiles are re-rasterized
 * from the full scene rather than composited, deleting a foreground face
 * restores whatever it occluded.
 */
export class Renderer {
  readonly width: number;
  readonly height: number;
  readonly tileSize: number;
  readonly viewport: Viewport;
  readonly image: RGBAImage;
  readonly depth: Uint32Array;
  readonly ids: Uint32Array;
  readonly scene = new Map<number, Face>();
  readonly stats = { tilesRendered: 0 };
  private readonly projected = new Map<number, ProjectedFace>();

  constructor(width: number, height: number, tileSize = 8, viewport?: Viewport) {
    this.width = width;
    this.height = height;
    this.tileSize = tileSize;
    this.viewport = viewport ?? { x: 0, y: 0, width, height };
    this.image = new RGBAImage(width, height);
    this.depth = new Uint32Array(width * height);
    this.ids = new Uint32Array(width * height);
  }

  setFace(face: Face): void {
    const old = this.projected.get(face.id);
    this.scene.set(face.id, face);
    const proj = projectFace(face, this.viewport);
    if (proj) this.projected.set(face.id, proj);
    else this.projected.delete(face.id);
    this.rerender(unionBBox(old, proj));
  }

  removeFace(id: number): void {
    const old = this.projected.get(id);
    this.scene.delete(id);
    this.projected.delete(id);
    if (old) this.rerender(bboxOf(old));
  }

  /** Render every tile; `order` is an optional permutation of tile indices. */
  renderFull(order?: number[]): void {
    const txCount = Math.ceil(this.width / this.tileSize);
    const tyCount = Math.ceil(this.height / this.tileSize);
    const n = txCount * tyCount;
    const seq = order ?? Array.from({ length: n }, (_, i) => i);
    for (const i of seq) this.renderTile(i % txCount, Math.floor(i / txCount));
  }

  renderTile(tx: number, ty: number): void {
    const ts = this.tileSize;
    const vp = this.viewport;
    const x0 = Math.max(tx * ts, vp.x);
    const y0 = Math.max(ty * ts, vp.y);
    const x1 = Math.min((tx + 1) * ts, vp.x + vp.width, this.width);
    const y1 = Math.min((ty + 1) * ts, vp.y + vp.height, this.height);
    this.stats.tilesRendered++;
    if (x0 >= x1 || y0 >= y1) return;
    for (let py = y0; py < y1; py++) {
      for (let px = x0; px < x1; px++) {
        const idx = py * this.width + px;
        this.depth[idx] = EMPTY;
        this.ids[idx] = EMPTY;
        this.image.set(px, py, 0, 0, 0, 0);
      }
    }
    const sc = { x0, y0, x1, y1 };
    const faces: ProjectedFace[] = [];
    for (const f of this.projected.values()) {
      if (f.minX < x1 && f.maxX > x0 && f.minY < y1 && f.maxY > y0) faces.push(f);
    }
    for (const f of faces) {
      if (f.transparent) continue;
      for (const tri of f.tris) rasterizeOpaque(tri, f.id, this.image, this.depth, this.ids, sc);
    }
    const transparent = faces.filter((f) => f.transparent).sort((a, b) => a.id - b.id);
    for (const f of transparent) {
      for (const tri of f.tris) rasterizeTransparent(tri, f.id, this.image, this.depth, sc);
    }
  }

  private rerender(box: [number, number, number, number] | null): void {
    if (!box) return;
    const ts = this.tileSize;
    const txCount = Math.ceil(this.width / ts);
    const tyCount = Math.ceil(this.height / ts);
    const tx0 = Math.max(0, Math.floor(box[0] / ts));
    const ty0 = Math.max(0, Math.floor(box[1] / ts));
    const tx1 = Math.min(txCount - 1, Math.floor((box[2] - 1) / ts));
    const ty1 = Math.min(tyCount - 1, Math.floor((box[3] - 1) / ts));
    for (let ty = ty0; ty <= ty1; ty++) {
      for (let tx = tx0; tx <= tx1; tx++) this.renderTile(tx, ty);
    }
  }
}

function bboxOf(p: ProjectedFace): [number, number, number, number] {
  return [p.minX, p.minY, p.maxX, p.maxY];
}

function unionBBox(
  a: ProjectedFace | undefined,
  b: ProjectedFace | undefined,
): [number, number, number, number] | null {
  if (!a) return b ? bboxOf(b) : null;
  if (!b) return bboxOf(a);
  return [
    Math.min(a.minX, b.minX),
    Math.min(a.minY, b.minY),
    Math.max(a.maxX, b.maxX),
    Math.max(a.maxY, b.maxY),
  ];
}
