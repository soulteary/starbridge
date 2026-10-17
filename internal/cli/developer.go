package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/soulteary/starbridge/internal/bridge"
	"github.com/soulteary/starbridge/internal/contract"
)

func developerRequest(values map[string]string) (map[string]any, error) {
	path, present := values["request"]
	if !present {
		return map[string]any{}, nil
	}
	if strings.TrimSpace(path) == "" {
		return nil, invalid("--request requires a nonempty JSON file path.")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, invalid("Developer request must be a regular JSON file.")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, invalid("Cannot open developer request file.")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, bridge.MaxDeveloperRequestBytes+1))
	if err != nil || len(data) > bridge.MaxDeveloperRequestBytes || !utf8.Valid(data) {
		return nil, invalid("Developer request file must contain at most 16000 bytes of UTF-8 JSON.")
	}
	if !contract.ValidJSONUnicode(data) {
		return nil, invalid("Developer request strings must contain valid Unicode without unpaired surrogate escapes.")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var params map[string]any
	if err := decoder.Decode(&params); err != nil || params == nil {
		return nil, invalid("Developer request file must contain a JSON parameters object.")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, invalid("Developer request file must contain exactly one JSON object.")
	}
	return params, nil
}
