package main

import (
	"encoding/json"
	"io"
	"strings"
	"sync"
)

func safeFrame(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, value := range x {
			key := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(k))
			if strings.Contains(key, "token") || strings.Contains(key, "cookie") || key == "sid" || key == "pub" || key == "authorization" || key == "password" || key == "secret" {
				continue
			}
			out[k] = safeFrame(value)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, v := range x {
			out[i] = safeFrame(v)
		}
		return out
	case string:
		runes := []rune(x)
		if len(runes) > 200 {
			return string(runes[:200]) + "…"
		}
		return x
	default:
		return v
	}
}

type output struct {
	mu      sync.Mutex
	encoder *json.Encoder
	err     error
}

func newOutput(w io.Writer) *output { return &output{encoder: json.NewEncoder(w)} }
func (o *output) write(v any) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.err == nil {
		o.err = o.encoder.Encode(v)
	}
}
