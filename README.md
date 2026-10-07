# frozen-submit-form

TypeScript 表单状态库：提交时冻结字段快照，响应只结算对应版本的提交记录。

## 核心语义

- **提交冻结快照**：`submit()` 深拷贝并 `Object.freeze` 当前值作为 `snapshot` 发给服务端；之后的编辑不会改动已发出的快照。
- **响应只确认该版本**：每次提交生成递增 `submissionId` 与一条 `SubmissionRecord`；响应到达时只结算该记录。确认后 `baseValues` 更新为提交版本，飞行期间的新编辑与新基线不同，**保持脏**。
- **字段错误按值应用**：服务端返回的 `fieldErrors` 只应用到「当前值仍等于提交值」的字段；被用户改过的字段不应用，错误仅保留在提交记录上（`record.fieldErrors` / `record.appliedFieldErrors`）。
- **其他错误保留作提交记录**：`formErrors` 只挂在 `SubmissionRecord` 上，不落到任何字段。
- **重复提交**：同一时刻只允许一个在途提交，重复 `submit()` 抛 `SubmissionInFlightError`。
- **取消**：`cancelSubmit()` 将记录置为 `cancelled` 并触发 `AbortSignal`；迟到的响应（成功或失败）一律忽略。
- **字段被删**：飞行期间删除的字段，其错误不应用（无活体字段），确认时也不会被 `confirmedValues` 复活，基线同步移除。

## 用法

```ts
import { FormStore } from "frozen-submit-form";

const form = new FormStore({
  initialValues: { email: "", name: "" },
  submitter: async (snapshot, { signal }) => {
    const res = await fetch("/api/profile", {
      method: "POST",
      body: JSON.stringify(snapshot),
      signal,
    });
    if (res.ok) return { ok: true };
    const body = await res.json();
    return { ok: false, fieldErrors: body.fieldErrors, formErrors: body.formErrors };
  },
});

form.setValue("email", "a@b.c");
const { promise } = form.submit();
form.setValue("name", "Ada"); // 飞行期间编辑，确认后仍为脏
const record = await promise; // SubmissionRecord: status / fieldErrors / formErrors
```

## 测试

需要 Node ≥ 22.6（类型剥离，仓库 `.nvmrc` 指定 v23.9.0），无第三方依赖：

```sh
nvm use   # 或任何 >= 22.6 的 Node
npm test
```

测试通过 `src/testing.ts` 的 `createControllableSubmitter()` 拿到可手动 `resolve`/`reject` 的请求句柄，确定性核对：快照冻结、版本确认、飞行期编辑保持脏、字段错误按值应用、表单错误入记录、重复提交、取消、字段被删、传输异常。
