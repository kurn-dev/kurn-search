package openapi

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/kurn-dev/kurn-search/profile"
)

var errInvalid = errors.New("openapi: invalid canonical contract")

var admittedSchemaKeywords = map[string]struct{}{
	"$ref": {}, "additionalProperties": {}, "const": {}, "description": {}, "enum": {},
	"examples": {}, "items": {}, "maxItems": {}, "maxLength": {}, "maximum": {},
	"minItems": {}, "minLength": {}, "minimum": {}, "pattern": {}, "properties": {},
	"required": {}, "title": {}, "type": {},
}

func CanonicalJSON(value any) ([]byte, error) {
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return nil, errInvalid
	}
	return output.Bytes(), nil
}

func StrictDecode(raw []byte) (any, error) {
	if len(raw) == 0 || !utf8.Valid(raw) || validateSurrogateEscapes(raw) != nil {
		return nil, errInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	value, err := decodeStrictValue(decoder)
	if err != nil {
		return nil, errInvalid
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errInvalid
	}
	return value, nil
}

func decodeStrictValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return token, nil
	}
	switch delimiter {
	case '{':
		object := map[string]any{}
		for decoder.More() {
			nameToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			name, ok := nameToken.(string)
			if !ok {
				return nil, errInvalid
			}
			if _, duplicate := object[name]; duplicate {
				return nil, errInvalid
			}
			value, err := decodeStrictValue(decoder)
			if err != nil {
				return nil, err
			}
			object[name] = value
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return nil, errInvalid
		}
		return object, nil
	case '[':
		array := make([]any, 0)
		for decoder.More() {
			value, err := decodeStrictValue(decoder)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return nil, errInvalid
		}
		return array, nil
	default:
		return nil, errInvalid
	}
}

func ValidateDocument(raw []byte) error {
	value, err := StrictDecode(raw)
	if err != nil {
		return err
	}
	document, ok := value.(map[string]any)
	if !ok {
		return errInvalid
	}
	canonical, err := CanonicalJSON(document)
	if err != nil || !bytes.Equal(raw, canonical) {
		return fmt.Errorf("%w: noncanonical bytes", errInvalid)
	}
	if stringValue(document["openapi"]) != OpenAPIVersion || stringValue(document["jsonSchemaDialect"]) != DialectURI ||
		document["servers"] != nil || document["externalDocs"] != nil || countURLStrings(document) != 1 {
		return fmt.Errorf("%w: top-level identity", errInvalid)
	}
	info, ok := document["info"].(map[string]any)
	if !ok || stringValue(info["version"]) != InfoVersion {
		return errInvalid
	}
	operation, err := operationObject(document)
	if err != nil || stringValue(operation["operationId"]) != OperationID || !strings.Contains(stringValue(operation["description"]), precedence) {
		return fmt.Errorf("%w: operation identity", errInvalid)
	}
	if err := validateOperationShape(operation); err != nil {
		return fmt.Errorf("%w: operation shape", errInvalid)
	}
	schemas, err := componentSchemaMap(document)
	if err != nil {
		return err
	}
	for name, schemaValue := range schemas {
		schema, ok := schemaValue.(map[string]any)
		if !ok || validateSchemaShape(schema, schemas, map[string]bool{}) != nil {
			return fmt.Errorf("%w: schema %s", errInvalid, name)
		}
	}
	if err := validateParameterSchema(operation, schemas); err != nil || validateAllRefs(document, schemas) != nil || countKey(document, "pattern") != 2 {
		return fmt.Errorf("%w: parameter, reference, or pattern conformance", errInvalid)
	}
	contract, err := ServiceContract()
	if err != nil || validateStatuses(operation, schemas, contract) != nil || validateExactSchemas(operation, schemas) != nil {
		return fmt.Errorf("%w: status conformance", errInvalid)
	}
	return nil
}

