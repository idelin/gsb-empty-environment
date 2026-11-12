# assetlinks声明接管判定

Android App Link 靠域名下的 assetlinks.json 声明哪个应用可接管链接，攻击者发布包名相同但签名不同的应用，或在文件中混入其他关系的声明。为Go应用校验服务实现可导入的声明解析包：只认 relation 含 delegate_permission/common.handle_all_urls 且 target.namespace 为 android_app 的语句；sha256_cert_fingerprints 的十六进制按冒号分隔、大小写不敏感，须与应用签名证书之一完全相等；响应要求 https、状态 200、无重定向、Content-Type 为 application/json 且不超过 1 MiB；同一包名多条语句取并集。用错指纹、其他 relation、重定向与超限响应验证。
