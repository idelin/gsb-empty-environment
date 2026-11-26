package credentialauth

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

type normalizedSnapshot struct {
	devices      []DeviceReceipt
	batches      []BatchSummary
	reservations []Reservation
	request      *PermitRequest

	snapshotDigest string
	requestDigest  string
}

type canonicalSnapshot struct {
	Devices      []canonicalReceipt     `json:"devices"`
	Batches      []canonicalBatch       `json:"batches"`
	Reservations []canonicalReservation `json:"reservations"`
}

type canonicalReceipt struct {
	ReceiptID         string          `json:"receiptID,omitempty"`
	DeviceID          string          `json:"deviceID"`
	BootGeneration    uint64          `json:"bootGeneration"`
	SignatureCounter  uint64          `json:"signatureCounter"`
	TransactionDigest string          `json:"transactionDigest,omitempty"`
	Credentials       []CredentialRef `json:"credentials,omitempty"`
	Signature         string          `json:"signature,omitempty"`
}

type canonicalBatch struct {
	BatchID              string          `json:"batchID"`
	RPID                 string          `json:"rpID,omitempty"`
	CandidateCredentials []CredentialRef `json:"candidateCredentials"`
	TransactionCount     int             `json:"transactionCount"`
	TransactionDigests   []string        `json:"transactionDigests,omitempty"`
	RequiredPurpose      string          `json:"requiredPurpose,omitempty"`
}

type canonicalReservation struct {
	RPID         string `json:"rpID"`
	CredentialID string `json:"credentialID"`
	DeviceID     string `json:"deviceID,omitempty"`
	Reason       string `json:"reason,omitempty"`
}

type canonicalRequest struct {
	RPID              string          `json:"rpID"`
	TransactionDigest string          `json:"transactionDigest,omitempty"`
	Purpose           string          `json:"purpose,omitempty"`
	Candidates        []CredentialRef `json:"candidates"`
	Mode              SafetyMode      `json:"mode"`
}

