package update

import (
	"encoding/json"
	"errors"
)

func jsonMarshal(value any) ([]byte, error) {
	return json.Marshal(value)
}

func errorIs(err, target error) bool {
	return errors.Is(err, target)
}
