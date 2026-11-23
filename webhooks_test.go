package octoreview

import (
	"errors"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

// Signed by the server's own octoreview.events.dispatch.webhook_signature (secret whsec-test).
const (
	vectorBody      = `{"id": "8d0c5a2e-7f0a-4b8e-9d55-2f4f8d6f3a10", "type": "review.published", "occurred_at": "2026-11-19T10:15:00.123456+00:00", "tenant_id": "example", "application_id": "storefront", "namespace_type": null, "namespace_id": null, "resource_type": "review", "resource_id": "1f9e7c5a-0b2d-4c8e-a6f1-3d5b7e9c1a20", "request_id": "req_1", "data": {"review_id": "1f9e7c5a-0b2d-4c8e-a6f1-3d5b7e9c1a20", "entity_id": "2a8f6d4b-1c3e-4f5a-9b7d-0e2c4a6b8d10", "entity_type": "product", "external_id": "sku-1", "status": "published", "previous_status": null, "rating": 5, "is_verified_purchase": true, "version": 2, "media_version": 1}}`
	vectorSignature = "t=1795000000,v1=0bc8a000e4f4aee9b555b260a8f723569694a5ac8449ae1375fe1620d7165722"
	vectorSecret    = "whsec-test"
)

var vectorClock = WithClock(func() time.Time { return time.Unix(1795000000+60, 0) })

func TestAServerSignedWebhookVerifiesAndDecodes(t *testing.T) {
	event, err := VerifyWebhook([]byte(vectorBody), vectorSignature, vectorSecret, vectorClock)
	if err != nil {
		t.Fatal(err)
	}
	review, err := event.Review()
	if err != nil || event.Type != EventReviewPublished || review.ExternalID != "sku-1" ||
		review.PreviousStatus != nil || !review.IsVerifiedPurchase || review.Version != 2 {
		t.Fatalf("got %+v, %+v, %v", event, review, err)
	}
	if _, err := event.Media(); err == nil {
		t.Error("a review event has no media payload")
	}
}

func TestATamperedOrStaleWebhookIsRejected(t *testing.T) {
	cases := map[string]func() error{
		"another body": func() error {
			_, err := VerifyWebhook([]byte(strings.Replace(vectorBody, `"rating": 5`, `"rating": 1`, 1)), vectorSignature, vectorSecret, vectorClock)
			return err
		},
		"another secret": func() error {
			_, err := VerifyWebhook([]byte(vectorBody), vectorSignature, "whsec-other", vectorClock)
			return err
		},
		"too old": func() error {
			_, err := VerifyWebhook([]byte(vectorBody), vectorSignature, vectorSecret,
				WithClock(func() time.Time { return time.Unix(1795000000+301, 0) }))
			return err
		},
		"from the future": func() error {
			_, err := VerifyWebhook([]byte(vectorBody), vectorSignature, vectorSecret,
				WithClock(func() time.Time { return time.Unix(1795000000-301, 0) }))
			return err
		},
		"malformed": func() error {
			_, err := VerifyWebhook([]byte(vectorBody), "sha256=abc", vectorSecret, vectorClock)
			return err
		},
	}
	for name, run := range cases {
		if err := run(); !errors.Is(err, ErrInvalidSignature) {
			t.Errorf("%s: got %v", name, err)
		}
	}
	client, _ := New("https://reviews.example.com", "ocr_x", "t")
	if _, err := client.Webhooks.Verify([]byte(vectorBody), vectorSignature, vectorSecret, vectorClock); err != nil {
		t.Errorf("the client method must verify the same way: %v", err)
	}
}

// documentedEvents parses the vendored events.md: event type -> its payload field names.
func documentedEvents(t *testing.T) map[string][]string {
	t.Helper()
	raw, err := os.ReadFile("testdata/contract/events.md")
	if err != nil {
		t.Fatalf("read the vendored events.md (run make vendor-contract): %v", err)
	}
	heading := regexp.MustCompile("(?m)^### ([a-z_.]+)$")
	field := regexp.MustCompile("(?m)^\\| `([a-z_]+)` \\|")
	sections := heading.FindAllStringSubmatchIndex(string(raw), -1)
	out := map[string][]string{}
	for i, loc := range sections {
		end := len(raw)
		if i+1 < len(sections) {
			end = sections[i+1][0]
		}
		name := string(raw[loc[2]:loc[3]])
		for _, m := range field.FindAllStringSubmatch(string(raw[loc[1]:end]), -1) {
			out[name] = append(out[name], m[1])
		}
	}
	return out
}

func jsonFields(v any) []string {
	var names []string
	typ := reflect.TypeOf(v)
	for i := 0; i < typ.NumField(); i++ {
		name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func TestEveryEventPayloadMatchesTheContract(t *testing.T) {
	documented := documentedEvents(t)
	families := map[string]any{EventEntityRatingChanged: RatingChangedEvent{}, EventAuthorErased: AuthorErasedEvent{}}
	for _, typ := range reviewEvents {
		families[typ] = ReviewEvent{}
	}
	for _, typ := range mediaEvents {
		families[typ] = MediaEvent{}
	}
	for typ, fields := range documented {
		payload, ok := families[typ]
		if !ok {
			t.Errorf("events.md documents %s, which this SDK does not decode", typ)
			continue
		}
		declared := jsonFields(payload)
		for _, f := range fields {
			if !has(declared, f) {
				t.Errorf("%s: %T lacks %s", typ, payload, f)
			}
		}
	}
	for typ := range families {
		if _, ok := documented[typ]; !ok {
			t.Errorf("this SDK decodes %s, which events.md does not document", typ)
		}
	}
	// Each payload field appears in at least one documented type of its family.
	for _, payload := range []any{ReviewEvent{}, MediaEvent{}, RatingChangedEvent{}, AuthorErasedEvent{}} {
		for _, f := range jsonFields(payload) {
			found := false
			for typ, fields := range documented {
				if reflect.TypeOf(families[typ]) == reflect.TypeOf(payload) && has(fields, f) {
					found = true
				}
			}
			if !found {
				t.Errorf("%T.%s is documented by no event type", payload, f)
			}
		}
	}
}
