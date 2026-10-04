import { readFileSync, writeFileSync, mkdirSync } from 'node:fs';
import { join } from 'node:path';
import { FileReader, HashTree, type RangeProof } from './tree.ts';
import { verifyRange, type RangeReport } from './verify.ts';

function usage(): never {
  console.error(`hash-tree — chunked file verification tool

Commands:
  build  <file> [--chunk-size N] [--tree-out tree.json]
  prove  <file> --tree tree.json --start S --end E --proof-out proof.json --chunks-dir DIR
  verify --tree tree.json --start S --end E --proof proof.json --chunks-dir DIR
`);
  process.exit(2);
}

function argValue(args: string[], name: string): string | null {
  const i = args.indexOf(name);
  return i >= 0 && i + 1 < args.length ? args[i + 1] : null;
}

interface TreeFile {
  format: 'hash-tree/1';
  fileLength: number;
  chunkSize: number;
  commitment: string;
  levels: string[][];
}

function loadTree(path: string): { tree: HashTree; file: TreeFile } {
  const file = JSON.parse(readFileSync(path, 'utf8')) as TreeFile;
  if (file.format !== 'hash-tree/1') throw new Error('unknown tree format');
  return { tree: HashTree.fromLevels(file.fileLength, file.chunkSize, file.levels), file };
}

const [cmd, ...args] = process.argv.slice(2);

if (cmd === 'build') {
  const file = args[0];
  if (!file) usage();
  const chunkSize = Number(argValue(args, '--chunk-size') ?? 4096);
  const reader = new FileReader(file);
  const tree = HashTree.build(reader, chunkSize);
  reader.close();
  const out: TreeFile = {
    format: 'hash-tree/1',
    fileLength: tree.fileLength,
    chunkSize: tree.chunkSize,
    commitment: tree.commitment.toString('hex'),
    levels: tree.toLevelsHex(),
  };
  const treePath = argValue(args, '--tree-out') ?? `${file}.htree.json`;
  writeFileSync(treePath, JSON.stringify(out));
  console.log(`fileLength=${out.fileLength} chunkSize=${out.chunkSize}`);
  console.log(`commitment=${out.commitment}`);
  console.log(`tree written to ${treePath}`);
} else if (cmd === 'prove') {
  const file = args[0];
  const treePath = argValue(args, '--tree');
  const start = Number(argValue(args, '--start'));
  const end = Number(argValue(args, '--end'));
  const proofOut = argValue(args, '--proof-out') ?? 'proof.json';
  const chunksDir = argValue(args, '--chunks-dir') ?? 'chunks';
  if (!file || !treePath) usage();
  const { tree } = loadTree(treePath);
  const reader = new FileReader(file);
  const { proof, chunks } = tree.proveRange(reader, start, end);
  reader.close();
  mkdirSync(chunksDir, { recursive: true });
  for (const [i, data] of chunks) writeFileSync(join(chunksDir, `chunk-${i}.bin`), data);
  writeFileSync(proofOut, JSON.stringify(proof, null, 2));
  console.log(`proof for [${start}, ${end}) -> ${proofOut}, chunks -> ${chunksDir}`);
} else if (cmd === 'verify') {
  const treePath = argValue(args, '--tree');
  const start = Number(argValue(args, '--start'));
  const end = Number(argValue(args, '--end'));
  const proofPath = argValue(args, '--proof');
  const chunksDir = argValue(args, '--chunks-dir');
  if (!treePath || !proofPath || !chunksDir) usage();
  const { file } = loadTree(treePath);
  const proof = JSON.parse(readFileSync(proofPath, 'utf8')) as RangeProof;
  const chunks = new Map<number, Buffer>();
  for (const i of proof.leafIndices) {
    try {
      chunks.set(i, readFileSync(join(chunksDir, `chunk-${i}.bin`)));
    } catch {
      // chunk not received: reported as missing, not corrupt
    }
  }
  const report: RangeReport = verifyRange({
    commitment: Buffer.from(file.commitment, 'hex'),
    fileLength: file.fileLength,
    chunkSize: file.chunkSize,
    start,
    end,
    chunks,
    proof,
  });
  for (const c of report.chunks) {
    console.log(`chunk ${c.index}: ${c.status}${c.detail ? ` (${c.detail})` : ''}`);
  }
  console.log(report.ok ? 'OK: range verified' : 'FAILED: range did not verify');
  process.exit(report.ok ? 0 : 1);
} else {
  usage();
}
