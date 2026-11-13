package v0msg

import (
	"errors"
	"fmt"
)

var (
	// ErrTooManyAccounts is returned when the full account list
	// (static + loaded) exceeds MaxAccounts.
	ErrTooManyAccounts = errors.New("v0msg: total account count exceeds 256")
	// ErrDuplicateAccount is returned when the same address appears more
	// than once in the full account list (static or loaded).
	ErrDuplicateAccount = errors.New("v0msg: duplicate account address")
	// ErrProgramIndexNotStatic is returned when an instruction's program
	// id index points outside the static account keys (e.g. into a
	// lookup-table-loaded account).
	ErrProgramIndexNotStatic = errors.New("v0msg: program id index is not a static account")
	// ErrAccountIndexOutOfRange is returned when an instruction references
	// an account index beyond the full account list.
	ErrAccountIndexOutOfRange = errors.New("v0msg: instruction account index out of range")
	// ErrDeniedAccount is returned when a denylisted address is found
	// anywhere in the full account list, including lookup-table-loaded
	// accounts. The Report is still returned for inspection.
	ErrDeniedAccount = errors.New("v0msg: denylisted account present")
	// ErrTableIndexOutOfRange is returned when a lookup references an
	// index beyond the injected table's addresses.
	ErrTableIndexOutOfRange = errors.New("v0msg: lookup table index out of range")
	// ErrBadHeader is returned when the header counts are inconsistent
	// with the static key count.
	ErrBadHeader = errors.New("v0msg: header counts inconsistent with static keys")
)

// TableResolver supplies the contents of an address lookup table. It is
// injected by the caller; the package never fetches tables itself.
type TableResolver func(table [32]byte) (addresses [][32]byte, err error)

// Options configures Inspect.
type Options struct {
	// ResolveTable resolves a lookup table address to its addresses.
	// Required when the message contains lookup table references.
	ResolveTable TableResolver
	// Denylist is the set of addresses that must not appear anywhere in
	// the full account list.
	Denylist [][32]byte
}

// Account describes one entry of the resolved full account list.
type Account struct {
	Address  [32]byte
	Index    int  // position in the full account list
	Static   bool // from the message's static keys (vs. table-loaded)
	Signer   bool
	Writable bool
	Denied   bool // present in Options.Denylist
}

// Report is the result of a successful or partially-successful Inspect.
// Accounts is always the full ordered list: static keys first, then each
// table's writable loads, then each table's readonly loads.
type Report struct {
	Accounts []Account
	// DeniedHits lists indexes into Accounts that matched the denylist.
	DeniedHits []int
}

// Inspect parses raw, resolves lookup tables via opts, and enforces the
// pre-signing policy on the full account list. It returns the Report even
// when a policy error occurs, so callers can surface details.
func Inspect(raw []byte, opts Options) (*Report, error) {
	msg, err := ParseMessage(raw)
	if err != nil {
		return nil, err
	}

	nStatic := len(msg.StaticKeys)
	nSig := int(msg.Header.NumRequiredSignatures)
	nRoSig := int(msg.Header.NumReadonlySignedAccounts)
	nRoNonSig := int(msg.Header.NumReadonlyUnsignedAccounts)
	if nRoSig > nSig || nSig > nStatic || nSig+nRoNonSig > nStatic {
		return nil, fmt.Errorf("%w: sigs=%d roSig=%d roNonSig=%d static=%d",
			ErrBadHeader, nSig, nRoSig, nRoNonSig, nStatic)
	}

	// Build the full account list: static keys first, then each table's
	// writable loads, then each table's readonly loads (Solana ordering).
	full := make([]Account, 0, nStatic)
	for i, k := range msg.StaticKeys {
		full = append(full, Account{
			Address:  k,
			Index:    i,
			Static:   true,
			Signer:   i < nSig,
			Writable: i < nSig-nRoSig || (i >= nSig && i < nStatic-nRoNonSig),
		})
	}

	// Resolve all referenced tables up front (caller-injected contents).
	resolved := make([][][32]byte, len(msg.Lookups))
	for li, lk := range msg.Lookups {
		if opts.ResolveTable == nil {
			return nil, fmt.Errorf("v0msg: message uses lookup tables but no TableResolver was provided")
		}
		addrs, err := opts.ResolveTable(lk.TableAddress)
		if err != nil {
			return nil, fmt.Errorf("v0msg: resolving lookup table %d: %w", li, err)
		}
		resolved[li] = addrs
	}

	load := func(li int, indexes []uint8, writable bool) error {
		addrs := resolved[li]
		for _, ti := range indexes {
			if int(ti) >= len(addrs) {
				return fmt.Errorf("%w: table %d index %d (table has %d addresses)",
					ErrTableIndexOutOfRange, li, ti, len(addrs))
			}
			full = append(full, Account{
				Address:  addrs[ti],
				Index:    len(full),
				Static:   false,
				Signer:   false,
				Writable: writable,
			})
		}
		return nil
	}
	for li, lk := range msg.Lookups {
		if err := load(li, lk.WritableIndexes, true); err != nil {
			return nil, err
		}
	}
	for li, lk := range msg.Lookups {
		if err := load(li, lk.ReadonlyIndexes, false); err != nil {
			return nil, err
		}
	}

	if len(full) > MaxAccounts {
		return nil, fmt.Errorf("%w: got %d", ErrTooManyAccounts, len(full))
	}

	// Duplicate check over the full list (catches an address that is both
	// static and table-loaded, or loaded twice).
	seen := make(map[[32]byte]int, len(full))
	for _, a := range full {
		if prev, ok := seen[a.Address]; ok {
			return nil, fmt.Errorf("%w: index %d duplicates index %d", ErrDuplicateAccount, a.Index, prev)
		}
		seen[a.Address] = a.Index
	}

	// Program id indexes must reference static accounts only; account
	// indexes may reference the full list.
	for ii, ix := range msg.Instructions {
		if int(ix.ProgramIDIndex) >= nStatic {
			return nil, fmt.Errorf("%w: instruction %d program index %d (only %d static accounts)",
				ErrProgramIndexNotStatic, ii, ix.ProgramIDIndex, nStatic)
		}
		for _, ai := range ix.AccountIndexes {
			if int(ai) >= len(full) {
				return nil, fmt.Errorf("%w: instruction %d references account %d of %d",
					ErrAccountIndexOutOfRange, ii, ai, len(full))
			}
		}
	}

	// Denylist check over the full list, including table-loaded accounts.
	report := &Report{Accounts: full}
	if len(opts.Denylist) > 0 {
		denied := make(map[[32]byte]struct{}, len(opts.Denylist))
		for _, d := range opts.Denylist {
			denied[d] = struct{}{}
		}
		for i := range full {
			if _, ok := denied[full[i].Address]; ok {
				full[i].Denied = true
				report.DeniedHits = append(report.DeniedHits, i)
			}
		}
	}
	if len(report.DeniedHits) > 0 {
		return report, fmt.Errorf("%w at account indexes %v", ErrDeniedAccount, report.DeniedHits)
	}
	return report, nil
}
