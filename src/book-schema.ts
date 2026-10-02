/**
 * 书籍文档模式（验收边界要求的树文法）：
 *
 *   book     -> title, author, chapter+        标题、作者，后接 1 个或多个章节
 *   author   -> name, email                    作者必须按序包含姓名与邮箱
 *   chapter  -> heading, paragraph+            章节标题后接 1 个或多个段落
 *   title/name/email/heading/paragraph -> #text
 */
import { defineGrammar } from './grammar.ts';
import type { Grammar } from './grammar.ts';
import { lit, seq, plus, text } from './pattern.ts';

export const bookGrammar: Grammar = defineGrammar('book-doc', 'book', {
  book: seq(lit('title'), lit('author'), plus(lit('chapter'))),
  author: seq(lit('name'), lit('email')),
  chapter: seq(lit('heading'), plus(lit('paragraph'))),
  title: text(),
  name: text(),
  email: text(),
  heading: text(),
  paragraph: text(),
});