func validateExactSchemas(operation map[string]any, schemas map[string]any) error {
	parameters, _ := operation["parameters"].([]any)
	parameter, _ := parameters[0].(map[string]any)
	routeSchema, _ := parameter["schema"].(map[string]any)
	if stringValue(routeSchema["pattern"]) != "^[a-z0-9]+(-[a-z0-9]+)*$" || integerValue(routeSchema["maxLength"]) != 64 {
		return errInvalid
	}
	request, _ := schemas["SearchRequest"].(map[string]any)
	requestProperties, _ := request["properties"].(map[string]any)
	query, _ := requestProperties["query"].(map[string]any)
	limit, _ := requestProperties["limit"].(map[string]any)
	partition, _ := requestProperties["partition_id"].(map[string]any)
	version, _ := requestProperties["adapter_version"].(map[string]any)
	if len(requestProperties) != 5 || !reflect.DeepEqual(request["required"], []any{"limit", "query"}) ||
		integerValue(query["minLength"]) != 1 || integerValue(query["maxLength"]) != 256 ||
		integerValue(limit["minimum"]) != 1 || integerValue(limit["maximum"]) != 1000 ||
		integerValue(partition["minLength"]) != 1 || partition["maxLength"] != nil ||
		integerValue(version["minLength"]) != 1 || version["maxLength"] != nil {
		return errInvalid
	}
	filters, _ := requestProperties["filters"].(map[string]any)
	filterProperties, _ := filters["properties"].(map[string]any)
	if len(filters) != 5 || stringValue(filters["type"]) != "object" || filters["additionalProperties"] != false ||
		!reflect.DeepEqual(filters["required"], []any{}) || len(filterProperties) != 3 {
		return errInvalid
	}
	for _, name := range []string{"category", "manufacturer", "status"} {
		field, _ := filterProperties[name].(map[string]any)
		maxItems := 16
		if name == "status" {
			maxItems = 2
		}
		if !boundedMembershipSchema(field) || integerValue(field["maxItems"]) != int64(maxItems) {
			return errInvalid
		}
	}
	success, _ := schemas["SearchSuccess"].(map[string]any)
	successProperties, _ := success["properties"].(map[string]any)
	hits, _ := successProperties["hits"].(map[string]any)
	lists, _ := successProperties["lists"].(map[string]any)
	complete, _ := successProperties["complete"].(map[string]any)
	if integerValue(hits["minItems"]) != 0 || integerValue(hits["maxItems"]) != 1000 ||
		integerValue(lists["minItems"]) != 1 || complete["const"] != true {
		return errInvalid
	}
	identity, _ := schemas["AdapterIdentity"].(map[string]any)
	identityProperties, _ := identity["properties"].(map[string]any)
	digest, _ := identityProperties["behavior_sha256"].(map[string]any)
	if stringValue(digest["pattern"]) != "^[0-9a-f]{64}$" {
		return errInvalid
	}
	return nil
}

func ValidateFixtures(raw []byte, documentRaw []byte, handler HandlerReport) error {
	value, err := StrictDecode(raw)
	if err != nil {
		return err
	}
	canonical, err := CanonicalJSON(value)
	if err != nil || !bytes.Equal(raw, canonical) {
		return errInvalid
	}
	var corpus FixtureCorpus
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&corpus) != nil || corpus.Schema != FixtureSchema || corpus.Claim != Claim || len(corpus.Fixtures) != corpus.Counts.Total {
		return fmt.Errorf("%w: fixture identity or total", errInvalid)
	}
	if corpus.Counts.StatusPairs != 28 || corpus.Counts.AdmittedErrors != 28 || corpus.Counts.AdmittedRequests != 9 ||
		corpus.Counts.AdmittedSuccess != 4 || corpus.Counts.HandlerCases != 11 || corpus.Counts.Mutations != 95 ||
		corpus.Counts.Mutations != corpus.Counts.Total-corpus.Counts.AdmittedErrors-corpus.Counts.AdmittedRequests-corpus.Counts.AdmittedSuccess-corpus.Counts.HandlerCases {
		return fmt.Errorf("%w: fixture class counts %+v", errInvalid, corpus.Counts)
	}
	if corpus.Counts.Total != 147 || corpus.Counts.SchemaProofs != 130 || corpus.Counts.SemanticProofs != 3 || corpus.Counts.HandlerProofs != 14 ||
		corpus.Counts.SchemaProofs+corpus.Counts.SemanticProofs+corpus.Counts.HandlerProofs != corpus.Counts.Total {
		return fmt.Errorf("%w: fixture proof counts %+v", errInvalid, corpus.Counts)
	}
	handlerChecks, err := handlerCheckMap(handler)
	if err != nil {
		return fmt.Errorf("%w: handler report", errInvalid)
	}
	seen := map[string]struct{}{}
	actual := FixtureCounts{StatusPairs: 28, Total: len(corpus.Fixtures)}
	for _, fixture := range corpus.Fixtures {
		if fixture.ID == "" || fixture.Class == "" || fixture.Expected == "" ||
			(fixture.Proof != "schema" && fixture.Proof != "semantic" && fixture.Proof != "handler") ||
			(fixture.Proof == "handler") != (fixture.HandlerCase != "") {
			return fmt.Errorf("%w: fixture fields %s", errInvalid, fixture.ID)
		}
		if _, duplicate := seen[fixture.ID]; duplicate {
			return errInvalid
		}
		seen[fixture.ID] = struct{}{}
		switch fixture.Proof {
		case "schema":
			actual.SchemaProofs++
		case "semantic":
			actual.SemanticProofs++
		case "handler":
			actual.HandlerProofs++
		}
		switch {
		case fixture.Class == "request" && fixture.Expected == "admit" && fixture.Proof == "schema":
			actual.AdmittedRequests++
			encoded, err := CanonicalJSON(fixture.Body)
			if err != nil || ValidateRequest(documentRaw, encoded) != nil {
				return fmt.Errorf("%w: admitted request %s", errInvalid, fixture.ID)
			}
		case fixture.Class == "success" && fixture.Expected == "admit" && fixture.Proof == "schema":
			actual.AdmittedSuccess++
			encoded, err := CanonicalJSON(fixture.Body)
			if err != nil || ValidateSuccess(documentRaw, encoded, fixture.Headers) != nil {
				return fmt.Errorf("%w: admitted success %s", errInvalid, fixture.ID)
			}
		case fixture.Class == "error" && fixture.Expected == "admit" && fixture.Proof == "schema":
			actual.AdmittedErrors++
			encoded, err := CanonicalJSON(fixture.Body)
			if err != nil || ValidateError(documentRaw, Envelope{Headers: fixture.Headers, Status: fixture.Status, Body: encoded}) != nil {
				return fmt.Errorf("%w: admitted error %s", errInvalid, fixture.ID)
			}
		case fixture.Class == "mutation" && fixture.Expected == "refuse":
			actual.Mutations++
			if validateMutationFixture(documentRaw, fixture, handlerChecks) != nil {
				return fmt.Errorf("%w: mutation %s", errInvalid, fixture.ID)
			}
		case fixture.Class == "handler":
			actual.HandlerCases++
			if validateHandlerFixture(fixture, handlerChecks) != nil {
				return fmt.Errorf("%w: handler %s", errInvalid, fixture.ID)
			}
		default:
			return fmt.Errorf("%w: fixture class %s", errInvalid, fixture.ID)
		}
	}
	if actual != corpus.Counts {
		return fmt.Errorf("%w: fixture recount got %+v want %+v", errInvalid, actual, corpus.Counts)
	}
	return nil
}

