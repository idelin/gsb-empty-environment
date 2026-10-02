import { MerkleTree } from './merkle.ts';
import { Anchorer, AnchorVerifier } from './anchorer.ts';
import { issueCertificate, verifyCertificate } from './certificate.ts';

// Injectable clock so the demo can pin the acceptance scenario: anchor at t=1000.
let now = 1000;
const anchorer = new Anchorer(() => now);

console.log('=== t=1000: business records enter the system ===');
const records = ['order#A-1001 amount=500', 'order#A-1002 amount=720', 'order#A-1003 amount=315'];
const tree = new MerkleTree(records);
console.log('merkle root:', tree.root.toString('hex'));

console.log('\n=== t=1000: scheduled anchor job signs root + timestamp ===');
const anchor = anchorer.anchor(tree.root);
console.log('anchor record:', JSON.stringify(anchor.record, null, 2));
console.log('signature:', anchor.signature.slice(0, 32) + '...');

const registry = new AnchorVerifier(anchorer.publicKeyPem);
registry.accept(anchor);
console.log('registry accepted anchor seq=0 at t=1000');

console.log('\n=== t=2500: auditor requests an existence certificate for order#A-1002 ===');
now = 2500;
const cert = issueCertificate(tree, 1, anchor);
const verdict = verifyCertificate(records[1], cert, anchorer.publicKeyPem);
console.log('certificate valid:', verdict.valid);
console.log(`=> proves "${records[1]}" existed on or before t=${verdict.existedBefore}`);

console.log('\n=== attack 1: replay the same anchor to the registry ===');
try {
  registry.accept(anchor);
  console.log('FAIL: replay accepted');
} catch (e) {
  console.log('rejected:', (e as Error).message);
}

console.log('\n=== attack 2: forge a certificate for data never anchored ===');
const forged = verifyCertificate('order#A-9999 amount=1', cert, anchorer.publicKeyPem);
console.log('forged cert valid:', forged.valid, '-', forged.reason);

console.log('\n=== attack 3: tamper with the anchored timestamp ===');
const tampered = structuredClone(cert);
tampered.anchor.record.timestamp = 500;
const tVerdict = verifyCertificate(records[1], tampered, anchorer.publicKeyPem);
console.log('tampered cert valid:', tVerdict.valid, '-', tVerdict.reason);
