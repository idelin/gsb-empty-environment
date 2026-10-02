/** Core data model for the atlas packer. All units are integer pixels. */

export interface FixedAnchor {
  page: number;
  x: number;
  y: number;
  rotated?: boolean;
}

export interface ItemSpec {
  id: string;
  /** Texture width in pixels (unrotated). */
  width: number;
  /** Texture height in pixels (unrotated). */
  height: number;
  /** Per-item keep-out margin around the sprite, in pixels. Default 0. */
  margin?: number;
  /** Whether a 90-degree rotation is allowed. Default true. */
  rotatable?: boolean;
  /** If set, the item is pinned to this exact spot and never moves. */
  fixed?: FixedAnchor;
}

export interface Placement {
  id: string;
  page: number;
  /** Actual sprite origin (margin and spacing excluded). */
  x: number;
  y: number;
  /** As-placed size: swapped relative to the texture when rotated. */
  width: number;
  height: number;
  rotated: boolean;
}

export type UnpackedReason = 'too-large' | 'no-space' | 'fixed-conflict';

export interface UnpackedItem {
  id: string;
  reason: UnpackedReason;
}

export type Proof =
  | { status: 'optimal'; pages: number }
  | { status: 'heuristic'; lowerBound: number; upperBound: number };

export interface PackResult {
  placements: Placement[];
  unpacked: UnpackedItem[];
  pages: number;
  proof: Proof;
}

export interface LayoutInput {
  pageWidth: number;
  pageHeight: number;
  /** Gap enforced between any two items' keep-out zones. Counted into capacity. */
  spacing: number;
  items: ItemSpec[];
}

export interface LayoutSnapshot extends LayoutInput {
  placements: Placement[];
  pages: number;
}

export interface UpdateChanges {
  add?: ItemSpec[];
  remove?: string[];
  resize?: { id: string; width: number; height: number }[];
}

export interface MoveOp {
  id: string;
  kind: 'add' | 'remove' | 'move' | 'stay';
  from: Placement | null;
  to: Placement | null;
}

export interface UpdateResult {
  ok: boolean;
  /** Published layout. On failure this is the previous layout, untouched. */
  placements: Placement[];
  pages: number;
  moves: MoveOp[];
  unpacked: UnpackedItem[];
  error?: string;
}
