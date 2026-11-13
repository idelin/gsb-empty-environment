# largeBlob数组摘要与解压上限

凭据 largeBlob 存在认证器返回的序列化数组里，攻击者改数组或把 origSize 写得极大诱发解压炸弹。为TypeScript浏览器宿主写可导入的 largeBlob 读取包：尾部 16 字节须等于前文 SHA-256 的前 16 字节；条目用 AES-256-GCM 解密，附加数据为 "blob" 加小端 8 字节 origSize；解压长度须等于 origSize 且不超 1 MiB。
