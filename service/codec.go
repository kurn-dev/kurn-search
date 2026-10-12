package service

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"io"
	"strconv"
	"sync"
	"unicode/utf8"

	"github.com/kurn-dev/kurn-search/internal/jsjson"
	"github.com/kurn-dev/kurn-search/profile"
)

type searchRequest struct {
	query          string
	partitionID    string
	hasPartition   bool
	adapterVersion string
	limit          int
	filters        json.RawMessage
}

func parseSearchRequest(raw []byte, bounds profile.Bounds) (searchRequest, error) {
	members, err := parseMembers(raw, bounds.MaxRequestBytes,
		[]string{"adapter_version", "filters", "limit", "partition_id", "query"}, []string{"limit", "query"})
	if err != nil {
		return searchRequest{}, err
	}
	result := searchRequest{}
	if value, present := members["filters"]; present {
		// Preserve presence and value syntax for adapter-owned validation. In
		// particular, the original catalog adapter must still refuse {}.
		result.filters = bytes.Clone(value)
	}
	if decodeString(members["query"], &result.query) != nil || result.query == "" ||
		utf8.RuneCountInString(result.query) > bounds.MaxQueryRunes ||
		decodeInteger(members["limit"], &result.limit) != nil || result.limit < 1 || result.limit > bounds.MaxResultLimit {
		return searchRequest{}, errInvalidRequest
	}
	if value, ok := members["partition_id"]; ok {
		if decodeString(value, &result.partitionID) != nil || result.partitionID == "" || len(result.partitionID) > 4096 {
			return searchRequest{}, errInvalidRequest
		}
		result.hasPartition = true
	}
	if value, ok := members["adapter_version"]; ok {
		if decodeString(value, &result.adapterVersion) != nil || result.adapterVersion == "" || len(result.adapterVersion) > 32 {
			return searchRequest{}, errInvalidRequest
		}
	}
	return result, nil
}

func (r searchRequest) adapterJSON() ([]byte, error) {
	value := jsjson.Object{"limit": r.limit, "query": r.query}
	if r.hasPartition {
		value["partition_id"] = r.partitionID
	}
	encoded, err := jsjson.Encode(value)
	if err != nil || r.filters == nil {
		return encoded, err
	}
	// The raw value has already passed parseMembers' JSON, UTF-8, null and
	// duplicate checks. Do not decode/coerce it or widen the shared encoder.
	// The other fields keep their existing canonical encoding byte for byte.
	var output bytes.Buffer
	output.Grow(len(encoded) + len(r.filters) + 16)
	output.WriteString("{\n  \"filters\": ")
	output.Write(r.filters)
	output.WriteString(",\n")
	output.Write(encoded[2:])
	return output.Bytes(), nil
}

func trustedScopeJSON(tenant string) ([]byte, error) {
	return jsjson.Encode(jsjson.Object{"tenant_id": tenant})
}

func parseMembers(raw []byte, maximum int, allowed, required []string) (map[string]json.RawMessage, error) {
	if len(raw) == 0 || len(raw) > maximum || !utf8.Valid(raw) || !json.Valid(raw) {
		return nil, errInvalidRequest
	}
	members, err := splitObject(raw)
	if err != nil {
		return nil, errInvalidRequest
	}
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		allowedSet[name] = struct{}{}
	}
	for name, value := range members {
		if _, ok := allowedSet[name]; !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, errInvalidRequest
		}
	}
	for _, name := range required {
		if _, ok := members[name]; !ok {
			return nil, errInvalidRequest
		}
	}
	return members, nil
}

