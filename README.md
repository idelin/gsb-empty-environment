# Tableland写入策略的语句改写

Tableland 式去中心化表允许表的控制者设置写入策略（可更新列、WHERE 约束、WITH CHECK），攻击者在自己的 UPDATE 或 DELETE 的条件里写 OR 1=1 或夹带多条语句，就想越过策略。给Go执行层写供写入语句调用的策略强制包，语句已解析成语法树传入：策略 WHERE 以 AND 合并到用户条件，合并时给用户条件整体加括号，不以字符串拼接；UPDATE 的 SET 只能是 updatableColumns 的子集；INSERT 的新行逐行经 WITH CHECK 求值，不满足整条语句失败；一条语句影响行数不超过策略上限，超出则回滚；子查询、多语句和对其他表的引用一律拒绝；策略表达式只能引用列、字面量、调用者地址与 block_num。用 OR 1=1、越权 SET 列、不满足 WITH CHECK 的插入、超过行数上限和多语句验证。
