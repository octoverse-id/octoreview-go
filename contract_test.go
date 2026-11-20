package octoreview

// The offline contract gate (server docs/spec.md section 18; `make contract`). It checks this
// package against the vendored testdata/contract/openapi.json and sdk-surface.json, refreshed
// with `make vendor-contract`.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
)

type openAPI struct {
	Paths      map[string]map[string]json.RawMessage `json:"paths"`
	Components struct {
		Schemas    map[string]json.RawMessage `json:"schemas"`
		Parameters map[string]json.RawMessage `json:"parameters"`
	} `json:"components"`
}

type surfaceFile struct {
	Operations []struct {
		Operation string   `json:"operation"`
		Go        string   `json:"go"`
		Retry     string   `json:"retry"`
		SDK       string   `json:"sdk"`
		Required  []string `json:"required"`
		Optional  []string `json:"optional"`
	} `json:"operations"`
	Helpers []struct {
		Name string `json:"name"`
		Go   string `json:"go"`
	} `json:"helpers"`
}

func loadJSON[T any](t *testing.T, name string) T {
	t.Helper()
	raw, err := os.ReadFile("testdata/contract/" + name)
	if err != nil {
		t.Fatalf("read the vendored contract (run make vendor-contract): %v", err)
	}
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	return out
}

type apiOperation struct {
	ID         string
	Params     map[string]bool // path and query parameters, and body properties
	HeaderList map[string]bool
}

// contractOperations indexes the vendored OpenAPI document by "METHOD path".
func contractOperations(t *testing.T) map[string]apiOperation {
	t.Helper()
	doc := loadJSON[openAPI](t, "openapi.json")
	resolve := func(raw json.RawMessage, section map[string]json.RawMessage) map[string]any {
		var node map[string]any
		_ = json.Unmarshal(raw, &node)
		if ref, ok := node["$ref"].(string); ok {
			_ = json.Unmarshal(section[ref[strings.LastIndex(ref, "/")+1:]], &node)
		}
		return node
	}
	out := map[string]apiOperation{}
	for path, item := range doc.Paths {
		var shared []json.RawMessage
		if raw, ok := item["parameters"]; ok {
			_ = json.Unmarshal(raw, &shared)
		}
		for method, raw := range item {
			if method == "parameters" {
				continue
			}
			var op struct {
				OperationID string            `json:"operationId"`
				Parameters  []json.RawMessage `json:"parameters"`
				RequestBody *struct {
					Content map[string]struct {
						Schema json.RawMessage `json:"schema"`
					} `json:"content"`
				} `json:"requestBody"`
			}
			if err := json.Unmarshal(raw, &op); err != nil {
				t.Fatalf("decode %s %s: %v", method, path, err)
			}
			entry := apiOperation{ID: op.OperationID, Params: map[string]bool{}, HeaderList: map[string]bool{}}
			for _, p := range append(slices.Clone(shared), op.Parameters...) {
				node := resolve(p, doc.Components.Parameters)
				name, _ := node["name"].(string)
				if node["in"] == "header" {
					entry.HeaderList[strings.ToLower(name)] = true
				} else {
					entry.Params[name] = true
				}
			}
			if op.RequestBody != nil {
				if media, ok := op.RequestBody.Content["application/json"]; ok {
					schema := resolve(media.Schema, doc.Components.Schemas)
					props, _ := schema["properties"].(map[string]any)
					for name := range props {
						entry.Params[name] = true
					}
				}
			}
			out[strings.ToUpper(method)+" "+path] = entry
		}
	}
	return out
}

