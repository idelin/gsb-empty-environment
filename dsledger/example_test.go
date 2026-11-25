package dsledger_test

import (
	"crypto/ed25519"
	"fmt"
	"os"

	"prescreen.local/ps-7934926941/dsledger"
)

func Example() {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		panic(err)
	}
	const id dsledger.MemberID = "validator-0"
	genesis := fixedDigest(0xaa)
	roster, err := dsledger.NewRoster("demo-chain", genesis,
		[]dsledger.Member{{ID: id, PublicKey: pub}})
	if err != nil {
		panic(err)
	}

	dir, err := os.MkdirTemp("", "dsledger-example-*")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	ledger, err := dsledger.Open(dir, roster)
	if err != nil {
		panic(err)
	}
	defer ledger.Close()

	block := fixedDigest(0xbb)
	// commit 乱序先到：缺 prepare，进入暂存。
	commit, _ := dsledger.SignShare(priv, roster.ChainID(), id, 1, 0, dsledger.PhaseCommit, block, genesis)
	first, _ := ledger.Ingest(commit)
	fmt.Println("commit-first:", first.Outcome)

	// prepare 补齐：自身发布并把暂存的 commit 一并泵出。
	prepare, _ := dsledger.SignShare(priv, roster.ChainID(), id, 1, 0, dsledger.PhasePrepare, block, genesis)
	second, _ := ledger.Ingest(prepare)
	fmt.Println("prepare:", second.Outcome, "released:", len(second.Released))

	// 同高度同阶段为另一区块签名：立即冻结并产出可独立验证的证据。
	other := fixedDigest(0xcc)
	conflict, _ := dsledger.SignShare(priv, roster.ChainID(), id, 1, 0, dsledger.PhasePrepare, other, genesis)
	third, _ := ledger.Ingest(conflict)
	fmt.Println("double-sign:", third.Outcome, third.Evidence.Kind)
	fmt.Println("evidence verifies:", dsledger.VerifyEvidence(roster, *third.Evidence) == nil)

	// Output:
	// commit-first: staged
	// prepare: released released: 1
	// double-sign: frozen same_height_block_conflict
	// evidence verifies: true
}

func fixedDigest(first byte) dsledger.Digest {
	var d dsledger.Digest
	d[0] = first
	return d
}
