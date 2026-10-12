package partscatalogselect

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
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
func canonicalEvidence(e evidence) ([]byte, error) {
	return jsjson.Encode(jsjson.Object{"ends": e.sorted(kindEnds), "exact": e.sorted(kindExact),
		"starts": e.sorted(kindStarts), "word": e.sorted(kindWord)})
}

func parseMembers(raw []byte, maximum int, allowed, required []string) (map[string]json.RawMessage, error) {
	if len(raw) == 0 || len(raw) > maximum || !utf8.Valid(raw) || !json.Valid(raw) || rejectDuplicateNames(raw) != nil {
		return nil, invalidInput()
	}
	var members map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&members); err != nil || members == nil || requireEOF(decoder) != nil {
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

func decodeString(raw json.RawMessage, destination *string) error {
	if len(raw) == 0 || validateJSONStringEscapes(raw) != nil {
		return invalidInput()
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

func rejectDuplicateNames(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := walkValue(decoder); err != nil {
		return err
	}
	return requireEOF(decoder)
}

func walkValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]struct{}{}
		for decoder.More() {
			nameToken, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := nameToken.(string)
			if !ok {
				return invalidInput()
			}
			if _, duplicate := seen[name]; duplicate {
				return invalidInput()
			}
			seen[name] = struct{}{}
			if err := walkValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return invalidInput()
		}
	case '[':
		for decoder.More() {
			if err := walkValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return invalidInput()
		}
	default:
		return invalidInput()
	}
	return nil
}

func requireEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return invalidInput()
	}
	return nil
}