func handlerCheckMap(handler HandlerReport) (map[string]HandlerCheck, error) {
	if handler.Schema != "kurn-search-s6-handler/v1" || handler.Result != "PASS-S6-C" || len(handler.Checks) != 11 {
		return nil, errInvalid
	}
	checks := make(map[string]HandlerCheck, len(handler.Checks))
	for _, check := range handler.Checks {
		if check.ID == "" || check.Status < 200 {
			return nil, errInvalid
		}
		if _, duplicate := checks[check.ID]; duplicate {
			return nil, errInvalid
		}
		checks[check.ID] = check
	}
	return checks, nil
}

func validateHandlerFixture(fixture Fixture, checks map[string]HandlerCheck) error {
	check, ok := checks[fixture.HandlerCase]
	if !ok || check.Status != fixture.Status || check.Category != fixture.Category {
		return errInvalid
	}
	if (fixture.Expected == "admit") != (fixture.Status >= 200 && fixture.Status < 300) ||
		(fixture.Expected == "refuse") != (fixture.Status >= 400) {
		return errInvalid
	}
	return nil
}

func ValidateRequest(documentRaw, requestRaw []byte) error {
	if len(requestRaw) == 0 || len(requestRaw) > profile.HardMaxRequestBytes {
		return errInvalid
	}
	document, value, err := documentAndValue(documentRaw, requestRaw)
	if err != nil {
		return err
	}
	return evaluateNamed(document, "SearchRequest", value)
}

func validateMutationFixture(document []byte, fixture Fixture, handlerChecks map[string]HandlerCheck) error {
	var schemaErr, fullErr error
	if fixture.Target == "request" {
		var raw []byte
		var err error
		switch {
		case fixture.Body != nil:
			raw, err = CanonicalJSON(fixture.Body)
		case fixture.Wire != "":
			raw = []byte(fixture.Wire)
		case fixture.WireHex != "":
			raw, err = hex.DecodeString(fixture.WireHex)
		default:
			raw, err = syntheticRequestMutation(fixture)
		}
		if err != nil {
			return errInvalid
		}
		schemaErr = ValidateRequest(document, raw)
		fullErr = schemaErr
	}
	if fixture.Target == "success" {
		value, err := mutatedSuccess(fixture.ID)
		if err != nil {
			return err
		}
		raw, err := CanonicalJSON(value)
		if err != nil {
			return errInvalid
		}
		_, schemaErr = validateSuccessSchema(document, raw, successHeaders())
		fullErr = ValidateSuccess(document, raw, successHeaders())
	}
	if fixture.Target == "error" {
		envelope, err := mutatedError(fixture.ID)
		if err != nil {
			return errInvalid
		}
		_, schemaErr = validateErrorSchema(document, envelope)
		fullErr = ValidateError(document, envelope)
	}
	if fixture.Target != "request" && fixture.Target != "success" && fixture.Target != "error" {
		return errInvalid
	}
	switch fixture.Proof {
	case "schema":
		if schemaErr == nil {
			return errInvalid
		}
	case "semantic":
		if schemaErr != nil || fullErr == nil {
			return errInvalid
		}
	case "handler":
		if schemaErr != nil || validateHandlerFixture(fixture, handlerChecks) != nil {
			return errInvalid
		}
	default:
		return errInvalid
	}
	return nil
}

