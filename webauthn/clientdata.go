package webauthn

import (
	"encoding/json"
	"fmt"
)

// parseClientJSON unmarshals the CollectedClientData subset relevant to
// origin policy. Standard fields such as type and challenge are ignored.
func parseClientJSON(raw []byte, data *ClientData) error {
	if err := json.Unmarshal(raw, data); err != nil {
		return fmt.Errorf("webauthn: parsing clientDataJSON: %w", err)
	}
	return nil
}
