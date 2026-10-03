import { BPlusTree } from "../src/bplustree.ts";

const N = 500;
const tree = BPlusTree.bulkLoad(Array.from({ length: N }, (_, i) => i + 1));

const fmt = (label: string) =>
  console.log(
    `${label.padEnd(22)} height=${tree.height()}  ` +
      `records=${tree.leafKeys().length.toString().padStart(3)}  ` +
      `pages(in-use/free/total)=${tree.allocatedPages}/${tree.freePages}/${tree.pm.capacity}  ` +
      `merges=${tree.mergeCount} borrows=${tree.borrowCount} rootShrinks=${tree.rootShrinkCount}`,
  );

fmt("bulk-loaded full tree");
let deleted = 0;
let lastHeight = tree.height();
for (let key = 1; key <= N - 3; key++) {
  tree.delete(key);
  deleted++;
  if (tree.height() !== lastHeight) {
    fmt(`after ${deleted} deletes`);
    lastHeight = tree.height();
  }
}
fmt(`final (${deleted} deletes)`);
console.log(`remaining records: [${tree.leafKeys().join(", ")}]`);
