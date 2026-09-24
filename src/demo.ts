import { IncrementalLexer } from './lexer.ts';

const COLORS: Record<string, string> = {
  keyword: '\x1b[35m', // magenta
  identifier: '\x1b[37m', // white
  number: '\x1b[36m', // cyan
  string: '\x1b[33m', // yellow
  comment: '\x1b[32m', // green
  punctuation: '\x1b[90m', // gray
};
const RESET = '\x1b[0m';

function render(lexer: IncrementalLexer, title: string, invalidated?: number[]) {
  console.log(`\n=== ${title} ===`);
  const text = lexer.getText();
  const tokens = lexer.getTokens();
  let cursor = 0;
  let out = '';
  for (const t of tokens) {
    out += text.slice(cursor, t.start);
    out += COLORS[t.type] + text.slice(t.start, t.end) + RESET;
    cursor = t.end;
  }
  out += text.slice(cursor);
  console.log(out);
  console.log('tokens:', tokens.map((t) => `${t.type}@[${t.start},${t.end})`).join(' '));
  if (invalidated) console.log('invalidated lines:', invalidated.join(', '));
}

// 1. initial document (LF endings)
const src = [
  'let greeting = "hi \\"there\\""',
  '/* header comment',
  '   still comment */',
  'let count = 42 // tally',
  'let emoji = "\u{1F600}"',
].join('\n');
const lexer = new IncrementalLexer(src);
render(lexer, 'initial document');

// 2. delete the early "*/" -> the comment swallows the following lines
const close = src.indexOf('*/');
const r1 = lexer.applyEdit(close, close + 2, '');
render(lexer, 'after deleting the early "*/"', r1.invalidatedLines);

// 3. undo: put the "*/" back
const r2 = lexer.applyEdit(close, close, '*/');
render(lexer, 'after restoring "*/"', r2.invalidatedLines);

// 4. change one char inside an identifier -> only that line is re-lexed
const pos = lexer.getText().indexOf('count') + 2;
const r3 = lexer.applyEdit(pos, pos + 1, 'X');
render(lexer, 'after editing one char inside identifier "count"', r3.invalidatedLines);

// 5. CRLF document
const crlf = new IncrementalLexer('let a = 1\r\n/* c\r\nd */\r\nlet b = 2');
render(crlf, 'CRLF document');

// 6. surrogate-pair protection
try {
  const emojiAt = lexer.getText().indexOf('\u{1F600}');
  lexer.applyEdit(emojiAt + 1, emojiAt + 1, 'x'); // inside the pair
} catch (err) {
  console.log('\nediting inside a surrogate pair is rejected:');
  console.log(' ', (err as Error).message);
}
