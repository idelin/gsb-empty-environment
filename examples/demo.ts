import { diffTokens, verifyDiff } from '../src/index.ts';

const oldCode = 'def main ( ) { init ( ) ; run ( ) ; }'.split(' ');
const newCode = 'def main ( ) { run ( ) ; init ( ) ; log ( ) ; }'.split(' ');

const result = diffTokens(oldCode, newCode);

console.log('old:', result.oldTokens.map((t) => `${t.text}[${t.label}:${t.status}${t.blockId !== null ? '#' + t.blockId : ''}]`).join(' '));
console.log('new:', result.newTokens.map((t) => `${t.text}[${t.label}:${t.status}${t.blockId !== null ? '#' + t.blockId : ''}]`).join(' '));
console.log('blocks:', result.blocks);
console.log('stats:', result.stats);
console.log('verify:', verifyDiff(oldCode, newCode, result));
