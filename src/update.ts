import { PagePacker } from './maxrects.ts';
import { footprintRectOf, fitsPage } from './packer.ts';
import {
  footprintSize,
  itemRotatable,
  packingOrder,
  spriteOrigin,
  compareIds,
} from './geometry.ts';
import { verifyAll } from './verifier.ts';
import type {
  ItemSpec,
  LayoutSnapshot,
  MoveOp,
  Placement,
  UnpackedItem,
  UpdateChanges,
  UpdateResult,
} from './types.ts';

/**
 * Apply add / remove / resize changes to an existing layout.
 *
 * Policy: first minimize the number of moved placements (everything that is
 * still valid stays exactly where it is), then minimize the page count for
 * whatever must be (re)packed. The result is an atomic move plan: the new
 * layout is verified by the independent pixel-occupancy verifier before
 * publication, and on any failure the previous layout is returned untouched
 * so overlapping coordinates are never published.
 */
export function planUpdate(prev: LayoutSnapshot, changes: UpdateChanges): UpdateResult {
  const removedIds = new Set(changes.remove ?? []);
  const resizeMap = new Map((changes.resize ?? []).map((r) => [r.id, r]));
  const added = changes.add ?? [];

  // Final item table.
  const itemMap = new Map<string, ItemSpec>();
  for (const item of prev.items) {
    if (removedIds.has(item.id)) continue;
    const rz = resizeMap.get(item.id);
    itemMap.set(item.id, rz ? { ...item, width: rz.width, height: rz.height } : item);
  }
  for (const item of added) {
    if (itemMap.has(item.id)) {
      return fail(prev, `add conflicts with existing item id: ${item.id}`);
    }
    itemMap.set(item.id, item);
  }
  const items = [...itemMap.values()];

  const prevById = new Map(prev.placements.map((p) => [p.id, p]));
  const unpacked: UnpackedItem[] = [];
  const kept: Placement[] = [];
  const toPack: ItemSpec[] = [];

  // Fixed items always sit at their anchor (authoritative, never "moved").
  const fixedPlacements: Placement[] = [];
  const fixedObstacles = new Map<number, { x: number; y: number; w: number; h: number }[]>();
  for (const item of items.filter((it) => it.fixed).sort((a, b) => compareIds(a.id, b.id))) {
    const a = item.fixed!;
    const rotated = a.rotated ?? false;
    const width = rotated ? item.height : item.width;
    const height = rotated ? item.width : item.height;
    const bad = a.page < 0 || a.x < 0 || a.y < 0 ||
      a.x + width > prev.pageWidth || a.y + height > prev.pageHeight;
    if (bad) {
      unpacked.push({ id: item.id, reason: 'fixed-conflict' });
      continue;
    }
    const rect = footprintRectOf(item, { x: a.x, y: a.y, width, height },
      prev.spacing, prev.pageWidth, prev.pageHeight);
    const list = fixedObstacles.get(a.page) ?? [];
    if (list.some((o) => o.x < rect.x + rect.w && rect.x < o.x + o.w &&
                          o.y < rect.y + rect.h && rect.y < o.y + o.h)) {
      unpacked.push({ id: item.id, reason: 'fixed-conflict' });
      continue;
    }
    list.push(rect);
    fixedObstacles.set(a.page, list);
    fixedPlacements.push({ id: item.id, page: a.page, x: a.x, y: a.y, width, height, rotated });
  }

  // Movable items: keep every previous placement that is still valid.
  const movableItems = items.filter((it) => !it.fixed);
  const keptFootprints = new Map<number, { x: number; y: number; w: number; h: number; id: string }[]>();
  const footprintOf = (item: ItemSpec, pl: Placement) =>
    footprintRectOf(item, pl, prev.spacing, prev.pageWidth, prev.pageHeight);

  for (const item of movableItems.sort((a, b) => compareIds(a.id, b.id))) {
    const old = prevById.get(item.id);
    const unchanged = old && !resizeMap.has(item.id) &&
      old.width === item.width && old.height === item.height;
    if (!unchanged) {
      if (!fitsPage(prev, item)) unpacked.push({ id: item.id, reason: 'too-large' });
      else toPack.push(item);
      continue;
    }
    // A kept placement must still be inside the page and clear of fixed items.
    const rect = footprintOf(item, old);
    const hitsFixed = (fixedObstacles.get(old.page) ?? []).some((o) =>
      o.x < rect.x + rect.w && rect.x < o.x + o.w && o.y < rect.y + rect.h && rect.y < o.y + o.h);
    const outOfPage = old.page < 0 || old.x < 0 || old.y < 0 ||
      old.x + old.width > prev.pageWidth || old.y + old.height > prev.pageHeight;
    if (hitsFixed || outOfPage) {
      toPack.push(item); // evicted: must move, minimal set is enforced below
      continue;
    }
    const list = keptFootprints.get(old.page) ?? [];
    list.push({ ...rect, id: item.id });
    keptFootprints.set(old.page, list);
    kept.push(old);
  }

  // Resolve any residual conflicts among kept placements (evict the minimal
  // number: on each conflict, evict the item with the larger footprint).
  for (const [page, list] of keptFootprints) {
    for (let i = 0; i < list.length; i++) {
      for (let j = i + 1; j < list.length; j++) {
        const a = list[i]!;
        const b = list[j]!;
        const overlap = a.x < b.x + b.w && b.x < a.x + a.w && a.y < b.y + b.h && b.y < a.y + a.h;
        if (!overlap) continue;
        const evictId = a.w * a.h >= b.w * b.h ? a.id : b.id;
        const idx = kept.findIndex((p) => p.id === evictId);
        if (idx >= 0) {
          kept.splice(idx, 1);
          toPack.push(itemMap.get(evictId)!);
        }
        const li = list.findIndex((r) => r.id === evictId);
        if (li >= 0) list.splice(li, 1);
        i = -1;
        break;
      }
    }
    keptFootprints.set(page, list);
  }

  // Pack the evicted / added / resized items: existing pages first (fewer
  // moves already achieved; now fewer new pages), then fresh pages.
  const pageCount = () => Math.max(prev.pages, ...[...fixedPlacements, ...kept].map((p) => p.page + 1), 0);
  const packers: PagePacker[] = [];
  const packerFor = (p: number): PagePacker => {
    while (packers.length <= p) {
      const pk = new PagePacker(prev.pageWidth, prev.pageHeight);
      for (const ob of fixedObstacles.get(packers.length) ?? []) pk.addObstacle(ob);
      for (const kf of keptFootprints.get(packers.length) ?? []) pk.addObstacle(kf);
      packers.push(pk);
    }
    return packers[p]!;
  };
  for (let p = 0; p < pageCount(); p++) packerFor(p);

  const newPlacements: Placement[] = [];
  for (const item of packingOrder(toPack, prev.spacing)) {
    const { fw, fh } = footprintSize(item, prev.spacing);
    let placed: Placement | null = null;
    for (let p = 0; p < packers.length && !placed; p++) {
      const spot = packers[p]!.place(fw, fh, itemRotatable(item));
      if (spot) {
        const origin = spriteOrigin(spot.x, spot.y, item, prev.spacing);
        placed = {
          id: item.id, page: p, x: origin.x, y: origin.y,
          width: spot.rotated ? item.height : item.width,
          height: spot.rotated ? item.width : item.height,
          rotated: spot.rotated,
        };
      }
    }
    if (!placed) {
      for (let p = packers.length; ; p++) {
        const spot = packerFor(p).place(fw, fh, itemRotatable(item));
        if (spot) {
          const origin = spriteOrigin(spot.x, spot.y, item, prev.spacing);
          placed = {
            id: item.id, page: p, x: origin.x, y: origin.y,
            width: spot.rotated ? item.height : item.width,
            height: spot.rotated ? item.width : item.height,
            rotated: spot.rotated,
          };
          break;
        }
      }
    }
    newPlacements.push(placed!);
  }

  const finalPlacements = [...fixedPlacements, ...kept, ...newPlacements]
    .sort((a, b) => compareIds(a.id, b.id));
  const finalPages = Math.max(0, ...finalPlacements.map((p) => p.page + 1));

  // Atomic gate: independent verification before anything is published.
  const check = verifyAll(prev.pageWidth, prev.pageHeight, prev.spacing, items, finalPlacements);
  if (!check.ok) {
    return fail(prev, `verification failed: ${check.errors.join('; ')}`);
  }

  // Move plan.
  const moves: MoveOp[] = [];
  const finalById = new Map(finalPlacements.map((p) => [p.id, p]));
  for (const old of prev.placements) {
    const next = finalById.get(old.id);
    if (!next) {
      if (!unpacked.some((u) => u.id === old.id)) {
        moves.push({ id: old.id, kind: 'remove', from: old, to: null });
      }
      continue;
    }
    const same = old.page === next.page && old.x === next.x && old.y === next.y &&
      old.width === next.width && old.height === next.height && old.rotated === next.rotated;
    moves.push({ id: old.id, kind: same ? 'stay' : 'move', from: old, to: next });
  }
  for (const next of finalPlacements) {
    if (!prevById.has(next.id)) moves.push({ id: next.id, kind: 'add', from: null, to: next });
  }
  moves.sort((a, b) => compareIds(a.id, b.id));

  return { ok: true, placements: finalPlacements, pages: finalPages, moves, unpacked };
}

function fail(prev: LayoutSnapshot, error: string): UpdateResult {
  return {
    ok: false,
    placements: prev.placements,
    pages: prev.pages,
    moves: [],
    unpacked: [],
    error,
  };
}