func syntheticRequestMutation(fixture Fixture) ([]byte, error) {
	request := map[string]any{"limit": 25, "query": "AX-104"}
	switch fixture.ID {
	case "request-adapter-version-empty":
		request["adapter_version"] = ""
	case "request-adapter-version-33-bytes":
		request["adapter_version"] = strings.Repeat("v", 33)
	case "request-body-over-limit":
		request["query"] = strings.Repeat("q", profile.HardMaxRequestBytes)
	case "request-partition-4097-bytes":
		request["partition_id"] = strings.Repeat("p", 4097)
	case "request-query-over-runes":
		request["query"] = strings.Repeat("q", 257)
	case "request-wrong-media-type":
	default:
		return nil, errInvalid
	}
	return CanonicalJSON(request)
}

func mutatedSuccess(id string) (map[string]any, error) {
	examples := successExamples()
	base, _ := exampleValue(examples["nonempty"]).(map[string]any)
	raw, _ := CanonicalJSON(base)
	decoded, err := StrictDecode(raw)
	if err != nil {
		return nil, errInvalid
	}
	result := decoded.(map[string]any)
	hits := result["hits"].([]any)
	hit := hits[0].(map[string]any)
	adapterIdentity := result["adapter"].(map[string]any)
	lists := result["lists"].([]any)
	list := lists[0].(map[string]any)
	timing := result["timing_us"].(map[string]any)
	switch id {
	case "success-adapter-missing":
		delete(result, "adapter")
	case "success-behavior-digest-invalid":
		adapterIdentity["behavior_sha256"] = "not-a-digest"
	case "success-complete-false":
		result["complete"] = false
	case "success-duplicate-entry-id":
		result["hits"] = append(hits, hit)
	case "success-entry-id-empty":
		hit["entry_id"] = ""
	case "success-hits-null":
		result["hits"] = nil
	case "success-hits-over-limit":
		many := make([]any, 1001)
		for index := range many {
			many[index] = map[string]any{"entry_id": fmt.Sprintf("entry-%d", index), "result": "neutral"}
		}
		result["hits"] = many
	case "success-lists-empty":
		result["lists"] = []any{}
	case "success-lists-out-of-order":
		result["lists"] = []any{
			map[string]any{"name": "z-list", "role": "z-role", "stamp": "z-stamp"},
			map[string]any{"name": "a-list", "role": "a-role", "stamp": "a-stamp"},
		}
	case "success-missing-hit-result":
		delete(hit, "result")
	case "success-missing-adapter-behavior":
		delete(adapterIdentity, "behavior_sha256")
	case "success-missing-adapter-name":
		delete(adapterIdentity, "name")
	case "success-missing-adapter-version":
		delete(adapterIdentity, "version")
	case "success-missing-complete":
		delete(result, "complete")
	case "success-missing-hit-entry-id":
		delete(hit, "entry_id")
	case "success-missing-hits":
		delete(result, "hits")
	case "success-missing-list-name":
		delete(list, "name")
	case "success-missing-list-role":
		delete(list, "role")
	case "success-missing-list-stamp":
		delete(list, "stamp")
	case "success-missing-lists":
		delete(result, "lists")
	case "success-missing-pool":
		delete(result, "pool")
	case "success-missing-pool-step":
		delete(result, "pool_step")
	case "success-missing-timing":
		delete(result, "timing_us")
	case "success-missing-timing-backend":
		delete(timing, "backend")
	case "success-missing-timing-residual":
		delete(timing, "residual")
	case "success-missing-timing-service":
		delete(timing, "service")
	case "success-missing-version-attempt":
		delete(result, "version_attempt")
	case "success-null-result":
		hit["result"] = nil
	case "success-non-array-hits":
		result["hits"] = "not-an-array"
	case "success-extra-adapter-member":
		adapterIdentity["extra"] = true
	case "success-extra-hit-member":
		hit["extra"] = true
	case "success-extra-list-member":
		list["extra"] = true
	case "success-extra-timing-member":
		timing["extra"] = true
	case "success-pool-step-zero":
		result["pool_step"] = 0
	case "success-pool-zero":
		result["pool"] = 0
	case "success-timing-negative":
		result["timing_us"].(map[string]any)["backend"] = -1
	case "success-undocumented-member":
		result["extra"] = true
	case "success-version-attempt-zero":
		result["version_attempt"] = 0
	default:
		return nil, errInvalid
	}
	return result, nil
}

