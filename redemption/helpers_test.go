package redemption

import (
	"math/rand"
)

const (
	issuerA IssuerID   = "issuer-a"
	issuerB IssuerID   = "issuer-b"
	act1    ActivityID = "gen-1"
	act2    ActivityID = "gen-2"
)

func originInput(id TxID, idx int, issuer IssuerID, act ActivityID, amount int64, nf Nullifier) Input {
	commit := Commitment(string(issuer) + "/" + string(act) + "/c-" + string(nf))
	return Input{
		Source: NoteSource{Origin: &OriginNote{
			Issuer: issuer, Activity: act, Commitment: commit,
			Denomination: amount, Nullifier: nf,
		}},
		Issuer: issuer, Activity: act, Commitment: commit,
		Amount: amount, Nullifier: nf,
	}
}

func changeInput(txID TxID, outIdx uint8, issuer IssuerID, act ActivityID, amount int64, nf Nullifier, commit Commitment) Input {
	return Input{
		Source:     NoteSource{Outpoint: &Outpoint{TxID: txID, OutIndex: outIdx}},
		Issuer:     issuer,
		Activity:   act,
		Commitment: commit,
		Amount:     amount,
		Nullifier:  nf,
	}
}

func changeOut(issuer IssuerID, act ActivityID, amount int64, tag string) ChangeOutput {
	return ChangeOutput{
		Issuer:     issuer,
		Activity:   act,
		Commitment: Commitment("change/" + tag),
		Amount:     amount,
	}
}

func service(service ServiceID, issuer IssuerID, act ActivityID, units, cost int64) ServiceEntitlement {
	return ServiceEntitlement{Service: service, Issuer: issuer, Activity: act, Units: units, Cost: cost}
}

// acceptAll is a permissive host verifier used to isolate the structural
// selection rules from cryptography.
func acceptAll(_ OriginNote) error { return nil }

func acceptAllProof(_ Transaction) error { return nil }

// randomProblem builds a small random instance with two issuers, two
// activities, issued roots and chained change transactions. The generator
// always produces structurally valid transactions; feasibility (double spend,
// quotas, cycles) is left for the solver to judge.
func randomProblem(rng *rand.Rand, n int) Problem {
	p := Problem{
		Issuers: []IssuerID{issuerA, issuerB},
		Quotas: []Quota{
			{Issuer: issuerA, Activity: act1, Limit: int64(20 + rng.Intn(40))},
			{Issuer: issuerB, Activity: act1, Limit: int64(20 + rng.Intn(40))},
			{Issuer: issuerA, Activity: act2, Limit: int64(10 + rng.Intn(30))},
		},
	}
	issuers := []IssuerID{issuerA, issuerB}
	acts := []ActivityID{act1, act2}
	nullSeq := 0
	nextNull := func() Nullifier {
		nullSeq++
		return Nullifier("nf-" + itoa(nullSeq))
	}
	// available change notes: outpoint -> note
	type avail struct {
		tx     TxID
		idx    uint8
		issuer IssuerID
		act    ActivityID
		amount int64
		commit Commitment
	}
	pools := map[BalanceKey][]avail{}
	for i := 0; i < n; i++ {
		txID := TxID("tx-" + itoa(i+1))
		issuer := issuers[rng.Intn(2)]
		act := acts[rng.Intn(2)]
		key := BalanceKey{Issuer: issuer, Activity: act}
		pool := pools[key]

		var inputs []Input
		inTotal := int64(0)
		// Every transaction stays on a single balance key, so conservation
		// holds by construction.
		if len(pool) > 0 && rng.Intn(2) == 0 {
			pick := pool[rng.Intn(len(pool))]
			inputs = append(inputs, changeInput(pick.tx, pick.idx, pick.issuer, pick.act, pick.amount, nextNull(), pick.commit))
			inTotal = pick.amount
		} else {
			amount := int64(5 + rng.Intn(20))
			inputs = append(inputs, originInput(txID, 0, issuer, act, amount, nextNull()))
			inTotal = amount
		}
		// Roughly a third of transactions are competing branches that spend
		// the same one-time nullifier as an earlier transaction.
		if i > 0 && rng.Intn(3) == 0 {
			prev := p.Candidates[rng.Intn(len(p.Candidates))]
			src := prev.Inputs[0]
			if (BalanceKey{Issuer: src.Issuer, Activity: src.Activity}) == key {
				clone := Input{
					Source: src.Source, Issuer: src.Issuer, Activity: src.Activity,
					Commitment: src.Commitment, Amount: src.Amount, Nullifier: src.Nullifier,
				}
				// Replace the fresh input so totals remain consistent.
				inputs = []Input{clone}
				inTotal = src.Amount
			}
		}

		cost := int64(0)
		if inTotal > 1 && rng.Intn(4) != 0 {
			cost = int64(1 + rng.Intn(int(inTotal)))
		}
		change := inTotal - cost
		var outputs []ChangeOutput
		if change > 0 {
			out := changeOut(issuer, act, change, string(txID))
			outputs = append(outputs, out)
			pools[key] = append(pools[key], avail{
				tx: txID, idx: 0, issuer: issuer, act: act,
				amount: change, commit: out.Commitment,
			})
		}
		var spends []ServiceEntitlement
		if cost > 0 {
			spends = append(spends, service("svc", issuer, act, int64(1+rng.Intn(3)), cost))
		}
		p.Candidates = append(p.Candidates, Transaction{
			ID: txID, Inputs: inputs, Outputs: outputs, Spends: spends,
		})
	}
	return p
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for v > 0 {
		pos--
		buf[pos] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[pos:])
}
