package catalogid

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"

	"github.com/kurn-dev/kurn-search/internal/jsjson"
)

type sourceRecord struct {
	ID          string
	Identifier  string
	TenantID    string
	PartitionID string
	partition   bool
}

type searchRequest struct {
	Query       string
	PartitionID string
	partition   bool
	Limit       int
}

type trustedScope struct{ TenantID string }

func parseSource(raw []byte) (sourceRecord, error) {
	members, err := parseMembers(raw, maxSourceBytes, []string{"id", "identifier", "partition_id", "tenant_id"}, []string{"id", "identifier", "tenant_id"})
	if err != nil {
		return sourceRecord{}, invalidInput()
	}
	record := sourceRecord{}
	if decodeString(members["id"], &record.ID) != nil || decodeString(members["identifier"], &record.Identifier) != nil ||
		decodeString(members["tenant_id"], &record.TenantID) != nil || record.ID == "" || record.Identifier == "" || record.TenantID == "" {
		return sourceRecord{}, invalidInput()
	}
	if rawPartition, ok := members["partition_id"]; ok {
		if decodeString(rawPartition, &record.PartitionID) != nil || record.PartitionID == "" {
			return sourceRecord{}, invalidInput()
		}
		record.partition = true
	}
	return record, nil
}

func parseRequest(raw []byte) (searchRequest, error) {
	members, err := parseMembers(raw, maxRequestBytes, []string{"limit", "partition_id", "query"}, []string{"query"})
	if err != nil {
		return searchRequest{}, invalidInput()
	}
	request := searchRequest{Limit: 25}
	if decodeString(members["query"], &request.Query) != nil || request.Query == "" {
		return searchRequest{}, invalidInput()
	}
	if rawPartition, ok := members["partition_id"]; ok {
		if decodeString(rawPartition, &request.PartitionID) != nil || request.PartitionID == "" {
			return searchRequest{}, invalidInput()
		}
		request.partition = true
	}
	if rawLimit, ok := members["limit"]; ok {
		if decodeInteger(rawLimit, &request.Limit) != nil || request.Limit < 1 || request.Limit > 1000 {
			return searchRequest{}, invalidInput()
		}
	}
	return request, nil
}

func parseScope(raw []byte) (trustedScope, error) {
	members, err := parseMembers(raw, maxTrustedScopeBytes, []string{"tenant_id"}, []string{"tenant_id"})
	if err != nil {
		return trustedScope{}, invalidInput()
	}
	scope := trustedScope{}
	if decodeString(members["tenant_id"], &scope.TenantID) != nil || scope.TenantID == "" {
		return trustedScope{}, invalidInput()
	}
	return scope, nil
}

func parseMembers(raw []byte, maximum int, allowed, required []string) (map[string]json.RawMessage, error) {
	if len(raw) == 0 || len(raw) > maximum || !utf8.Valid(raw) || !json.Valid(raw) || rejectDuplicateNames(raw) != nil {
		return nil, errors.New("invalid")
	}
	var members map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&members); err != nil || members == nil || requireEOF(decoder) != nil {
		return nil, errors.New("invalid")
	}
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		allowedSet[name] = struct{}{}
	}
	for name, value := range members {
		if _, ok := allowedSet[name]; !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, errors.New("invalid")
		}
	}
	for _, name := range required {
		if _, ok := members[name]; !ok {
			return nil, errors.New("invalid")
		}
	}
	return members, nil
}

func decodeString(raw json.RawMessage, destination *string) error {
	if len(raw) == 0 {
		return errors.New("invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(destination); err != nil || requireEOF(decoder) != nil || !utf8.ValidString(*destination) {
		return errors.New("invalid")
	}
	return nil
}

func decodeInteger(raw json.RawMessage, destination *int) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var number json.Number
	if err := decoder.Decode(&number); err != nil || requireEOF(decoder) != nil {
		return errors.New("invalid")
	}
	value, err := number.Int64()
	if err != nil || value < -jsjson.MaxExactInteger || value > jsjson.MaxExactInteger || int64(int(value)) != value {
		return errors.New("invalid")
	}
	*destination = int(value)
	return nil
}

func canonicalSource(record sourceRecord) ([]byte, error) {
	value := jsjson.Object{
		"id": record.ID, "identifier": record.Identifier, "tenant_id": record.TenantID,
	}
	if record.partition {
		value["partition_id"] = record.PartitionID
	}
	return jsjson.Encode(value)
}

func canonicalEvidence(e evidence) ([]byte, error) {
	return jsjson.Encode(jsjson.Object{
		"ends": e.sorted(kindEnds), "exact": e.sorted(kindExact),
		"starts": e.sorted(kindStarts), "word": e.sorted(kindWord),
	})
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
				return errors.New("invalid")
			}
			if _, exists := seen[name]; exists {
				return errors.New("invalid")
			}
			seen[name] = struct{}{}
			if err := walkValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return errors.New("invalid")
		}
	case '[':
		for decoder.More() {
			if err := walkValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return errors.New("invalid")
		}
	default:
		return errors.New("invalid")
	}
	return nil
}

func requireEOF(decoder *json.Decoder) error {
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("invalid")
		}
		return err
	}
	return nil
}