func mutatedError(id string) (Envelope, error) {
	status := 429
	body := map[string]any{"category": "backend_rate_limited", "retry_after_seconds": 3}
	headers := map[string]string{
		"Cache-Control": "no-store", "Content-Type": MediaType,
		"X-Content-Type-Options": "nosniff", "Retry-After": "3",
	}
	switch id {
	case "error-category-status-mismatch":
		status = 400
		delete(body, "retry_after_seconds")
		delete(headers, "Retry-After")
	case "error-fractional-hint":
		body["retry_after_seconds"] = 1.5
		headers["Retry-After"] = "1.5"
	case "error-hint-header-body-mismatch":
		headers["Retry-After"] = "4"
	case "error-ineligible-hint":
		status = 400
		body["category"] = "invalid_request"
	case "error-raw-backend-prose", "error-undocumented-member":
		body["message"] = "opaque"
	case "error-six-second-hint":
		body["retry_after_seconds"] = 6
		headers["Retry-After"] = "6"
	case "error-zero-second-hint":
		body["retry_after_seconds"] = 0
		headers["Retry-After"] = "0"
	default:
		return Envelope{}, errInvalid
	}
	raw, err := CanonicalJSON(body)
	return Envelope{Body: raw, Headers: headers, Status: status}, err
}

func ValidateSuccess(documentRaw, responseRaw []byte, headers map[string]string) error {
	object, err := validateSuccessSchema(documentRaw, responseRaw, headers)
	if err != nil {
		return errInvalid
	}
	hits, _ := object["hits"].([]any)
	seen := map[string]struct{}{}
	for _, raw := range hits {
		hit, _ := raw.(map[string]any)
		entry := stringValue(hit["entry_id"])
		if _, duplicate := seen[entry]; duplicate {
			return errInvalid
		}
		seen[entry] = struct{}{}
	}
	lists, _ := object["lists"].([]any)
	previous := ""
	for _, raw := range lists {
		list, _ := raw.(map[string]any)
		key := stringValue(list["role"]) + "\x00" + stringValue(list["name"])
		if previous != "" && key <= previous {
			return errInvalid
		}
		previous = key
	}
	return nil
}

func validateSuccessSchema(documentRaw, responseRaw []byte, headers map[string]string) (map[string]any, error) {
	document, value, err := documentAndValue(documentRaw, responseRaw)
	ordinary := evaluateNamed(document, "SearchSuccess", value)
	faceted := evaluateNamed(document, "SearchFacetsSuccess", value)
	alternatives := evaluateNamed(document, "SearchFacetAlternativesSuccess", value)
	validShapes := 0
	for _, outcome := range []error{ordinary, faceted, alternatives} {
		if outcome == nil {
			validShapes++
		}
	}
	exactlyOne := validShapes == 1
	if err != nil || !exactlyOne || headers["Content-Type"] != MediaType || headers["Cache-Control"] != "no-store" {
		return nil, errInvalid
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, errInvalid
	}
	return object, nil
}

func ValidateError(documentRaw []byte, envelope Envelope) error {
	object, err := validateErrorSchema(documentRaw, envelope)
	if err != nil {
		return errInvalid
	}
	contract, err := ServiceContract()
	if err != nil || contract.Statuses[stringValue(object["category"])] != envelope.Status {
		return errInvalid
	}
	if envelope.Status == 401 && envelope.Headers["WWW-Authenticate"] != "Bearer" {
		return errInvalid
	}
	if envelope.Status == 405 && envelope.Headers["Allow"] != "POST" {
		return errInvalid
	}
	hintValue, bodyHasHint := object["retry_after_seconds"]
	headerHint, headerHasHint := envelope.Headers["Retry-After"]
	if bodyHasHint != headerHasHint {
		return errInvalid
	}
	if !bodyHasHint {
		return nil
	}
	if envelope.Status != 429 && envelope.Status != 503 {
		return errInvalid
	}
	hint, ok := exactInteger(hintValue)
	if !ok || hint < 1 || hint > 5 || strconv.FormatInt(hint, 10) != headerHint {
		return errInvalid
	}
	return nil
}