func normalizeSnapshot(snapshot Snapshot) (*normalizedSnapshot, error) {
	result := &normalizedSnapshot{}

	devices := make([]DeviceReceipt, 0, len(snapshot.Devices)+len(snapshot.Receipts))
	devices = append(devices, snapshot.Devices...)
	devices = append(devices, snapshot.Receipts...)
	for _, receipt := range devices {
		normalized, err := normalizeReceipt(receipt)
		if err != nil {
			return nil, err
		}
		result.devices = append(result.devices, normalized)
	}
	sort.SliceStable(result.devices, func(i, j int) bool {
		return receiptOrder(result.devices[i], result.devices[j])
	})
	uniqueDevices := result.devices[:0]
	seenReceipts := make(map[string]struct{}, len(result.devices))
	for _, receipt := range result.devices {
		identity := fmt.Sprintf("%s\x00%d\x00%d\x00%s\x00%s",
			receipt.DeviceID, receipt.BootGeneration, receipt.SignatureCounter,
			receipt.ReceiptID, receiptFingerprint(receipt))
		if _, ok := seenReceipts[identity]; ok {
			continue
		}
		seenReceipts[identity] = struct{}{}
		uniqueDevices = append(uniqueDevices, receipt)
	}
	result.devices = uniqueDevices
	if err := validateDeviceChains(result.devices); err != nil {
		return nil, err
	}

	batches := make([]BatchSummary, 0, len(snapshot.Batches)+len(snapshot.ConfirmedBatches))
	batches = append(batches, snapshot.Batches...)
	batches = append(batches, snapshot.ConfirmedBatches...)
	for index, batch := range batches {
		normalized, err := normalizeBatch(batch, index)
		if err != nil {
			return nil, err
		}
		result.batches = append(result.batches, normalized)
	}
	var err error
	result.batches, err = deduplicateBatches(result.batches)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(result.batches, func(i, j int) bool {
		return result.batches[i].batchID() < result.batches[j].batchID()
	})

	reservations := make([]Reservation, 0, len(snapshot.Reservations)+len(snapshot.Reserved))
	reservations = append(reservations, snapshot.Reservations...)
	reservations = append(reservations, snapshot.Reserved...)
	sourceRPByCredential := make(map[string][]string)
	for _, receipt := range result.devices {
		for _, ref := range receipt.Credentials {
			key, err := ref.key()
			if err == nil {
				sourceRPByCredential[key.id] = append(sourceRPByCredential[key.id], key.rpID)
			}
		}
	}
	seenReservation := make(map[credentialKey]struct{}, len(reservations))
	for _, reservation := range reservations {
		if strings.TrimSpace(reservation.RPID) == "" {
			id := strings.TrimSpace(reservation.CredentialID)
			if id == "" {
				id = strings.TrimSpace(reservation.ID)
			}
			rps := sortedUnique(sourceRPByCredential[id])
			if len(rps) != 1 {
				return nil, invalidf("cannot infer rpID for reserved credential %q; %d sources match", id, len(rps))
			}
			reservation.RPID = rps[0]
		}
		key, err := reservation.key()
		if err != nil {
			return nil, err
		}
		if _, ok := seenReservation[key]; ok {
			continue
		}
		seenReservation[key] = struct{}{}
		result.reservations = append(result.reservations, Reservation{
			RPID:         key.rpID,
			CredentialID: key.id,
			DeviceID:     strings.TrimSpace(reservation.DeviceID),
			Reason:       reservation.Reason,
		})
	}
	sort.SliceStable(result.reservations, func(i, j int) bool {
		left, _ := result.reservations[i].key()
		right, _ := result.reservations[j].key()
		return left.less(right)
	})

	request, err := selectRequest(snapshot.Request, snapshot.Pending)
	if err != nil {
		return nil, err
	}
	if request != nil {
		normalized, err := normalizeRequest(*request)
		if err != nil {
			return nil, err
		}
		result.request = &normalized
	}

	canonical := canonicalSnapshot{
		Devices:      make([]canonicalReceipt, 0, len(result.devices)),
		Batches:      make([]canonicalBatch, 0, len(result.batches)),
		Reservations: make([]canonicalReservation, 0, len(result.reservations)),
	}
	for _, receipt := range result.devices {
		canonical.Devices = append(canonical.Devices, canonicalizeReceipt(receipt))
	}
	for _, batch := range result.batches {
		canonical.Batches = append(canonical.Batches, canonicalizeBatch(batch))
	}
	for _, reservation := range result.reservations {
		key, _ := reservation.key()
		canonical.Reservations = append(canonical.Reservations, canonicalReservation{
			RPID:         key.rpID,
			CredentialID: key.id,
			DeviceID:     reservation.DeviceID,
			Reason:       reservation.Reason,
		})
	}
	result.snapshotDigest, err = digestJSON(canonical)
	if err != nil {
		return nil, fmt.Errorf("hash snapshot: %w", err)
	}
	if result.request != nil {
		result.requestDigest, err = digestJSON(canonicalizeRequest(*result.request))
		if err != nil {
			return nil, fmt.Errorf("hash request: %w", err)
		}
	}
	return result, nil
}

func normalizeReceipt(receipt DeviceReceipt) (DeviceReceipt, error) {
	receipt.DeviceID = strings.TrimSpace(receipt.DeviceID)
	if receipt.DeviceID == "" {
		return DeviceReceipt{}, invalidf("device receipt has an empty deviceID")
	}
	receipt.RPID = strings.TrimSpace(receipt.RPID)
	receipt.CredentialID = strings.TrimSpace(receipt.CredentialID)
	receipt.TransactionDigest = strings.TrimSpace(receipt.TransactionDigest)
	receipt.ReceiptID = strings.TrimSpace(receipt.ReceiptID)
	receipt.AllowedPurposes = sortedUnique(receipt.AllowedPurposes)

	refs := make([]CredentialRef, 0, len(receipt.Credentials)+1)
	if receipt.CredentialID != "" {
		refs = append(refs, CredentialRef{
			RPID:            receipt.RPID,
			CredentialID:    receipt.CredentialID,
			AllowedPurposes: append([]string(nil), receipt.AllowedPurposes...),
			SourceDeviceID:  receipt.DeviceID,
		})
	}
	for _, ref := range receipt.Credentials {
		ref.RPID = strings.TrimSpace(ref.RPID)
		if ref.RPID == "" {
			ref.RPID = receipt.RPID
		}
		ref.CredentialID = strings.TrimSpace(ref.CredentialIDValue())
		ref.ID = ""
		if len(ref.AllowedPurposes) == 0 && len(ref.Purposes) == 0 {
			ref.AllowedPurposes = append([]string(nil), receipt.AllowedPurposes...)
		} else {
			ref.AllowedPurposes = sortedUnique(append(append([]string(nil), ref.AllowedPurposes...), ref.Purposes...))
		}
		ref.Purposes = nil
		if ref.SourceDeviceID == "" {
			ref.SourceDeviceID = receipt.DeviceID
		}
		if _, err := ref.key(); err != nil {
			return DeviceReceipt{}, err
		}
		refs = append(refs, ref)
	}
	sort.SliceStable(refs, func(i, j int) bool {
		left, leftErr := refs[i].key()
		right, rightErr := refs[j].key()
		if leftErr != nil || rightErr != nil {
			return false
		}
		if left != right {
			return left.less(right)
		}
		return strings.Join(refs[i].AllowedPurposes, "\x00") < strings.Join(refs[j].AllowedPurposes, "\x00")
	})
	unique := refs[:0]
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		key, _ := ref.key()
		fingerprint := key.String() + "\x00" + strings.Join(ref.AllowedPurposes, "\x00")
		if _, ok := seen[fingerprint]; ok {
			continue
		}
		seen[fingerprint] = struct{}{}
		unique = append(unique, ref)
	}
	receipt.Credentials = unique
	return receipt, nil
}

