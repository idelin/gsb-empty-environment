# C++17 联合准入组件

服务滚动升级时，同一份已经校验的缓冲会同时交给旧、新两种消费视图。
本仓库提供一个无外部依赖、可静态嵌入的 C++17 组件：它只接受冻结布局、
字段读取区间、受签可见性清单，以及可选的压缩证明；准入成功后才签发只读句柄。

## 构建与测试

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build -j
ctest --test-dir build --output-on-failure
```

产物为 `libjoint_admission.a`，公开头文件是
`include/ja/joint_admission.hpp`。实现不使用第三方库。

## 模型

- `Layout` 声明消息长度域 `[min_length, max_length]`，上界最大为 `2^40`。
- `Field::span` 是半开区间 `[begin(L), end(L))`。`begin`/`end` 是关于
  消息长度 `L` 的线性表达式，因此可表达定长、尾部变长和带上界的尾部变长
  字段。常用构造器为 `Span::fixed`、`Span::to_end`、
  `Span::bounded_tail`。
- `Layout::discriminant` 是两边都可读取的公开判别字段。`TagBinding` 把原始
  判别值区间映射到语义 `CaseId`；新旧布局可以为同一 `CaseId` 使用不同的
  原始数值，从而支持迁移前后的判别值重命名。
- `Arm` 是按 `CaseId` 选择的字段集合；`common_fields` 对全部类别生效。
- `Field::shared_object` 指向 `Layout::shared_objects` 中的共享子对象。
  字段必须始终位于该子对象内；同一共享子对象上的非零语义解释必须一致。
- `Compatibility` 声明旧字段到新片段的有序映射。片段按顺序无洞、无重叠地
  拼成旧字段，且被引用的新字段本身不得越出旧字段范围。这样旧字段可以拆成
  多个新字段，但新别名不能顺手扩大可读字节范围。

## 可见性清单与签名

`VisibilityManifest` 由 `generation`、`grants` 和 `denials` 组成。一个字段
可读，当且仅当它的完整区间被该视图的 grant 并集覆盖，且不与任何 denial
相交。`denials` 可用来表达策略撤销。

签名校验通过 `ManifestVerifier` 注入：

```cpp
using Verifier =
    std::function<bool(const ja::VisibilityManifest&, const ja::Signature&)>;
```

`canonical_manifest()` 给出稳定的待签名字节串；宿主可接入 Ed25519、HMAC
或其他现有签名设施。`sha256()` 和 `layout_digest()` 仅提供摘要能力。
组件不会把未通过 verifier 的清单用于任何权限判断。

## 压缩证明

`AdmissionProof` 只保存两类有限分区：

- `tags`: 每个视图的原始判别值区间到 `CaseId` 的映射；
- `lengths`: 每个视图、每个 `CaseId` 的消息长度区间。

独立入口 `JointAdmission::verify()` 会拒绝缺类、重叠、越界、生成代次不匹配，
以及同一原始判别值在新旧视图中解释不同的证明。证明不会列出所有长度，也不会
展开字节。

`admit(..., nullptr)` 可由组件自己生成规范分区；传入宿主证明时，则使用宿主
提供的压缩分区进行独立校验。

## 全称检查

组件把所有字段端点、共享对象端点、权限规则端点和兼容片段端点视为关于 `L`
的仿射函数。它只在这些函数相交或与 `0`/`L` 相交的临界长度处取样，并在相邻
临界点之间取代表点。由于端点序关系在每个开区间内不变，长度域上的下列条件
得到全称检查：

- 每个字段都在 `[0, L)` 内；
- 每个字段都被对应视图的 grant 覆盖且不与 denial 相交；
- 共享字段始终位于共享子对象内；
- 旧字段到新片段的拼接始终精确等于旧字段；
- 同一类别中重叠的非零语义解释不矛盾。

检查复杂度与端点数量相关，与 `L` 的数值大小无关。

## 失败与成功

失败只返回两个编号：

```cpp
struct Failure {
  ja::Obligation layout;
  ja::Obligation permission;
};
```

- `layout` 指向出问题的布局、字段、判别分区或兼容义务；
- `permission` 指向相关权限义务；若失败是缺 grant 等“无具体规则”情形，则为 `0`；
- 签名失败使用 `SignedManifest::signature_obligation`。

成功只产生 `ReadOnlyHandle`。句柄是不可写的能力对象，绑定：

- 旧布局的 SHA-256 摘要；
- 新布局的 SHA-256 摘要；
- 可见性策略代次。

`read()` 返回借用调用方缓冲的 `ByteView`，其中指针始终是 `const`；越界、
不匹配的长度/判别值、未知字段都会返回 `nullopt`。

## 最小示例

```cpp
ja::Layout old_layout;
old_layout.obligation = 10;
old_layout.min_length = old_layout.max_length = 8;
old_layout.arms.push_back({ja::kDefaultCase, 11,
                           {{1, 12, ja::Span::fixed(4, 4)}}});

ja::Layout new_layout;
new_layout.obligation = 20;
new_layout.min_length = new_layout.max_length = 8;
new_layout.arms.push_back({ja::kDefaultCase, 21,
                           {{11, 22, ja::Span::fixed(4, 4)}}});
new_layout.compatibility.push_back(
    ja::Compatibility{30, 1, {{31, 11, 0, std::nullopt}}});

ja::VisibilityManifest manifest;
manifest.generation = 7;
manifest.grants.push_back({40, ja::View::Old, ja::Span::fixed(4, 4)});
manifest.grants.push_back({41, ja::View::New, ja::Span::fixed(4, 4)});

ja::SignedManifest signed_manifest;
signed_manifest.manifest = manifest;
signed_manifest.signature = host_sign(ja::canonical_manifest(manifest));

ja::JointAdmission admission(host_verify);
ja::AdmissionOutcome outcome =
    admission.admit(old_layout, new_layout, signed_manifest);
if (!outcome) {
  log_obligations(outcome.failure.layout, outcome.failure.permission);
  return;
}

ja::Message message{buffer, size, tag};
if (auto bytes = outcome.handle->read(ja::View::New, 11, message)) {
  consume(bytes->data, bytes->size);
}
```

## 测试覆盖

`tests/joint_admission_test.cpp` 做小空间穷举和边界测试，包括：

- 同一共享子对象在旧、新入口具有不同可见区间；
- 旧变长字段跨边界拆成两个新片段，并检查 `2^40` 长度；
- 判别值从 `1/2` 重命名到 `101/102`，但保持同一语义类别；
- 策略撤销、签名篡改、漏掉证明类别、重叠证明类别；
- 重叠字段的冲突语义解释；
- 新别名试图吞入旧字段之外的字节；
- 运行时拒绝越界长度、空缓冲和未知字段。
