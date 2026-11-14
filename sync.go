package netsec

import (
	"fmt"
	"sort"
)

// Sync returns only the requested entries that deviceID is authorized to
// read. Authorization is all-or-nothing: if any requested entry is outside
// the device's scope, no entries are returned. This avoids partial results
// and prevents an unauthorized caller from probing entry existence.
//
// SyncAll returns every entry currently authorized for the device.
func (s *Service) Sync(accountID AccountID, deviceID DeviceID, entryIDs []EntryID) ([]Entry, error) {
	s.ensureInitialized()
	s.mu.RLock()
	defer s.mu.RUnlock()

	acct, dev, err := s.authorizeLocked(accountID, deviceID)
	if err != nil {
		return nil, err
	}

	unique := make(map[EntryID]struct{}, len(entryIDs))
	for _, id := range entryIDs {
		if id == "" {
			return nil, fmt.Errorf("%w: empty entry id", ErrInvalidArgument)
		}
		unique[id] = struct{}{}
	}
	ordered := make([]EntryID, 0, len(unique))
	for id := range unique {
		ordered = append(ordered, id)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })

	if !dev.allEntries {
		for _, id := range ordered {
			if _, allowed := dev.scopes[id]; !allowed {
				return nil, fmt.Errorf("%w: %q", ErrScopeDenied, id)
			}
		}
	}

	out := make([]Entry, 0, len(ordered))
	for _, id := range ordered {
		record, exists := acct.entries[id]
		if !exists {
			return nil, fmt.Errorf("%w: %q", ErrEntryNotFound, id)
		}
		out = append(out, entryView(record))
	}
	return out, nil
}

// SyncAll returns all entries the device is authorized to read. A scoped
// device receives only its granted entries; the account owner receives all
// entries.
func (s *Service) SyncAll(accountID AccountID, deviceID DeviceID) ([]Entry, error) {
	s.ensureInitialized()
	s.mu.RLock()
	defer s.mu.RUnlock()

	acct, dev, err := s.authorizeLocked(accountID, deviceID)
	if err != nil {
		return nil, err
	}

	ids := make([]EntryID, 0, len(acct.entries))
	if dev.allEntries {
		for id := range acct.entries {
			ids = append(ids, id)
		}
	} else {
		for id := range dev.scopes {
			if _, exists := acct.entries[id]; exists {
				ids = append(ids, id)
			}
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	out := make([]Entry, 0, len(ids))
	for _, id := range ids {
		out = append(out, entryView(acct.entries[id]))
	}
	return out, nil
}

// GrantEntries adds entry IDs to an already approved device's scope. Only
// the account owner may expand another device's scope, so a compromised
// scoped device cannot promote itself or a replacement device beyond its
// own authority. Entries may be granted before they exist so a device can
// be provisioned ahead of data.
func (s *Service) GrantEntries(accountID AccountID, actorID, targetID DeviceID, entryIDs []EntryID) error {
	s.ensureInitialized()
	s.mu.Lock()
	defer s.mu.Unlock()

	acct, actor, err := s.authorizeLocked(accountID, actorID)
	if err != nil {
		return err
	}
	target, exists := acct.devices[targetID]
	if !exists {
		return fmt.Errorf("%w: %q", ErrDeviceNotFound, targetID)
	}
	if target.status == DeviceRevoked {
		return fmt.Errorf("%w: %q", ErrDeviceRevoked, targetID)
	}
	if target.status != DeviceApproved {
		return fmt.Errorf("%w: %q", ErrDeviceNotApproved, targetID)
	}
	if !actor.owner {
		return fmt.Errorf("%w: only the account owner may expand device scope", ErrApprovalRequired)
	}

	for _, id := range entryIDs {
		if id == "" {
			return fmt.Errorf("%w: empty entry scope", ErrInvalidArgument)
		}
	}
	if target.allEntries {
		return nil
	}
	if target.scopes == nil {
		target.scopes = make(map[EntryID]struct{}, len(entryIDs))
	}
	for _, id := range entryIDs {
		target.scopes[id] = struct{}{}
	}
	return nil
}

// RemoveEntries removes entry IDs from a device's scope. The account owner
// or the device itself may reduce scope. Removing scope takes effect for
// subsequent sync calls.
func (s *Service) RemoveEntries(accountID AccountID, actorID, targetID DeviceID, entryIDs []EntryID) error {
	s.ensureInitialized()
	s.mu.Lock()
	defer s.mu.Unlock()

	acct, actor, err := s.authorizeLocked(accountID, actorID)
	if err != nil {
		return err
	}
	target, exists := acct.devices[targetID]
	if !exists {
		return fmt.Errorf("%w: %q", ErrDeviceNotFound, targetID)
	}
	if target.status == DeviceRevoked {
		return fmt.Errorf("%w: %q", ErrDeviceRevoked, targetID)
	}
	if target.status != DeviceApproved {
		return fmt.Errorf("%w: %q", ErrDeviceNotApproved, targetID)
	}
	if actorID != targetID && !actor.owner {
		return fmt.Errorf("%w: only the account owner may reduce another device's scope", ErrApprovalRequired)
	}
	if target.allEntries {
		return fmt.Errorf("%w: cannot remove individual entries from all-entry scope", ErrInvalidArgument)
	}
	for _, id := range entryIDs {
		if id == "" {
			return fmt.Errorf("%w: empty entry scope", ErrInvalidArgument)
		}
	}
	for _, id := range entryIDs {
		delete(target.scopes, id)
	}
	return nil
}

func entryView(record *entryRecord) Entry {
	return Entry{
		ID:         record.id,
		AccountID:  record.accountID,
		Ciphertext: append([]byte(nil), record.ciphertext...),
		Metadata:   append([]byte(nil), record.metadata...),
		Version:    record.version,
		UpdatedAt:  record.updatedAt,
	}
}
