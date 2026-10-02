import { SlottedPage, PageType, PAGE_SIZE, type Probe } from "../src/slottedPage.ts";

// --- Leaf page: 150 variable-length keys in one 4 KiB page -------------------
const leaf = SlottedPage.create(PageType.Leaf);
const keys: string[] = [];
for (let i = 0; i < 150; i++) {
  keys.push(`key-${String(i).padStart(4, "0")}${"x".repeat(i % 11)}`);
}

// Insert in reverse order to prove the slot directory keeps keys sorted.
for (const [i, key] of [...keys].reverse().entries()) {
  leaf.insertLeaf(key, { pageId: 7000 + i, slotId: i });
}

console.log(`leaf page: ${leaf.slotCount} slots, ${leaf.freeSpace} bytes free of ${PAGE_SIZE}`);
console.log(`first 3 keys in slot order: ${[...leaf.keys()].slice(0, 3).join(", ")}`);

// Look up an arbitrary key and report the binary-search probe count.
const probe: Probe = { comparisons: 0 };
const target = keys[117];
const loc = leaf.find(target, probe)!;
console.log(
  `find(${JSON.stringify(target)}) -> slot #${loc.slotIndex}, ` +
    `cell offset ${loc.offset}, ${probe.comparisons} comparisons ` +
    `(log2(150) ~ ${Math.log2(150).toFixed(1)})`,
);
console.log(`RID at that slot: ${JSON.stringify(leaf.readRid(loc))}`);

// --- Internal page: separator keys -> child page ids --------------------------
const internal = SlottedPage.create(PageType.Internal);
internal.insertInternal("key-0050", 11);
internal.insertInternal("key-0100", 12);
internal.insertInternal("key-0000", 10);

const hit = internal.find("key-0050")!;
console.log(
  `internal page separators: ${[...internal.keys()].join(" | ")}`,
);
console.log(`find("key-0050") -> child page ${internal.readChildPageId(hit)}`);
