package octoreview

import (
	"os"
	"sort"
	"strings"
	"testing"
)

// retryTable renders the README's retry-safety table from the operation table (the server's
// docs/sdk-surface.yaml, checked by the contract gate), so the README cannot drift from it.
func retryTable() string {
	byClass := map[retryClass][]string{}
	for _, op := range operations {
		byClass[op.Retry] = append(byClass[op.Retry], "`"+op.GoMethod+"`")
	}
	var b strings.Builder
	b.WriteString("| Retry class | Behavior | Methods |\n| --- | --- | --- |\n")
	rows := []struct {
		class    retryClass
		behavior string
	}{
		{retrySafe, "a read: retried on timeouts, 503 and 429, honoring Retry-After"},
		{retryIdempotent, "repeats to the same state: retried like a read"},
		{retryKeyed, "idempotent with its `ClientUploadID`, which the SDK fills in: retried like a read"},
		{retryUnsafe, "never retried; after a lost response, read the resource to learn the outcome"},
	}
	for _, row := range rows {
		methods := byClass[row.class]
		sort.Strings(methods)
		b.WriteString("| `" + string(row.class) + "` | " + row.behavior + " | " + strings.Join(methods, ", ") + " |\n")
	}
	return b.String()
}

func TestTheReadmeRetryTableMatchesTheSurface(t *testing.T) {
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	const start, end = "<!-- retry-table:start -->\n", "<!-- retry-table:end -->"
	text := string(readme)
	i, j := strings.Index(text, start), strings.Index(text, end)
	if i < 0 || j < i {
		t.Fatal("README.md lost its retry-table markers")
	}
	if got := text[i+len(start) : j]; got != retryTable() {
		t.Errorf("README.md's retry table is stale; replace it with:\n%s", retryTable())
	}
}
