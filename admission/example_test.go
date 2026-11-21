package admission_test

import (
	"fmt"

	"prescreen.local/ddd-10496-v4/admission"
)

// 演示并行增量的持久化顺序未知时，如何得到保守的三分类准入结论。
func Example() {
	cp := admission.Checkpoint{
		Root:   []byte("R0"),
		Shards: []admission.Range{{0, admission.MaxPage}},
		Permissions: []admission.PermissionRange{
			{Pages: admission.Range{0, admission.MaxPage}, Perm: admission.PermExec},
		},
		Base: []admission.BaseContent{{
			Pages:  admission.Range{0, 15},
			Digest: []byte("base"),
			Trust:  admission.TrustTrusted,
		}},
	}

	// 两条并行内容增量都从 R0 分叉，谁先持久化无法确定。
	receipts := []admission.Receipt{
		{
			Publisher: "vendor",
			OldRoot:   []byte("R0"),
			NewRoot:   []byte("R1"),
			Pages:     admission.Range{0, 3},
			Kind:      admission.KindContent,
			Content: []admission.ContentEntry{{
				Pages: admission.Range{0, 3}, Digest: []byte("new-a"),
			}},
		},
		{
			Publisher: "vendor",
			OldRoot:   []byte("R0"),
			NewRoot:   []byte("R2"),
			Pages:     admission.Range{0, 3},
			Kind:      admission.KindContent,
			Content: []admission.ContentEntry{{
				Pages: admission.Range{0, 3}, Digest: []byte("new-b"),
			}},
		},
	}

	verifier := admission.VerifierFunc(func(admission.Receipt) admission.Trust {
		return admission.TrustTrusted
	})

	adm, err := admission.Build(admission.AdmissionInput{
		Checkpoint: cp,
		Receipts:   receipts,
		Request:    admission.Range{0, 7},
		Verifier:   verifier,
	})
	if err != nil {
		fmt.Println("build error:", err)
		return
	}
	decisions, err := adm.Admit()
	if err != nil {
		fmt.Println("admit error:", err)
		return
	}
	exec, forbidden, unknown := admission.RangesByVerdict(decisions)
	fmt.Println("executable:", exec)
	fmt.Println("forbidden:", forbidden)
	fmt.Println("unknown:", unknown)
	// Output:
	// executable: [{4 7}]
	// forbidden: []
	// unknown: [{0 3}]
}
