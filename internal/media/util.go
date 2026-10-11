package media

import "encoding/json"

func jsonUnmarshal(s string, v any) error { return json.Unmarshal([]byte(s), v) }

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }
