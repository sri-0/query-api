package query

import (
	"encoding/base64"
	"encoding/json"
)

func encodeCursor(sort []any) string {
	if len(sort) == 0 {
		return ""
	}
	b, _ := json.Marshal(sort)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(s string) ([]any, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, badf("invalid cursor")
	}
	var out []any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, badf("invalid cursor")
	}
	return out, nil
}