func TestEveryOperationMatchesTheContract(t *testing.T) {
	api := contractOperations(t)
	covered := map[string]bool{}
	for _, op := range operations {
		entry, ok := api[op.Method+" "+op.Path]
		if !ok {
			t.Errorf("%s: %s %s is not in openapi.json", op.ID, op.Method, op.Path)
			continue
		}
		if entry.ID != op.ID {
			t.Errorf("%s %s is %s in openapi.json, %s here", op.Method, op.Path, entry.ID, op.ID)
		}
		covered[op.ID] = true
		for _, name := range op.Params {
			if name != "include_global" && !entry.Params[name] {
				t.Errorf("%s sends %s, which openapi.json does not declare", op.ID, name)
			}
		}
	}
	surface := loadJSON[surfaceFile](t, "sdk-surface.json")
	for _, s := range surface.Operations {
		if s.SDK == "none" {
			continue
		}
		if !covered[s.Operation] {
			t.Errorf("the surface lists %s, which this SDK does not implement", s.Operation)
		}
	}
}

func TestEveryOperationMatchesTheSurface(t *testing.T) {
	surface := loadJSON[surfaceFile](t, "sdk-surface.json")
	byID := map[string]operation{}
	for _, op := range operations {
		byID[op.ID] = op
	}
	listed := map[string]bool{}
	for _, s := range surface.Operations {
		if s.SDK == "none" {
			continue
		}
		listed[s.Operation] = true
		op, ok := byID[s.Operation]
		if !ok {
			continue // reported by TestEveryOperationMatchesTheContract
		}
		if op.GoMethod != s.Go {
			t.Errorf("%s: the surface names %s, this SDK %s", s.Operation, s.Go, op.GoMethod)
		}
		if string(op.Retry) != s.Retry {
			t.Errorf("%s: retry %s in the surface, %s here", s.Operation, s.Retry, op.Retry)
		}
		want := append(slices.Clone(s.Required), s.Optional...)
		got := slices.Clone(op.Params)
		sort.Strings(want)
		sort.Strings(got)
		if !slices.Equal(want, got) {
			t.Errorf("%s: parameters\n surface %v\n SDK     %v", s.Operation, want, got)
		}
	}
	for id := range byID {
		if !listed[id] {
			t.Errorf("%s is implemented but missing from the surface", id)
		}
	}
}

// pendingHelpers are surface helpers the next PR adds (uploads and webhooks, issue #4).
var pendingHelpers = map[string]bool{
	"uploadFile": true, "uploadBytes": true, "uploadStream": true, "verifyWebhook": true,
}

func TestEverySurfaceMethodExists(t *testing.T) {
	client, _ := New("https://reviews.example.com", "ocr_test", "t")
	surface := loadJSON[surfaceFile](t, "sdk-surface.json")
	names := map[string]string{}
	for _, s := range surface.Operations {
		if s.SDK != "none" {
			names[s.Operation] = s.Go
		}
	}
	for _, h := range surface.Helpers {
		if !pendingHelpers[h.Name] {
			names[h.Name] = h.Go
		}
	}
	value := reflect.ValueOf(client).Elem()
	for name, goMethod := range names {
		resource, method, _ := strings.Cut(goMethod, ".")
		service := value.FieldByName(resource)
		if !service.IsValid() || !service.MethodByName(method).IsValid() {
			t.Errorf("%s: Client.%s does not exist", name, goMethod)
		}
	}
}

func TestErrorCodesMatchTheContract(t *testing.T) {
	doc := loadJSON[openAPI](t, "openapi.json")
	var schema struct {
		Enum []string `json:"enum"`
	}
	if err := json.Unmarshal(doc.Components.Schemas["ErrorCode"], &schema); err != nil {
		t.Fatal(err)
	}
	want := slices.Clone(schema.Enum)
	got := slices.Clone(errorCodes)
	sort.Strings(want)
	sort.Strings(got)
	if !slices.Equal(want, got) {
		t.Errorf("error code constants differ from the contract\n contract %v\n SDK      %v", want, got)
	}
}

// --- every operation through a recording transport -------------------------------------------

type recorded struct {
	method, path string
	header       http.Header
	query        url.Values
	body         map[string]any
}

type recorder struct {
	calls []recorded
}

