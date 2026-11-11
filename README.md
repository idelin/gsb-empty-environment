# warcwriter

归档爬虫需要以WARC格式保存抓取结果。给Go爬虫实现WARC写入器：每条记录单独作为一个gzip成员，按WARC/1.1写request、response、metadata记录，Content-Length和块摘要正确；载荷摘要重复时改写为revisit记录，相关记录用WARC-Concurrent-To关联；文件按大小滚动并生成CDXJ索引。实现读取路径，按索引偏移随机读取记录并校验摘要。