func validateErrorSchema(documentRaw []byte, envelope Envelope) (map[string]any, error) {
	document, value, err := documentAndValue(documentRaw, envelope.Body)
	if err != nil || evaluateNamed(document, errorSchemaName(envelope.Status), value) != nil ||
		envelope.Headers["Content-Type"] != MediaType || envelope.Headers["Cache-Control"] != "no-store" ||
		envelope.Headers["X-Content-Type-Options"] != "nosniff" {
		return nil, errInvalid
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, errInvalid
	}
	return object, nil
}

func documentAndValue(documentRaw, valueRaw []byte) (map[string]any, any, error) {
	if ValidateDocument(documentRaw) != nil {
		return nil, nil, errInvalid
	}
	documentValue, _ := StrictDecode(documentRaw)
	document, _ := documentValue.(map[string]any)
	value, err := StrictDecode(valueRaw)
	return document, value, err
}

func evaluateNamed(document map[string]any, name string, value any) error {
	schemas, err := componentSchemaMap(document)
	if err != nil {
		return err
	}
	schema, ok := schemas[name].(map[string]any)
	if !ok {
		return errInvalid
	}
	return evaluate(schema, schemas, value)
}

func evaluate(schema map[string]any, schemas map[string]any, value any) error {
	if ref := stringValue(schema["$ref"]); ref != "" {
		name := strings.TrimPrefix(ref, "#/components/schemas/")
		target, ok := schemas[name].(map[string]any)
		if !ok || name == ref {
			return errInvalid
		}
		return evaluate(target, schemas, value)
	}
	types := schemaTypes(schema["type"])
	if len(types) == 0 || !valueMatchesAnyType(value, types) {
		return errInvalid
	}
	if constant, exists := schema["const"]; exists && !equivalentJSON(constant, value) {
		return errInvalid
	}
	if enum, exists := schema["enum"].([]any); exists {
		found := false
		for _, candidate := range enum {
			found = found || equivalentJSON(candidate, value)
		}
		if !found {
			return errInvalid
		}
	}
	switch typed := value.(type) {
	case map[string]any:
		propertiesValue, constrained := schema["properties"]
		if !constrained {
			return nil
		}
		properties, _ := propertiesValue.(map[string]any)
		required, _ := schema["required"].([]any)
		for _, name := range required {
			if _, ok := typed[stringValue(name)]; !ok {
				return errInvalid
			}
		}
		for name, child := range typed {
			childSchema, ok := properties[name].(map[string]any)
			if !ok || evaluate(childSchema, schemas, child) != nil {
				return errInvalid
			}
		}
	case []any:
		if !withinCount(len(typed), schema["minItems"], schema["maxItems"]) {
			return errInvalid
		}
		itemsValue, constrained := schema["items"]
		if !constrained {
			return nil
		}
		itemSchema, _ := itemsValue.(map[string]any)
		for _, item := range typed {
			if evaluate(itemSchema, schemas, item) != nil {
				return errInvalid
			}
		}
	case string:
		if !withinCount(utf8.RuneCountInString(typed), schema["minLength"], schema["maxLength"]) {
			return errInvalid
		}
		if pattern := stringValue(schema["pattern"]); pattern != "" {
			compiled, err := regexp.Compile(pattern)
			if err != nil || !compiled.MatchString(typed) {
				return errInvalid
			}
		}
	default:
		if number, ok := numericValue(value); ok && !withinNumber(number, schema["minimum"], schema["maximum"]) {
			return errInvalid
		}
	}
	return nil
}