func (r *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	call := recorded{method: req.Method, path: req.URL.EscapedPath(), header: req.Header.Clone(), query: req.URL.Query()}
	if req.Body != nil {
		raw, _ := io.ReadAll(req.Body)
		_ = json.Unmarshal(raw, &call.body)
	}
	r.calls = append(r.calls, call)
	data := `{}`
	switch {
	case strings.HasSuffix(call.path, "/ratings/query"), call.method == "GET" && strings.HasSuffix(call.path, "/media"):
		data = `[]`
	case call.method == "GET" && (strings.HasSuffix(call.path, "/entities") || strings.HasSuffix(call.path, "/reviews") || strings.HasSuffix(call.path, "/audit-logs")):
		return response(200, `{"data": [], "pagination": {"limit": 20, "offset": 0, "count": 0}}`), nil
	}
	return response(200, `{"data": `+data+`}`), nil
}

func response(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": {"application/json"}, "X-Request-Id": {"req-1"}, "Etag": {`"etag-1"`}},
		Body:       io.NopCloser(bytes.NewBufferString(body)),
	}
}

func ptr[T any](v T) *T { return &v }

// Distinguishable values: every argument is unique, so a swapped field shows up.
func everyCall(ctx context.Context, c *Client) map[string]func() error {
	return map[string]func() error{
		"getHealthLive":  func() error { _, err := c.Health.Live(ctx); return err },
		"getHealthReady": func() error { _, err := c.Health.Ready(ctx); return err },
		"listEntities": func() error {
			_, err := c.Entities.List(ctx, ListEntitiesParams{EntityType: "entity_type-1", ExternalID: "external_id-1", IsActive: ptr(true), Page: Page{Limit: 7, Offset: 3}}, IncludeGlobal())
			return err
		},
		"upsertEntity": func() error {
			_, err := c.Entities.Upsert(ctx, UpsertEntityParams{EntityType: "entity_type-2", ExternalID: "external_id-2", Metadata: map[string]any{"m": 1}, MaxImages: Value[int64](4), MaxVideos: Null[int64](), MaxImageBytes: Value[int64](5), MaxVideoBytes: Value[int64](6)})
			return err
		},
		"lookupEntity": func() error {
			_, err := c.Entities.Lookup(ctx, "entity_type-3", "external_id-3", IncludeGlobal())
			return err
		},
		"getEntity": func() error { _, err := c.Entities.Get(ctx, "entity_id-4", IncludeGlobal()); return err },
		"updateEntity": func() error {
			_, err := c.Entities.Update(ctx, "entity_id-5", UpdateEntityParams{Metadata: map[string]any{"m": 2}, MaxImages: Value[int64](1), MaxVideos: Value[int64](2), MaxImageBytes: Value[int64](3), MaxVideoBytes: Null[int64](), IsActive: ptr(false)})
			return err
		},
		"deactivateEntity": func() error { _, err := c.Entities.Deactivate(ctx, "entity_id-6"); return err },
		"getEntityRating":  func() error { _, err := c.Ratings.ForEntity(ctx, "entity_id-7", IncludeGlobal()); return err },
		"queryRatings": func() error {
			_, err := c.Ratings.Query(ctx, QueryRatingsParams{EntityType: "entity_type-8", ExternalIDs: []string{"a", "b"}}, IncludeGlobal())
			return err
		},
		"getApplicationSettings": func() error { _, err := c.Settings.Get(ctx, "application_id-9"); return err },
		"putApplicationSettings": func() error {
			_, err := c.Settings.Put(ctx, "application_id-10", PutSettingsParams{MaxImages: ptr[int64](1), AllowedContentTypes: &[]string{"image/png"}, ModerationMode: ptr(ModerationPreModeration)})
			return err
		},
		"listReviews": func() error {
			_, err := c.Reviews.List(ctx, ListReviewsParams{EntityID: "entity_id-11", EntityType: "entity_type-11", ExternalID: "external_id-11", AuthorID: "author_id-11", IncludeUnpublished: ptr(true), Status: "pending", Rating: ptr(4), HasMedia: ptr(true), IsVerifiedPurchase: ptr(false), HasPendingRevision: ptr(true), MediaModerationStatus: "pending", Sort: "oldest", Page: Page{Limit: 5, Offset: 10}}, IncludeGlobal())
			return err
		},
		"createReview": func() error {
			_, err := c.Reviews.Create(ctx, CreateReviewParams{EntityID: "entity_id-12", EntityType: "entity_type-12", ExternalID: "external_id-12", AuthorID: "author_id-12", Rating: 5, Title: ptr("title-12"), Body: ptr("body-12"), Metadata: map[string]any{"m": 12}, AuthorMetadata: map[string]any{"a": 12}, SourceRef: "source_ref-12", IsVerifiedPurchase: ptr(true)})
			return err
		},
		"getReview": func() error {
			_, err := c.Reviews.Get(ctx, "review_id-13", GetReviewParams{AuthorID: "author_id-13", IncludeUnpublished: ptr(true)}, IncludeGlobal())
			return err
		},
		"updateReview": func() error {
			_, err := c.Reviews.Update(ctx, "review_id-14", UpdateReviewParams{Rating: ptr(3), Title: Value("title-14"), Body: Null[string](), Metadata: map[string]any{"m": 14}, AuthorMetadata: map[string]any{"a": 14}, IsVerifiedPurchase: ptr(true)})
			return err
		},
		"removeReview": func() error { _, err := c.Reviews.Remove(ctx, "review_id-15"); return err },
		"publishReview": func() error {
			_, err := c.Reviews.Publish(ctx, "review_id-16", PublishParams{ExpectedVersion: 16, ExpectedMediaVersion: 161, Reason: ptr("reason-16")})
			return err
		},
		"rejectReview": func() error {
			_, err := c.Reviews.Reject(ctx, "review_id-17", ModerationParams{Reason: ptr("reason-17")})
			return err
		},
		"hideReview": func() error {
			_, err := c.Reviews.Hide(ctx, "review_id-18", ModerationParams{Reason: ptr("reason-18")})
			return err
		},
		"getReviewRevision": func() error { _, err := c.Reviews.GetRevision(ctx, "review_id-19"); return err },
		"approveReviewRevision": func() error {
			_, err := c.Reviews.ApproveRevision(ctx, "review_id-20", ApproveRevisionParams{RevisionID: "revision_id-20", ExpectedVersion: 20, ExpectedMediaVersion: 201})
			return err
		},
		"rejectReviewRevision": func() error {
			_, err := c.Reviews.RejectRevision(ctx, "review_id-21", RejectRevisionParams{RevisionID: "revision_id-21"})
			return err
		},
		"listReviewMedia": func() error {
			_, err := c.Media.List(ctx, "review_id-22", ListMediaParams{AuthorID: "author_id-22", IncludeUnpublished: ptr(true)}, IncludeGlobal())
			return err
		},
		"createMediaUpload": func() error {
			_, err := c.Media.CreateUpload(ctx, "review_id-23", CreateUploadParams{Kind: KindImage, ContentType: "image/png", SizeBytes: 23, ClientUploadID: "client_upload_id-23", Position: ptr(2), Width: ptr(230), Height: ptr(231), DurationSeconds: ptr(2.3)})
			return err
		},
		"completeMediaUpload": func() error { _, err := c.Media.Complete(ctx, "review_id-24", "media_id-24"); return err },
		"deleteMedia":         func() error { _, err := c.Media.Delete(ctx, "review_id-25", "media_id-25"); return err },
		"approveMedia": func() error {
			_, err := c.Media.Approve(ctx, "review_id-26", "media_id-26", ApproveMediaParams{ExpectedVersion: 26, ExpectedMediaVersion: 261, Reason: ptr("reason-26")})
			return err
		},
		"rejectMedia": func() error {
			_, err := c.Media.Reject(ctx, "review_id-27", "media_id-27", ModerationParams{Reason: ptr("reason-27")})
			return err
		},
		"eraseAuthor": func() error { _, err := c.Authors.Erase(ctx, EraseAuthorParams{AuthorID: "author_id-28"}); return err },
		"listAuditLogs": func() error {
			_, err := c.AuditLogs.List(ctx, ListAuditLogsParams{Action: "action-29", AggregateType: "aggregate_type-29", AggregateID: "aggregate_id-29", ActorID: "actor_id-29", Page: Page{Limit: 9, Offset: 2}}, IncludeGlobal())
			return err
		},
	}
}

