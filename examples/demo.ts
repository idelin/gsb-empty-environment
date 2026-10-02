/**
 * 运行样例：霍夫曼容错解码器 vs 普通解码器
 * 场景：位流第 50 字节处被信道抹去 3 个比特。
 *   node examples/demo.ts
 */
import { buildCodebook } from '../src/huffman.ts';
import {
  decodePlain,
  decodeResilient,
  encodePlain,
  encodeResilient,
  SEGMENT_SIZE_BYTES,
} from '../src/resilient.ts';
import { deleteBits } from '../src/channel.ts';
import { buildSampleText } from '../src/sample-text.ts';
import { longestCommonSuffixLength } from '../src/metrics.ts';

const text = buildSampleText(4000);
const symbols = [...text].map((ch) => ch.charCodeAt(0));
const codebook = buildCodebook(symbols);

console.log('=== 1. 原始文本与编码 ===');
console.log(`文本长度: ${text.length} 字符, 字母表大小: ${codebook.size}`);
console.log(`原文预览: ${JSON.stringify(text.slice(0, 80))}`);

const resilient = encodeResilient(symbols, codebook, { withHeader: false });
const plain = encodePlain(symbols, codebook, { withHeader: false });
console.log(`容错流: ${resilient.data.length} 字节, ${resilient.segmentCount} 个分段（每段 ${SEGMENT_SIZE_BYTES} 字节，段首为同步字 0x1ACFFC1D）`);
console.log(`普通流: ${plain.data.length} 字节（无同步机制）`);

console.log('\n=== 2. 信道事故：第 50 字节处抹去 3 个比特 ===');
const DELETE_BIT = 50 * 8;
const damagedResilient = deleteBits(resilient.data, DELETE_BIT, 3);
const damagedPlain = deleteBits(plain.data, DELETE_BIT, 3);
console.log(`受损容错流: ${damagedResilient.length} 字节（丢失 bit ${DELETE_BIT}..${DELETE_BIT + 2}）`);

console.log('\n=== 3. 普通解码器：损坏点后全部乱码 ===');
const naive = decodePlain(damagedPlain, codebook);
const naiveRatio = longestCommonSuffixLength(symbols, naive) / symbols.length;
const naiveText = String.fromCharCode(...naive);
console.log(`解码输出 ${naive.length} 符号，损坏点后可恢复比例: ${(naiveRatio * 100).toFixed(2)}%`);
console.log(`损坏点附近输出: ${JSON.stringify(naiveText.slice(80, 190))}`);
console.log(`原文对应位置  : ${JSON.stringify(text.slice(80, 190))}`);

console.log('\n=== 4. 容错解码器：同步字重锁，错误被隔离 ===');
const result = decodeResilient(damagedResilient, codebook);
for (const r of result.resyncs) {
  console.log(`重新锁定：在受损流比特 ${r.bitPos} 处捕获同步字（即原始流第 ${(r.bitPos + 3) / 8} 字节处的同步字，因抹去 3bit 前移）`);
}
console.log(`分段统计: ${result.segmentsOk} 段完好, ${result.segmentsLost} 段被隔离`);
const ratio = result.symbols.length / symbols.length;
console.log(`挽救内容: ${result.symbols.length}/${symbols.length} 符号 = ${(ratio * 100).toFixed(2)}%`);
const recovered = String.fromCharCode(...result.symbols);
console.log(`恢复内容开头（紧接被隔离段之后）: ${JSON.stringify(recovered.slice(0, 80))}`);
console.log(`恢复内容结尾: ${JSON.stringify(recovered.slice(-80))}`);
