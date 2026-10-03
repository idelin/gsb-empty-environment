/**
 * 运行样例：用书籍文档模式校验 JSON / XML 树。
 * 运行：npm run sample
 */
import { bookSchema } from '../src/schemas/book-schema.ts';
import { jsonToTree, xmlToTree } from '../src/tree.ts';
import { validate, type ValidationResult } from '../src/validator.ts';

function report(title: string, result: ValidationResult): void {
  console.log(`=== ${title} ===`);
  if (result.valid) {
    console.log('校验通过 ✔');
  } else {
    console.log(`校验失败，共 ${result.violations.length} 处违规：`);
    for (const violation of result.violations) {
      console.log(`  路径 ${violation.path}`);
      console.log(`    ${violation.message}`);
    }
  }
  console.log();
}

// 样例 1：缺失必要字段的 JSON 树（author 下缺少 name 与 email）
const missingFields = {
  book: {
    title: '树自动机导论',
    author: {},
    paragraph: ['第一段：什么是树自动机。', '第二段：如何用正规式约束子节点序列。'],
  },
};
report('JSON：缺失必要字段', validate(bookSchema, jsonToTree(missingFields)));

// 样例 2：完全合法的 JSON 树
const valid = {
  book: {
    title: '树自动机导论',
    author: { name: '张三', email: 'zhangsan@example.com' },
    paragraph: ['第一段。', '第二段。', '第三段。'],
  },
};
report('JSON：合法文档', validate(bookSchema, jsonToTree(valid)));

// 样例 3：子节点顺序错误的 XML 文档（author 出现在 title 之前）
const outOfOrder = `<?xml version="1.0"?>
<book>
  <author><name>李四</name><email>lisi@example.com</email></author>
  <title>结构模式校验</title>
  <paragraph>唯一段落。</paragraph>
</book>`;
report('XML：子节点顺序错误', validate(bookSchema, xmlToTree(outOfOrder)));

// 样例 4：合法的 XML 文档
const validXml = `
<book>
  <title>结构模式校验</title>
  <author><name>李四</name><email>lisi@example.com</email></author>
  <paragraph>唯一段落。</paragraph>
</book>`;
report('XML：合法文档', validate(bookSchema, xmlToTree(validXml)));
