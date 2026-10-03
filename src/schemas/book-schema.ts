/**
 * 书籍文档模式：
 *   book      → title, author, paragraph+   （标题、作者，后接 1 个或多个段落）
 *   author    → name, email                 （姓名与邮箱，按序且必填）
 *   title/name/email/paragraph → 纯文本叶子
 */
import { empty, label, plus, rule, Schema, seq } from '../schema.ts';

export const bookSchema = new Schema('book', [
  rule('book', seq(label('title'), label('author'), plus(label('paragraph')))),
  rule('author', seq(label('name'), label('email'))),
  rule('title', empty(), { text: 'required' }),
  rule('name', empty(), { text: 'required' }),
  rule('email', empty(), { text: 'required' }),
  rule('paragraph', empty(), { text: 'required' }),
]);
