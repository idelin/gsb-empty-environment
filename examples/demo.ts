import { planRename, applyPatch, bindProject } from '../src/index.ts';

const files: Record<string, string> = {
  'other.lang': `let x = 1;
let y = 2;
`,
  'main.lang': `import { x as a, y } from "./other.lang";
let x = 10;
fn f() {
  let y = 20;
  return y + x;
}
print(a);
print(y);
print(x);
`,
};

const bound = bindProject(new Map(Object.entries(files)));
console.log('symbols:');
for (const d of bound.decls.values()) {
  console.log(`  ${d.id}  ${d.kind}  ${d.name}`);
}

const outerX = [...bound.decls.values()].find((d) => d.file === 'main.lang' && d.name === 'x');
if (!outerX) throw new Error('missing symbol');

console.log('\n-- plan: rename main.lang x -> y (expect conflicts) --');
const bad = planRename(files, outerX.id, 'y');
if (!bad.ok) {
  for (const c of bad.conflicts) {
    console.log(`  [${c.kind}] ${c.file}:${c.line}:${c.column} '${c.name}' - ${c.message}`);
  }
}

console.log('\n-- plan: rename main.lang x -> count (expect ok) --');
const good = planRename(files, outerX.id, 'count');
if (good.ok) {
  console.log(`  version: ${good.patch.version.slice(0, 12)}...`);
  console.log(`  edits: ${good.patch.edits.length}`);
  const applied = applyPatch(files, good.patch);
  if (applied.ok) {
    console.log('\n-- main.lang after apply (re-parsed and verified) --');
    console.log(applied.sources.get('main.lang'));
  }
}