func validateSchemaShape(schema map[string]any, schemas map[string]any, stack map[string]bool) error {
	for keyword := range schema {
		if _, ok := admittedSchemaKeywords[keyword]; !ok {
			return errInvalid
		}
	}
	if ref := stringValue(schema["$ref"]); ref != "" {
		if len(schema) != 1 || !strings.HasPrefix(ref, "#/components/schemas/") {
			return errInvalid
		}
		name := strings.TrimPrefix(ref, "#/components/schemas/")
		if stack[name] {
			return errInvalid
		}
		target, ok := schemas[name].(map[string]any)
		if !ok {
			return errInvalid
		}
		stack[name] = true
		err := validateSchemaShape(target, schemas, stack)
		delete(stack, name)
		return err
	}
	types := schemaTypes(schema["type"])
	if len(types) == 0 || len(types) > 5 {
		return errInvalid
	}
	if len(types) == 2 {
		if !boundedMembershipSchema(schema) {
			return errInvalid
		}
	} else if len(types) > 1 {
		want := []string{"array", "boolean", "number", "object", "string"}
		sorted := append([]string(nil), types...)
		sort.Strings(sorted)
		if !reflect.DeepEqual(sorted, want) {
			return errInvalid
		}
	}
	if properties, exists := schema["properties"]; exists {
		object, ok := properties.(map[string]any)
		if !ok || schema["additionalProperties"] != false {
			return errInvalid
		}
		for _, child := range object {
			childSchema, ok := child.(map[string]any)
			if !ok || validateSchemaShape(childSchema, schemas, stack) != nil {
				return errInvalid
			}
		}
	}
	if items, exists := schema["items"]; exists {
		child, ok := items.(map[string]any)
		if !ok || validateSchemaShape(child, schemas, stack) != nil {
			return errInvalid
		}
	}
	if pattern := stringValue(schema["pattern"]); pattern != "" {
		if _, err := regexp.Compile(pattern); err != nil {
			return errInvalid
		}
	}
	return nil
}

// This is the only new union shape, not a general combinator evaluator.
func boundedMembershipSchema(schema map[string]any) bool {
	items, _ := schema["items"].(map[string]any)
	return len(schema) == 6 && reflect.DeepEqual(schema["type"], []any{"string", "array"}) &&
		integerValue(schema["minLength"]) == 1 && integerValue(schema["maxLength"]) == 512 &&
		integerValue(schema["minItems"]) == 1 && (integerValue(schema["maxItems"]) == 16 || integerValue(schema["maxItems"]) == 2) &&
		len(items) == 3 && stringValue(items["type"]) == "string" &&
		integerValue(items["minLength"]) == 1 && integerValue(items["maxLength"]) == 512
}

func validateOperationShape(operation map[string]any) error {
	security, ok := operation["security"].([]any)
	if !ok || len(security) != 1 {
		return errInvalid
	}
	parameters, ok := operation["parameters"].([]any)
	if !ok || len(parameters) != 1 {
		return errInvalid
	}
	parameter, _ := parameters[0].(map[string]any)
	if stringValue(parameter["name"]) != "adapter" || stringValue(parameter["in"]) != "path" || parameter["required"] != true {
		return errInvalid
	}
	body, _ := operation["requestBody"].(map[string]any)
	content, _ := body["content"].(map[string]any)
	if len(content) != 1 || content[MediaType] == nil {
		return errInvalid
	}
	return nil
}

func validateParameterSchema(operation map[string]any, schemas map[string]any) error {
	parameters, _ := operation["parameters"].([]any)
	parameter, _ := parameters[0].(map[string]any)
	schema, _ := parameter["schema"].(map[string]any)
	return validateSchemaShape(schema, schemas, map[string]bool{})
}

func validateStatuses(operation map[string]any, schemas map[string]any, contract Contract) error {
	responses, ok := operation["responses"].(map[string]any)
	groups := statusGroups(contract.Statuses)
	if !ok || len(responses) != len(groups)+1 || responses["200"] == nil {
		return errInvalid
	}
	for _, status := range sortedStatuses(groups) {
		if responses[strconv.Itoa(status)] == nil {
			return errInvalid
		}
		schema, ok := schemas[errorSchemaName(status)].(map[string]any)
		if !ok {
			return errInvalid
		}
		properties, _ := schema["properties"].(map[string]any)
		category, _ := properties["category"].(map[string]any)
		enum, _ := category["enum"].([]any)
		if !reflect.DeepEqual(enum, stringValues(groups[status])) {
			return errInvalid
		}
	}
	return nil
}

func validateAllRefs(value any, schemas map[string]any) error {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if key == "$ref" {
				ref := stringValue(child)
				name := strings.TrimPrefix(ref, "#/components/schemas/")
				if !strings.HasPrefix(ref, "#/components/schemas/") || schemas[name] == nil {
					return errInvalid
				}
			}
			if validateAllRefs(child, schemas) != nil {
				return errInvalid
			}
		}
	case []any:
		for _, child := range typed {
			if validateAllRefs(child, schemas) != nil {
				return errInvalid
			}
		}
	}
	return nil
}

func operationObject(document map[string]any) (map[string]any, error) {
	paths, ok := document["paths"].(map[string]any)
	if !ok || len(paths) != 1 {
		return nil, errInvalid
	}
	path, ok := paths[Route].(map[string]any)
	if !ok || len(path) != 1 {
		return nil, errInvalid
	}
	operation, ok := path["post"].(map[string]any)
	if !ok {
		return nil, errInvalid
	}
	return operation, nil
}