func TestEveryOperationIsSentAsTheContractSays(t *testing.T) {
	rec := &recorder{}
	client, err := New("https://reviews.example.com/base", "ocr_contract_key", "tenant-c",
		WithApplication("app-c"), WithNamespace("store", "s-1"), WithActor("actor-c"),
		WithHTTPClient(&http.Client{Transport: rec}))
	if err != nil {
		t.Fatal(err)
	}
	api := contractOperations(t)
	calls := everyCall(context.Background(), client)
	for _, op := range operations {
		call, ok := calls[op.ID]
		if !ok {
			t.Errorf("%s has no recorded call in this test", op.ID)
			continue
		}
		rec.calls = nil
		if err := call(); err != nil {
			t.Errorf("%s: %v", op.ID, err)
			continue
		}
		if len(rec.calls) != 1 {
			t.Errorf("%s sent %d requests", op.ID, len(rec.calls))
			continue
		}
		got := rec.calls[0]
		if got.method != op.Method || !pathMatches(op.Path, strings.TrimPrefix(got.path, "/base/api/v1")) {
			t.Errorf("%s sent %s %s, want %s %s", op.ID, got.method, got.path, op.Method, op.Path)
		}
		if ua := got.header.Get("User-Agent"); ua != "octoreview-go/"+Version {
			t.Errorf("%s: User-Agent %q", op.ID, ua)
		}
		health := op.ID == opGetHealthLive.ID || op.ID == opGetHealthReady.ID
		if !health && (got.header.Get("Authorization") != "Bearer ocr_contract_key" || got.header.Get("X-Tenant-ID") != "tenant-c") {
			t.Errorf("%s: missing credentials", op.ID)
		}
		declared := api[op.Method+" "+op.Path]
		for header := range got.header {
			name := strings.ToLower(header)
			switch name {
			case "authorization", "accept", "user-agent", "content-type", "if-none-match", "x-request-id":
				continue
			}
			if !declared.HeaderList[name] {
				t.Errorf("%s sends header %s, which openapi.json does not declare", op.ID, header)
			}
		}
		sent := map[string]bool{}
		for key := range got.query {
			sent[key] = true
		}
		for key := range got.body {
			sent[key] = true
		}
		for key := range sent {
			if !has(op.Params, key) {
				t.Errorf("%s sent %s, which its operation does not list", op.ID, key)
			}
		}
		for _, param := range op.Params {
			if !sent[param] && !pathHas(op.Path, param) {
				t.Errorf("%s never sent %s", op.ID, param)
			}
		}
		// Each distinguishable value names its parameter, so a swapped field fails here.
		for key, values := range got.query {
			for _, value := range values {
				checkNamed(t, op.ID, key, value)
			}
		}
		for key, value := range got.body {
			if text, ok := value.(string); ok {
				checkNamed(t, op.ID, key, text)
			}
		}
		tparts := strings.Split(op.Path, "/")
		parts := strings.Split(strings.TrimPrefix(got.path, "/base/api/v1"), "/")
		for i, part := range tparts {
			if strings.HasPrefix(part, "{") && i < len(parts) {
				checkNamed(t, op.ID, strings.Trim(part, "{}"), parts[i])
			}
		}
	}
}

var namedValue = regexp.MustCompile(`^([a-z_]+)-\d+$`)

func checkNamed(t *testing.T, opID, key, value string) {
	t.Helper()
	if match := namedValue.FindStringSubmatch(value); match != nil && match[1] != key {
		t.Errorf("%s sent %q as %s", opID, value, key)
	}
}

func pathMatches(template, path string) bool {
	tparts, parts := strings.Split(template, "/"), strings.Split(path, "/")
	if len(tparts) != len(parts) {
		return false
	}
	for i := range tparts {
		if !strings.HasPrefix(tparts[i], "{") && tparts[i] != parts[i] {
			return false
		}
	}
	return true
}

func pathHas(template, param string) bool { return strings.Contains(template, "{"+param+"}") }
