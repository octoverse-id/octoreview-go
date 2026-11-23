package octoreview

// The response half of the contract gate (server docs/spec.md section 18: "the response schema
// ref of every called operation"): the Go type each method decodes into declares exactly the
// properties of the schema the contract names for that operation's response.

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// responseTypes is the type each operation's `data` decodes into (an item type for lists).
var responseTypes = map[string]reflect.Type{
	"getHealthLive":          reflect.TypeOf(HealthLive{}),
	"getHealthReady":         reflect.TypeOf(HealthReady{}),
	"listEntities":           reflect.TypeOf(Entity{}),
	"upsertEntity":           reflect.TypeOf(Entity{}),
	"lookupEntity":           reflect.TypeOf(Entity{}),
	"getEntity":              reflect.TypeOf(Entity{}),
	"updateEntity":           reflect.TypeOf(Entity{}),
	"deactivateEntity":       reflect.TypeOf(Entity{}),
	"getEntityRating":        reflect.TypeOf(EntityRating{}),
	"queryRatings":           reflect.TypeOf(BatchRating{}),
	"getApplicationSettings": reflect.TypeOf(ApplicationSettings{}),
	"putApplicationSettings": reflect.TypeOf(ApplicationSettings{}),
	"listReviews":            reflect.TypeOf(Review{}),
	"createReview":           reflect.TypeOf(Review{}),
	"getReview":              reflect.TypeOf(Review{}),
	"updateReview":           reflect.TypeOf(Review{}),
	"removeReview":           reflect.TypeOf(Review{}),
	"publishReview":          reflect.TypeOf(Review{}),
	"rejectReview":           reflect.TypeOf(Review{}),
	"hideReview":             reflect.TypeOf(Review{}),
	"getReviewRevision":      reflect.TypeOf(Revision{}),
	"approveReviewRevision":  reflect.TypeOf(Review{}),
	"rejectReviewRevision":   reflect.TypeOf(Review{}),
	"listReviewMedia":        reflect.TypeOf(Media{}),
	"createMediaUpload":      reflect.TypeOf(MediaUpload{}),
	"completeMediaUpload":    reflect.TypeOf(Media{}),
	"deleteMedia":            reflect.TypeOf(Media{}),
	"approveMedia":           reflect.TypeOf(Media{}),
	"rejectMedia":            reflect.TypeOf(Media{}),
	"eraseAuthor":            reflect.TypeOf(EraseResult{}),
	"listAuditLogs":          reflect.TypeOf(AuditLog{}),
}

type schemaNode struct {
	Ref        string                `json:"$ref"`
	Type       any                   `json:"type"`
	Properties map[string]schemaNode `json:"properties"`
	Items      *schemaNode           `json:"items"`
	Content    map[string]struct {
		Schema *schemaNode `json:"schema"`
	} `json:"content"`
}

type responseDoc struct {
	Paths      map[string]map[string]any `json:"paths"`
	Components struct {
		Schemas   map[string]schemaNode `json:"schemas"`
		Responses map[string]schemaNode `json:"responses"`
	} `json:"components"`
}

// responseData resolves the schema an operation's success response carries under `data`: its
// name (a component, or "inline"), its property names, and whether `data` is a list of it.
func responseData(t *testing.T, doc responseDoc, raw map[string]any, opID string) (string, []string, bool) {
	t.Helper()
	component := func(ref string) schemaNode {
		return doc.Components.Schemas[ref[strings.LastIndex(ref, "/")+1:]]
	}
	for path, item := range doc.Paths {
		for method := range item {
			if method == "parameters" {
				continue
			}
			op := raw["paths"].(map[string]any)[path].(map[string]any)[method].(map[string]any)
			if op["operationId"] != opID {
				continue
			}
			codes := []string{}
			for code := range op["responses"].(map[string]any) {
				if strings.HasPrefix(code, "2") {
					codes = append(codes, code)
				}
			}
			sort.Strings(codes)
			if len(codes) == 0 {
				return "", nil, false
			}
			var response schemaNode
			remarshal(t, op["responses"].(map[string]any)[codes[0]], &response)
			if response.Ref != "" {
				response = doc.Components.Responses[response.Ref[strings.LastIndex(response.Ref, "/")+1:]]
			}
			body := response.Content["application/json"].Schema
			if body == nil {
				return "", nil, false
			}
			if body.Ref != "" {
				resolved := component(body.Ref)
				body = &resolved
			}
			data, ok := body.Properties["data"]
			if !ok {
				return "", nil, false
			}
			list := data.Type == "array"
			if list {
				data = *data.Items
			}
			name := "inline"
			if data.Ref != "" {
				name = data.Ref[strings.LastIndex(data.Ref, "/")+1:]
				data = component(data.Ref)
			}
			fields := make([]string, 0, len(data.Properties))
			for field := range data.Properties {
				fields = append(fields, field)
			}
			sort.Strings(fields)
			return name, fields, list
		}
	}
	t.Fatalf("%s is not in the vendored contract", opID)
	return "", nil, false
}

// jsonTags lists a struct's JSON field names as encoding/json sees them: an untagged embedded
// struct contributes its own fields (RatingSummary in EntityRating), while ResponseMeta and
// `json:"-"` fields are not part of the payload.
func jsonTags(typ reflect.Type) []string {
	var names []string
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		switch {
		case field.Type == reflect.TypeOf(ResponseMeta{}) || name == "-":
			continue
		case field.Anonymous && name == "" && field.Type.Kind() == reflect.Struct:
			names = append(names, jsonTags(field.Type)...)
			continue
		case name == "":
			name = field.Name
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func TestEveryResponseTypeMatchesItsSchema(t *testing.T) {
	doc := loadJSON[responseDoc](t, "openapi.json")
	raw := loadJSON[map[string]any](t, "openapi.json")
	for _, op := range operations {
		typ, ok := responseTypes[op.ID]
		if !ok {
			t.Errorf("%s has no response type in responseTypes", op.ID)
			continue
		}
		schema, fields, _ := responseData(t, doc, raw, op.ID)
		if len(fields) == 0 {
			t.Errorf("%s: the contract names no data properties", op.ID)
			continue
		}
		if got := jsonTags(typ); !reflect.DeepEqual(got, fields) {
			t.Errorf("%s decodes into %s, whose fields differ from the contract schema %s:\n got  %v\n want %v",
				op.ID, typ.Name(), schema, got, fields)
		}
	}
	for id := range responseTypes {
		if _, ok := operationByID(id); !ok {
			t.Errorf("responseTypes names %s, which is not an operation", id)
		}
	}
}

func operationByID(id string) (operation, bool) {
	for _, op := range operations {
		if op.ID == id {
			return op, true
		}
	}
	return operation{}, false
}

// remarshal converts a decoded JSON value into a typed one.
func remarshal(t *testing.T, value any, out any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err == nil {
		err = json.Unmarshal(raw, out)
	}
	if err != nil {
		t.Fatalf("remarshal: %v", err)
	}
}
