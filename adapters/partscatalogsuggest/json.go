package partscatalogsuggest

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"io"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/kurn-dev/kurn-search/internal/jsjson"
)

type sourceRecord struct {
	ID, Identifier, Manufacturer, Category, Status, TenantID, PartitionID string
	partition                                                             bool
}
type searchRequest struct {
	Query, PartitionID string
	partition          bool
	Limit              int
	filters            map[string][]string
}
type trustedScope struct{ TenantID string }

func boundedString(raw json.RawMessage, target *string) bool {
	return decodeString(raw, target) == nil && *target != "" && utf8.RuneCountInString(*target) <= 512
}

func parseSource(raw []byte) (sourceRecord, error) {
	members, err := parseMembers(raw, maxSourceBytes,
		[]string{"category", "id", "identifier", "manufacturer", "partition_id", "status", "tenant_id"},
		[]string{"category", "id", "identifier", "manufacturer", "status", "tenant_id"})
	if err != nil {
		return sourceRecord{}, invalidInput()
	}
	r := sourceRecord{}
	targets := map[string]*string{"id": &r.ID, "identifier": &r.Identifier, "manufacturer": &r.Manufacturer,
		"category": &r.Category, "status": &r.Status, "tenant_id": &r.TenantID, "partition_id": &r.PartitionID}
	for name, target := range targets {
		value, ok := members[name]
		if !ok {
			continue
		}
		if !boundedString(value, target) || strings.TrimSpace(*target) != *target {
			return sourceRecord{}, invalidInput()
		}
	}
	if len(r.ID) > 512 || (r.Status != "active" && r.Status != "discontinued") {
		return sourceRecord{}, invalidInput()
	}
	_, r.partition = members["partition_id"]
	return r, nil
}

func parseRequest(raw []byte) (searchRequest, error) {
	members, err := parseMembers(raw, maxRequestBytes, []string{"filters", "limit", "partition_id", "query"}, []string{"query"})
	if err != nil {
		return searchRequest{}, invalidInput()
	}
	r := searchRequest{Limit: 25}
	if decodeString(members["query"], &r.Query) != nil || whitespaceOnly(r.Query) || utf8.RuneCountInString(r.Query) > 256 {
		return searchRequest{}, invalidInput()
	}
	if value, ok := members["limit"]; ok {
		if decodeInteger(value, &r.Limit) != nil || r.Limit < 1 || r.Limit > 1000 {
			return searchRequest{}, invalidInput()
		}
	}
	if value, ok := members["partition_id"]; ok {
		if !boundedString(value, &r.PartitionID) {
			return searchRequest{}, invalidInput()
		}
		r.partition = true
	}
	if value, ok := members["filters"]; ok {
		fields, err := parseMembers(value, maxRequestBytes, []string{"category", "manufacturer", "status"}, nil)
		if err != nil {
			return searchRequest{}, invalidInput()
		}
		r.filters = make(map[string][]string, len(fields))
		for name, value := range fields {
			values, err := parseSelection(name, value)
			if err != nil {
				return searchRequest{}, err
			}
			r.filters[name] = values
		}
	}
	return r, nil
}

func parseScope(raw []byte) (trustedScope, error) {
	members, err := parseMembers(raw, maxTrustedScopeBytes, []string{"tenant_id"}, []string{"tenant_id"})
	s := trustedScope{}
	if err != nil || !boundedString(members["tenant_id"], &s.TenantID) {
		return trustedScope{}, invalidInput()
	}
	return s, nil
}