func componentSchemaMap(document map[string]any) (map[string]any, error) {
	components, ok := document["components"].(map[string]any)
	if !ok {
		return nil, errInvalid
	}
	schemas, ok := components["schemas"].(map[string]any)
	if !ok || len(schemas) == 0 {
		return nil, errInvalid
	}
	return schemas, nil
}

func schemaTypes(value any) []string {
	switch typed := value.(type) {
	case string:
		return []string{typed}
	case []any:
		result := make([]string, len(typed))
		for index, item := range typed {
			result[index] = stringValue(item)
			if result[index] == "" {
				return nil
			}
		}
		return result
	default:
		return nil
	}
}

func valueMatchesAnyType(value any, types []string) bool {
	for _, expected := range types {
		switch expected {
		case "object":
			_, ok := value.(map[string]any)
			if ok {
				return true
			}
		case "array":
			_, ok := value.([]any)
			if ok {
				return true
			}
		case "string":
			_, ok := value.(string)
			if ok {
				return true
			}
		case "boolean":
			_, ok := value.(bool)
			if ok {
				return true
			}
		case "number":
			_, ok := numericValue(value)
			if ok {
				return true
			}
		case "integer":
			_, ok := exactInteger(value)
			if ok {
				return true
			}
		}
	}
	return false
}

func exactInteger(value any) (int64, bool) {
	switch typed := value.(type) {
	case json.Number:
		result, err := typed.Int64()
		return result, err == nil
	case int:
		return int64(typed), true
	case int64:
		return typed, true
	case float64:
		return int64(typed), !math.IsNaN(typed) && !math.IsInf(typed, 0) && typed == math.Trunc(typed)
	default:
		return 0, false
	}
}

func numericValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case json.Number:
		result, err := typed.Float64()
		return result, err == nil && !math.IsInf(result, 0) && !math.IsNaN(result)
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case float64:
		return typed, !math.IsInf(typed, 0) && !math.IsNaN(typed)
	default:
		return 0, false
	}
}

func withinCount(value int, minimum, maximum any) bool {
	if min, ok := exactInteger(minimum); ok && int64(value) < min {
		return false
	}
	if max, ok := exactInteger(maximum); ok && int64(value) > max {
		return false
	}
	return true
}

func withinNumber(value float64, minimum, maximum any) bool {
	if min, ok := numericValue(minimum); ok && value < min {
		return false
	}
	if max, ok := numericValue(maximum); ok && value > max {
		return false
	}
	return true
}

func equivalentJSON(left, right any) bool {
	if l, ok := numericValue(left); ok {
		r, ok := numericValue(right)
		return ok && l == r
	}
	return reflect.DeepEqual(left, right)
}

func stringValue(value any) string {
	result, _ := value.(string)
	return result
}

func integerValue(value any) int64 {
	result, _ := exactInteger(value)
	return result
}

func validateSurrogateEscapes(raw []byte) error {
	inString := false
	for index := 0; index < len(raw); index++ {
		switch {
		case !inString && raw[index] == '"':
			inString = true
		case inString && raw[index] == '"':
			inString = false
		case inString && raw[index] == '\\':
			index++
			if index >= len(raw) {
				return errInvalid
			}
			if raw[index] != 'u' {
				continue
			}
			unit, ok := decodeHexUnit(raw, index+1)
			if !ok {
				return errInvalid
			}
			index += 4
			switch {
			case unit >= 0xd800 && unit <= 0xdbff:
				if index+6 >= len(raw) || raw[index+1] != '\\' || raw[index+2] != 'u' {
					return errInvalid
				}
				low, ok := decodeHexUnit(raw, index+3)
				if !ok || low < 0xdc00 || low > 0xdfff {
					return errInvalid
				}
				index += 6
			case unit >= 0xdc00 && unit <= 0xdfff:
				return errInvalid
			}
		}
	}
	if inString {
		return errInvalid
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

func countURLStrings(value any) int {
	count := 0
	walkStrings(value, func(input string) {
		if strings.Contains(input, "://") {
			if input != DialectURI {
				count = -1_000_000
				return
			}
			count++
		}
	})
	return count
}

func walkStrings(value any, visit func(string)) {
	switch typed := value.(type) {
	case string:
		visit(typed)
	case map[string]any:
		for key, child := range typed {
			visit(key)
			walkStrings(child, visit)
		}
	case []any:
		for _, child := range typed {
			walkStrings(child, visit)
		}
	}
}

func countKey(value any, target string) int {
	count := 0
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if key == target {
				count++
			}
			count += countKey(child, target)
		}
	case []any:
		for _, child := range typed {
			count += countKey(child, target)
		}
	}
	return count
}
