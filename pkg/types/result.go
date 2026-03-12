package types

type APIResult struct {
	OK      bool           `json:"ok"`
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Data    map[string]any `json:"data,omitempty"`
	TS      int64          `json:"ts"`
}
