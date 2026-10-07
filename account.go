package replay

import "fmt"

// AccountState is a concrete derived state used to demonstrate the
// executor: a balance folded from deposit/withdraw events.
type AccountState struct {
	Balance int64
	TxCount int
}

// AccountReducer is a pure, deterministic reducer. Each transaction
// declares a ledger posting; deposits above a threshold additionally
// declare a notification.
func AccountReducer(state State, ev Event) (State, []EffectSpec) {
	acct, _ := state.(AccountState)
	switch ev.Kind {
	case "deposit":
		acct.Balance += ev.Amount
	case "withdraw":
		acct.Balance -= ev.Amount
	default:
		return acct, nil
	}
	acct.TxCount++
	specs := []EffectSpec{{
		Kind:   "ledger.post",
		Detail: fmt.Sprintf("%s %d -> balance %d", ev.Kind, ev.Amount, acct.Balance),
	}}
	if ev.Kind == "deposit" && ev.Amount >= 1000 {
		specs = append(specs, EffectSpec{
			Kind:   "notify.user",
			Detail: fmt.Sprintf("large deposit %d", ev.Amount),
		})
	}
	return acct, specs
}
