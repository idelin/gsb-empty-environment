package threshold

import (
	"crypto/rand"
	"fmt"
	"io"
	"math/big"
)

// DKGConfig configures either a DKG participant or the coordinator. The same
// participant list, threshold, and context must be used by all parties.
type DKGConfig struct {
	Group        Group
	Context      []byte
	Threshold    int
	Participants []Identity

	// Self is required by NewParticipant and ignored by NewCoordinator.
	Self ID

	// Random defaults to crypto/rand.Reader.
	Random io.Reader

	// Blacklist is optional; a coordinator creates one when omitted.
	Blacklist *Blacklist
}

type resolvedConfig struct {
	group      Group
	context    []byte
	threshold  int
	order      []ID
	identities map[ID]Identity
	random     io.Reader
	blacklist  *Blacklist
}

func resolveConfig(cfg DKGConfig, requireSelf bool) (*resolvedConfig, error) {
	if cfg.Group == nil {
		return nil, ErrGroupRequired
	}
	if cfg.Threshold < 1 || cfg.Threshold > len(cfg.Participants) {
		return nil, fmt.Errorf("%w: t=%d n=%d", ErrInvalidThreshold, cfg.Threshold, len(cfg.Participants))
	}
	if len(cfg.Participants) == 0 {
		return nil, fmt.Errorf("%w: empty participant set", ErrInvalidConfig)
	}

	r := cfg.Random
	if r == nil {
		r = rand.Reader
	}
	b := cfg.Blacklist
	if b == nil {
		b = NewBlacklist()
	}

	out := &resolvedConfig{
		group:      cfg.Group,
		context:    append([]byte(nil), cfg.Context...),
		threshold:  cfg.Threshold,
		order:      make([]ID, 0, len(cfg.Participants)),
		identities: make(map[ID]Identity, len(cfg.Participants)),
		random:     r,
		blacklist:  b,
	}

	orderMod := cfg.Group.Order()
	seenIndex := make(map[int]ID, len(cfg.Participants))
	for pos, ident := range cfg.Participants {
		if ident.ID == "" {
			return nil, fmt.Errorf("%w: participant at position %d has empty ID", ErrInvalidConfig, pos)
		}
		if _, exists := out.identities[ident.ID]; exists {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateParticipant, ident.ID)
		}
		if ident.Index == 0 {
			ident.Index = pos + 1
		}
		if ident.Index <= 0 || big.NewInt(int64(ident.Index)).Cmp(orderMod) >= 0 {
			return nil, fmt.Errorf("%w: id=%q index=%d", ErrInvalidParticipantIndex, ident.ID, ident.Index)
		}
		if prior, ok := seenIndex[ident.Index]; ok {
			return nil, fmt.Errorf("%w: index %d used by %q and %q", ErrDuplicateParticipant, ident.Index, prior, ident.ID)
		}
		seenIndex[ident.Index] = ident.ID
		out.identities[ident.ID] = ident
		out.order = append(out.order, ident.ID)
	}

	if requireSelf {
		if cfg.Self == "" {
			return nil, fmt.Errorf("%w: Self is required", ErrInvalidConfig)
		}
		if _, ok := out.identities[cfg.Self]; !ok {
			return nil, fmt.Errorf("%w: self %q", ErrUnknownParticipant, cfg.Self)
		}
	}
	return out, nil
}

func (c *resolvedConfig) identity(id ID) (Identity, error) {
	ident, ok := c.identities[id]
	if !ok {
		return Identity{}, fmt.Errorf("%w: %q", ErrUnknownParticipant, id)
	}
	return ident, nil
}