func normalizeBatch(batch BatchSummary, index int) (BatchSummary, error) {
	id := strings.TrimSpace(batch.batchID())
	if id == "" {
		id = fmt.Sprintf("batch-%04d", index)
	}
	rpID := strings.TrimSpace(batch.RPID)
	count := batch.transactionCount()
	if count < 0 {
		return BatchSummary{}, invalidf("batch %q has a negative transaction count", id)
	}
	if batch.TransactionCount != 0 && batch.Count != 0 && batch.TransactionCount != batch.Count {
		return BatchSummary{}, invalidf("batch %q has conflicting transaction counts %d and %d", id, batch.TransactionCount, batch.Count)
	}
	digests := sortedUnique(batch.TransactionDigests)
	if len(digests) > 0 {
		if count == 0 {
			count = len(digests)
		}
		if count != len(digests) {
			return BatchSummary{}, invalidf("batch %q has %d transaction digests but count %d", id, len(digests), count)
		}
	}
	seen := make(map[credentialKey]struct{}, len(batch.candidates()))
	candidates := make([]CredentialRef, 0, len(batch.candidates()))
	for _, candidate := range batch.candidates() {
		candidate.RPID = strings.TrimSpace(candidate.RPID)
		if candidate.RPID == "" {
			candidate.RPID = rpID
		}
		candidate.CredentialID = strings.TrimSpace(candidate.CredentialIDValue())
		candidate.ID = ""
		if candidate.CredentialID == "" {
			return BatchSummary{}, invalidf("batch %q has an empty credential ID", id)
		}
		if rpID != "" && candidate.RPID != rpID {
			return BatchSummary{}, fmt.Errorf("%w: batch %q uses rpID %q but candidate uses %q", ErrRPIsolation, id, rpID, candidate.RPID)
		}
		key := credentialKey{rpID: candidate.RPID, id: candidate.CredentialID}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		candidate.AllowedPurposes = sortedUnique(append(append([]string(nil), candidate.AllowedPurposes...), candidate.Purposes...))
		candidate.Purposes = nil
		candidates = append(candidates, candidate)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		left := credentialKey{rpID: candidates[i].RPID, id: candidates[i].CredentialIDValue()}
		right := credentialKey{rpID: candidates[j].RPID, id: candidates[j].CredentialIDValue()}
		return left.less(right)
	})
	return BatchSummary{
		BatchID:              id,
		RPID:                 rpID,
		CandidateCredentials: candidates,
		TransactionCount:     count,
		TransactionDigests:   digests,
		RequiredPurpose:      strings.TrimSpace(batch.purpose()),
	}, nil
}

func deduplicateBatches(batches []BatchSummary) ([]BatchSummary, error) {
	seen := make(map[string]BatchSummary, len(batches))
	unique := batches[:0]
	for _, batch := range batches {
		id := batch.batchID()
		if previous, ok := seen[id]; ok {
			if !reflect.DeepEqual(canonicalizeBatch(previous), canonicalizeBatch(batch)) {
				return nil, fmt.Errorf("%w: batch ID %q has divergent summaries", ErrBatchConflict, id)
			}
			continue
		}
		seen[id] = batch
		unique = append(unique, batch)
	}
	return unique, nil
}

func selectRequest(primary, secondary *PermitRequest) (*PermitRequest, error) {
	if primary == nil {
		return secondary, nil
	}
	if secondary == nil {
		return primary, nil
	}
	left, err := normalizeRequest(*primary)
	if err != nil {
		return nil, err
	}
	right, err := normalizeRequest(*secondary)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(canonicalizeRequest(left), canonicalizeRequest(right)) {
		return nil, invalidf("both request and pending are present with different contents")
	}
	return &left, nil
}

