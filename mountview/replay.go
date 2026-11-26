package mountview

import "errors"

// Replay validates signatures and reconstructs the view across all concrete
// entries, signed checkpoints and signed missing windows in input order. Only
// the supplied in-memory records are consulted.
func (e *Engine) Replay(in *Input) error {
	if in == nil {
		return errors.New("mountview: nil input")
	}
	if in.Verify != nil {
		e.verifier = in.Verify
	}
	e.lastReplay = in
	if err := e.verifyManifests(in.Manifests); err != nil {
		return err
	}
	items, err := orderReplay(in)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return errors.New("mountview: no signed checkpoint to anchor recovery")
	}
	if items[0].kind != itemCheckpoint {
		return errors.New("mountview: recovery must begin with a signed genesis checkpoint")
	}
	for _, it := range items {
		switch it.kind {
		case itemCheckpoint:
			if err := e.applyCheckpoint(it.ckpt); err != nil {
				return err
			}
		case itemEntry:
			if err := e.applyConcrete(it.entry); err != nil {
				return err
			}
		case itemGap:
			e.applyGap(it.gap)
		}
	}
	return nil
}
