package internal_api

import (
	"encoding/json"
	"io"
)

func decodeJSON(value string, target any) error {
	return json.Unmarshal([]byte(value), target)
}

// decodeJSONBody decodes exactly one JSON document from a size-limited body.
func decodeJSONBody(reader io.Reader, target any) error {
	decoder := json.NewDecoder(io.LimitReader(reader, 16*1024))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return err
	}
	return nil
}