func matchesRequest(r sourceRecord, q searchRequest) bool {
	if q.partition && (!r.partition || r.PartitionID != q.PartitionID) {
		return false
	}
	for name, values := range q.filters {
		switch name {
		case "category":
			if !slices.Contains(values, r.Category) {
				return false
			}
		case "manufacturer":
			if !slices.Contains(values, r.Manufacturer) {
				return false
			}
		case "status":
			if !slices.Contains(values, r.Status) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func canonicalSource(r sourceRecord) ([]byte, error) {
	value := jsjson.Object{"id": r.ID, "identifier": r.Identifier, "manufacturer": r.Manufacturer,
		"category": r.Category, "status": r.Status, "tenant_id": r.TenantID}
	if r.partition {
		value["partition_id"] = r.PartitionID
	}
	return jsjson.Encode(value)
}

func canonicalResult(r sourceRecord, score int) ([]byte, error) {
	value := jsjson.Object{"id": r.ID, "identifier": r.Identifier, "manufacturer": r.Manufacturer,
		"category": r.Category, "status": r.Status, "tenant_id": r.TenantID, "score": score}
	if r.partition {
		value["partition_id"] = r.PartitionID
	}
	return jsjson.Encode(value)
}

func canonicalEvidence(score int, exact, transposition bool, echoedKey string) ([]byte, error) {
	exactValue := "no"
	if exact {
		exactValue = "yes"
	}
	value := jsjson.Object{"echoed_key": echoedKey, "normalized_exact": exactValue, "score": score}
	if transposition {
		// Present only on fallback hits, so similarity evidence keeps its
		// shape byte for byte.
		value["transposition"] = "yes"
	}
	return jsjson.Encode(value)
}

func parseMembers(raw []byte, maximum int, allowed, required []string) (map[string]json.RawMessage, error) {
	if len(raw) == 0 || len(raw) > maximum || !utf8.Valid(raw) || !json.Valid(raw) {
		return nil, invalidInput()
	}
	members, err := splitObject(raw)
	if err != nil {
		return nil, invalidInput()
	}
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		allowedSet[name] = struct{}{}
	}
	for name, value := range members {
		if _, ok := allowedSet[name]; !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, invalidInput()
		}
	}
	for _, name := range required {
		if _, ok := members[name]; !ok {
			return nil, invalidInput()
		}
	}
	return members, nil
}

// objectDecoders pools single-pass decoders for splitObject: it runs for every
// request, scope and candidate payload, and allocating a fresh decoder per
// call dominated searchd's allocation profile.
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
		return nil, invalidInput()
	}
	members := make(map[string]json.RawMessage)
	for p.decoder.PeekKind() != '}' {
		name, err := p.decoder.ReadToken()
		if err != nil || name.Kind() != '"' {
			return nil, invalidInput()
		}
		key := name.String()
		value, err := p.decoder.ReadValue()
		if err != nil {
			return nil, invalidInput()
		}
		members[key] = bytes.Clone(value)
	}
	if token, err := p.decoder.ReadToken(); err != nil || token.Kind() != '}' {
		return nil, invalidInput()
	}
	if _, err := p.decoder.ReadToken(); !errors.Is(err, io.EOF) {
		return nil, invalidInput()
	}
	if !float64Numbers(p, raw) {
		return nil, invalidInput()
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

func decodeString(raw json.RawMessage, destination *string) error {
	if len(raw) == 0 || validateJSONStringEscapes(raw) != nil {
		return invalidInput()
	}
	if value, ok := plainString(raw); ok {
		*destination = value
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(destination); err != nil || requireEOF(decoder) != nil || !utf8.ValidString(*destination) {
		return invalidInput()
	}
	return nil
}

func decodeInteger(raw json.RawMessage, destination *int) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || (trimmed[0] != '-' && (trimmed[0] < '0' || trimmed[0] > '9')) {
		return invalidInput()
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var number json.Number
	if err := decoder.Decode(&number); err != nil || requireEOF(decoder) != nil {
		return invalidInput()
	}
	value, err := number.Int64()
	if err != nil || value < -jsjson.MaxExactInteger || value > jsjson.MaxExactInteger || int64(int(value)) != value {
		return invalidInput()
	}
	*destination = int(value)
	return nil
}

func validateJSONStringEscapes(raw json.RawMessage) error {
	value := bytes.TrimSpace(raw)
	if len(value) < 2 || value[0] != '"' || value[len(value)-1] != '"' {
		return invalidInput()
	}
	for i := 1; i < len(value)-1; i++ {
		if value[i] != '\\' {
			continue
		}
		i++
		if i >= len(value)-1 {
			return invalidInput()
		}
		if value[i] != 'u' {
			continue
		}
		unit, ok := decodeHexUnit(value, i+1)
		if !ok {
			return invalidInput()
		}
		i += 4
		switch {
		case unit >= 0xd800 && unit <= 0xdbff:
			if i+6 >= len(value) || value[i+1] != '\\' || value[i+2] != 'u' {
				return invalidInput()
			}
			low, ok := decodeHexUnit(value, i+3)
			if !ok || low < 0xdc00 || low > 0xdfff {
				return invalidInput()
			}
			i += 6
		case unit >= 0xdc00 && unit <= 0xdfff:
			return invalidInput()
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

func requireEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return invalidInput()
	}
	return nil
}