func normalizeRequest(request PermitRequest) (PermitRequest, error) {
	request.RPID = strings.TrimSpace(request.RPID)
	request.TransactionDigest = strings.TrimSpace(request.TransactionDigest)
	request.Purpose = strings.TrimSpace(request.Purpose)
	request.RequiredPurpose = strings.TrimSpace(request.RequiredPurpose)
	request.CredentialID = strings.TrimSpace(request.CredentialID)
	request.CredentialIDs = sortedUnique(request.CredentialIDs)
	request.Mode = effectiveMode(request)

	refs := append([]CredentialRef(nil), request.Candidates...)
	for _, id := range request.CredentialIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			refs = append(refs, CredentialRef{RPID: request.RPID, CredentialID: id})
		}
	}
	if request.CredentialID != "" {
		refs = append(refs, CredentialRef{RPID: request.RPID, CredentialID: request.CredentialID})
	}
	seen := make(map[credentialKey]struct{}, len(refs))
	normalizedRefs := make([]CredentialRef, 0, len(refs))
	for _, ref := range refs {
		ref.RPID = strings.TrimSpace(ref.RPID)
		if ref.RPID == "" {
			ref.RPID = request.RPID
		}
		ref.CredentialID = strings.TrimSpace(ref.CredentialIDValue())
		ref.ID = ""
		ref.AllowedPurposes = sortedUnique(append(append([]string(nil), ref.AllowedPurposes...), ref.Purposes...))
		ref.Purposes = nil
		if ref.RPID == "" {
			return PermitRequest{}, invalidf("permit candidate has no rpID")
		}
		if ref.CredentialID == "" {
			return PermitRequest{}, invalidf("permit candidate has an empty credential ID")
		}
		if request.RPID != "" && ref.RPID != request.RPID {
			return PermitRequest{}, fmt.Errorf("%w: request rpID %q but candidate rpID %q", ErrRPIsolation, request.RPID, ref.RPID)
		}
		key := credentialKey{rpID: ref.RPID, id: ref.CredentialID}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		normalizedRefs = append(normalizedRefs, ref)
	}
	sort.SliceStable(normalizedRefs, func(i, j int) bool {
		left := credentialKey{rpID: normalizedRefs[i].RPID, id: normalizedRefs[i].CredentialIDValue()}
		right := credentialKey{rpID: normalizedRefs[j].RPID, id: normalizedRefs[j].CredentialIDValue()}
		return left.less(right)
	})
	request.Candidates = normalizedRefs
	if request.RPID == "" && len(normalizedRefs) > 0 {
		request.RPID = normalizedRefs[0].RPID
		for _, ref := range normalizedRefs[1:] {
			if ref.RPID != request.RPID {
				return PermitRequest{}, fmt.Errorf("%w: request candidates span multiple rpIDs", ErrRPIsolation)
			}
		}
	}
	if request.RPID == "" {
		return PermitRequest{}, invalidf("permit request has an empty rpID")
	}
	return request, nil
}

func validateDeviceChains(receipts []DeviceReceipt) error {
	byDevice := make(map[string][]DeviceReceipt)
	for _, receipt := range receipts {
		byDevice[receipt.DeviceID] = append(byDevice[receipt.DeviceID], receipt)
	}
	if len(byDevice) > MaxDevices {
		return fmt.Errorf("%w: got %d distinct devices, maximum is %d", ErrTooManyDevices, len(byDevice), MaxDevices)
	}
	for deviceID, group := range byDevice {
		sort.SliceStable(group, func(i, j int) bool {
			return receiptOrder(group[i], group[j])
		})
		var (
			maxCounter   uint64
			maxCounterTx string
			haveMax      bool
			lastGen      uint64
			lastCounter  uint64
			lastTx       string
			haveLast     bool
		)
		for _, receipt := range group {
			if haveLast && receipt.BootGeneration == lastGen &&
				receipt.SignatureCounter == lastCounter &&
				lastTx != "" && receipt.TransactionDigest != "" &&
				receipt.TransactionDigest != lastTx {
				return fmt.Errorf("%w: device %q generation %d has divergent transaction %q and %q",
					ErrDeviceFork, deviceID, receipt.BootGeneration, lastTx, receipt.TransactionDigest)
			}
			if haveLast && receipt.BootGeneration > lastGen {
				if receipt.SignatureCounter < maxCounter {
					return fmt.Errorf("%w: device %q generation %d counter %d is below prior counter %d",
						ErrCounterRollback, deviceID, receipt.BootGeneration, receipt.SignatureCounter, maxCounter)
				}
				if receipt.SignatureCounter == maxCounter &&
					maxCounterTx != "" && receipt.TransactionDigest != "" &&
					receipt.TransactionDigest != maxCounterTx {
					return fmt.Errorf("%w: device %q reused counter %d in generation %d with a different transaction",
						ErrCounterRollback, deviceID, receipt.SignatureCounter, receipt.BootGeneration)
				}
			}
			if !haveMax || receipt.SignatureCounter > maxCounter {
				maxCounter = receipt.SignatureCounter
				haveMax = true
				maxCounterTx = receipt.TransactionDigest
			} else if receipt.SignatureCounter == maxCounter && maxCounterTx == "" {
				maxCounterTx = receipt.TransactionDigest
			}
			lastGen = receipt.BootGeneration
			lastCounter = receipt.SignatureCounter
			lastTx = receipt.TransactionDigest
			haveLast = true
		}
	}
	return nil
}

