package im_api

import (
	"encoding/json"
	"errors"
	"io"
)

func jsonDecoder(reader io.Reader) *json.Decoder {
	decoder := json.NewDecoder(io.LimitReader(reader, 65*1024))
	decoder.DisallowUnknownFields()
	return decoder
}

func decodeJSONBody(reader io.Reader, target any) error {
	decoder := jsonDecoder(reader)
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}