func decodeString(raw json.RawMessage, destination *string) error {
	if len(raw) == 0 || validateJSONStringEscapes(raw) != nil {
		return errInvalidRequest
	}
	if value, ok := plainString(raw); ok {
		*destination = value
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(destination); err != nil || requireEOF(decoder) != nil || !utf8.ValidString(*destination) {
		return errInvalidRequest
	}
	return nil
}

func decodeInteger(raw json.RawMessage, destination *int) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || (trimmed[0] != '-' && (trimmed[0] < '0' || trimmed[0] > '9')) {
		return errInvalidRequest
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var number json.Number
	if err := decoder.Decode(&number); err != nil || requireEOF(decoder) != nil {
		return errInvalidRequest
	}
	value, err := number.Int64()
	if err != nil || value < -jsjson.MaxExactInteger || value > jsjson.MaxExactInteger || int64(int(value)) != value {
		return errInvalidRequest
	}
	*destination = int(value)
	return nil
}

func validateJSONStringEscapes(raw json.RawMessage) error {
	value := bytes.TrimSpace(raw)
	if len(value) < 2 || value[0] != '"' || value[len(value)-1] != '"' {
		return errInvalidRequest
	}
	for i := 1; i < len(value)-1; i++ {
		if value[i] != '\\' {
			continue
		}
		i++
		if i >= len(value)-1 {
			return errInvalidRequest
		}
		if value[i] != 'u' {
			continue
		}
		unit, ok := decodeHexUnit(value, i+1)
		if !ok {
			return errInvalidRequest
		}
		i += 4
		switch {
		case unit >= 0xd800 && unit <= 0xdbff:
			if i+6 >= len(value) || value[i+1] != '\\' || value[i+2] != 'u' {
				return errInvalidRequest
			}
			low, ok := decodeHexUnit(value, i+3)
			if !ok || low < 0xdc00 || low > 0xdfff {
				return errInvalidRequest
			}
			i += 6
		case unit >= 0xdc00 && unit <= 0xdfff:
			return errInvalidRequest
		}
	}
	return nil
}

func decodeHexUnit(value []byte, start int) (uint16, bool) {
	if start+4 > len(value) {
		return 0, false
	}
	var result uint16
	for _, digit := range value[start : start+4] {
		result <<= 4
		switch {
		case digit >= '0' && digit <= '9':
			result |= uint16(digit - '0')
		case digit >= 'a' && digit <= 'f':
			result |= uint16(digit-'a') + 10
		case digit >= 'A' && digit <= 'F':
			result |= uint16(digit-'A') + 10
		default:
			return 0, false
		}
	}
	return result, true
}

// objectDecoders pools single-pass decoders for splitObject, which runs for
// every search request; a fresh decoder per call showed in searchd's profile.
var objectDecoders = sync.Pool{New: func() any {
	p := new(pooledDecoder)
	p.decoder = jsontext.NewDecoder(&p.reader)
	return p
}}

type pooledDecoder struct {
	reader  bytes.Reader
	decoder *jsontext.Decoder
}

// pooledInputLimit bounds what the pool may retain: a decoder keeps buffer
// capacity sized to its largest input, so one that read a large input is
// dropped for the collector instead of being pooled.
const pooledInputLimit = 64 << 10

// release drops the decoder's reference to its input and pools it only if
// that input was small.
func (p *pooledDecoder) release(size int) {
	p.reader.Reset(nil)
	p.decoder.Reset(&p.reader)
	if size <= pooledInputLimit {
		objectDecoders.Put(p)
	}
}

// splitObject reads raw as exactly one JSON object and returns its members as
// exact raw values. Duplicate member names are refused at every depth, as the
// former token walk did. Invalid UTF-8 has already been refused by the caller;
// the options are encoding/json's own, so names unescape exactly as before and
// lone-surrogate escapes are left to the per-value string checks.
func splitObject(raw []byte) (map[string]json.RawMessage, error) {
	p := objectDecoders.Get().(*pooledDecoder)
	defer p.release(len(raw))
	p.reader.Reset(raw)
	p.decoder.Reset(&p.reader, jsontext.AllowDuplicateNames(false), jsontext.AllowInvalidUTF8(true))
	if token, err := p.decoder.ReadToken(); err != nil || token.Kind() != '{' {
		return nil, errInvalidRequest
	}
	members := make(map[string]json.RawMessage)
	for p.decoder.PeekKind() != '}' {
		name, err := p.decoder.ReadToken()
		if err != nil || name.Kind() != '"' {
			return nil, errInvalidRequest
		}
		key := name.String()
		value, err := p.decoder.ReadValue()
		if err != nil {
			return nil, errInvalidRequest
		}
		members[key] = bytes.Clone(value)
	}
	if token, err := p.decoder.ReadToken(); err != nil || token.Kind() != '}' {
		return nil, errInvalidRequest
	}
	if _, err := p.decoder.ReadToken(); !errors.Is(err, io.EOF) {
		return nil, errInvalidRequest
	}
	if !float64Numbers(p, raw) {
		return nil, errInvalidRequest
	}
	return members, nil
}

// float64Numbers reports whether every JSON number in raw parses as a
// float64. The former token walk decoded each number as a float64 and so
// refused out-of-range numbers at any depth; this keeps that refusal. Only a
// number with an exponent (an e or E directly after a digit) or with a run of
// more than 300 digits can overflow, so the scan ignores strings, member names
// and literals, and other inputs skip the second pass. raw is already valid
// JSON here.
func float64Numbers(p *pooledDecoder, raw []byte) bool {
	run, possible, inString, afterDigit := 0, false, false, false
	for i := 0; i < len(raw) && !possible; i++ {
		b := raw[i]
		if inString {
			switch b {
			case '\\':
				i++
			case '"':
				inString = false
			}
			continue
		}
		switch {
		case b == '"':
			inString, run, afterDigit = true, 0, false
		case b >= '0' && b <= '9':
			run, afterDigit = run+1, true
			possible = run > 300
		case (b == 'e' || b == 'E') && afterDigit:
			possible = true
		default:
			run, afterDigit = 0, false
		}
	}
	if !possible {
		return true
	}
	p.reader.Reset(raw)
	p.decoder.Reset(&p.reader, jsontext.AllowDuplicateNames(true), jsontext.AllowInvalidUTF8(true))
	for {
		token, err := p.decoder.ReadToken()
		if errors.Is(err, io.EOF) {
			return true
		}
		if err != nil {
			return false
		}
		if token.Kind() == '0' {
			if _, err := strconv.ParseFloat(token.String(), 64); err != nil {
				return false
			}
		}
	}
}

// plainString returns the content of a JSON string with no escapes and no
// control or quote bytes, which decodes to exactly its own bytes. Only JSON
// whitespace is trimmed; anything else is left to the general decoder.
func plainString(raw []byte) (string, bool) {
	start, end := 0, len(raw)
	for start < end && (raw[start] == ' ' || raw[start] == '\t' || raw[start] == '\r' || raw[start] == '\n') {
		start++
	}
	for end > start && (raw[end-1] == ' ' || raw[end-1] == '\t' || raw[end-1] == '\r' || raw[end-1] == '\n') {
		end--
	}
	if end-start < 2 || raw[start] != '"' || raw[end-1] != '"' {
		return "", false
	}
	body := raw[start+1 : end-1]
	for _, b := range body {
		if b == '\\' || b == '"' || b < 0x20 {
			return "", false
		}
	}
	if !utf8.Valid(body) {
		return "", false
	}
	return string(body), true
}

func requireEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errInvalidRequest
	}
	return nil
}
