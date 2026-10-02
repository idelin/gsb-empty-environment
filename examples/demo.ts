import { bookGrammar } from '../src/book-schema.ts';
import { validateDocument } from '../src/validator.ts';

const validBook = {
  book: {
    title: '树自动机导论',
    author: { name: '张三', email: 'zhangsan@example.com' },
    chapter: [
      { heading: '引言', paragraph: ['第一段', '第二段'] },
      { heading: '正文', paragraph: ['唯一段落'] },
    ],
  },
};

// 验收边界：缺失必要字段 author 的 JSON 树
const missingAuthor = {
  book: {
    title: '树自动机导论',
    chapter: [{ heading: '引言', paragraph: ['第一段'] }],
  },
};

function report(title: string, doc: unknown): void {
  console.log(`== ${title} ==`);
  const diagnostics = validateDocument(doc, bookGrammar);
  if (diagnostics.length === 0) {
    console.log('✓ 校验通过\n');
  } else {
    console.log(`✗ 校验失败，发现 ${diagnostics.length} 处问题:`);
    for (const d of diagnostics) {
      console.log(`  路径 ${d.path}: ${d.message}`);
    }
    console.log();
  }
}

report('合法书籍文档', validBook);
report('缺失 author 的书籍文档', missingAuthor);