func receiptOrder(left, right DeviceReceipt) bool {
	if left.DeviceID != right.DeviceID {
		return left.DeviceID < right.DeviceID
	}
	if left.BootGeneration != right.BootGeneration {
		return left.BootGeneration < right.BootGeneration
	}
	if left.SignatureCounter != right.SignatureCounter {
		return left.SignatureCounter < right.SignatureCounter
	}
	leftFingerprint := receiptFingerprint(left)
	rightFingerprint := receiptFingerprint(right)
	if leftFingerprint != rightFingerprint {
		return leftFingerprint < rightFingerprint
	}
	return left.ReceiptID < right.ReceiptID
}

func receiptFingerprint(receipt DeviceReceipt) string {
	return fmt.Sprintf("%s\x00%s\x00%s\x00%s", receipt.TransactionDigest,
		strings.Join(receipt.AllowedPurposes, "\x01"),
		credentialsFingerprint(receipt.Credentials), receipt.Signature)
}

func credentialsFingerprint(refs []CredentialRef) string {
	parts := make([]string, 0, len(refs))
	for _, ref := range refs {
		key, _ := ref.key()
		parts = append(parts, key.String()+"\x00"+strings.Join(ref.AllowedPurposes, "\x01"))
	}
	sort.Strings(parts)
	return strings.Join(parts, "\x02")
}

func canonicalizeReceipt(receipt DeviceReceipt) canonicalReceipt {
	refs := append([]CredentialRef(nil), receipt.Credentials...)
	sort.SliceStable(refs, func(i, j int) bool {
		left, _ := refs[i].key()
		right, _ := refs[j].key()
		if left != right {
			return left.less(right)
		}
		return strings.Join(refs[i].AllowedPurposes, "\x00") < strings.Join(refs[j].AllowedPurposes, "\x00")
	})
	return canonicalReceipt{
		ReceiptID:         receipt.ReceiptID,
		DeviceID:          receipt.DeviceID,
		BootGeneration:    receipt.BootGeneration,
		SignatureCounter:  receipt.SignatureCounter,
		TransactionDigest: receipt.TransactionDigest,
		Credentials:       refs,
		Signature:         receipt.Signature,
	}
}

func canonicalizeBatch(batch BatchSummary) canonicalBatch {
	refs := append([]CredentialRef(nil), batch.CandidateCredentials...)
	sort.SliceStable(refs, func(i, j int) bool {
		left, _ := refs[i].key()
		right, _ := refs[j].key()
		return left.less(right)
	})
	return canonicalBatch{
		BatchID:              batch.batchID(),
		RPID:                 batch.RPID,
		CandidateCredentials: refs,
		TransactionCount:     batch.transactionCount(),
		TransactionDigests:   append([]string(nil), batch.TransactionDigests...),
		RequiredPurpose:      batch.purpose(),
	}
}

func canonicalizeRequest(request PermitRequest) canonicalRequest {
	refs := append([]CredentialRef(nil), request.Candidates...)
	sort.SliceStable(refs, func(i, j int) bool {
		left, _ := refs[i].key()
		right, _ := refs[j].key()
		return left.less(right)
	})
	return canonicalRequest{
		RPID:              request.RPID,
		TransactionDigest: request.TransactionDigest,
		Purpose:           requestPurpose(request),
		Candidates:        refs,
		Mode:              effectiveMode(request),
	}
}
