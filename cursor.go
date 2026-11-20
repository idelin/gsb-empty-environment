package newsindex

import (
	"encoding/base64"
	"encoding/json"
	"errors"
)

const cursorVersion = 1

type cursorPayload struct {
	Version        int         `json:"v"`
	Principal      PrincipalID `json:"p"`
	AuthGeneration uint64      `json:"a"`
	Snapshot       string      `json:"s"`
	After          MessageID   `json:"after"`
}

func encodeCursor(principal PrincipalID, generation uint64, snapshot string, after MessageID) string {
	payload := cursorPayload{
		Version:        cursorVersion,
		Principal:      principal,
		AuthGeneration: generation,
		Snapshot:       snapshot,
		After:          after,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(data)
}

func decodeCursor(token string) (cursorPayload, error) {
	var payload cursorPayload
	data, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return payload, err
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return cursorPayload{}, err
	}
	if payload.Version != cursorVersion || payload.Snapshot == "" || payload.After == "" {
		return cursorPayload{}, errors.New("incomplete cursor")
	}
	return payload, nil
}
