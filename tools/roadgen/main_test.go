package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMilestoneNumber pins the milestone ordering.
//
// The names begin "M<n> — ", and a plain string sort would put M10 before M2, which
// would scramble the table.
func TestMilestoneNumber(t *testing.T) {
	cases := []struct {
		name string
		want int
	}{
		{"M1 — Something", 1},
		{"M2 — Something else", 2},
		{"M10 — Later", 10},
		{"M26 — Latest", 26},
		{"No number here", 0},
		{"M — no digits", 0},
		{"", 0},
	}
	for _, c := range cases {
		if got := milestoneNumber(milestone{Name: c.name}); got != c.want {
			t.Errorf("milestoneNumber(%q) = %d, want %d", c.name, got, c.want)
		}
	}
}

// TestSortMilestones is the ordering the table depends on.
func TestSortMilestones(t *testing.T) {
	list := []milestone{
		{Name: "M10 — Later"},
		{Name: "M2 — Second"},
		{Name: "M1 — First"},
		{Name: "M26 — Last"},
	}
	sortMilestones(list)

	want := []string{"M1 — First", "M2 — Second", "M10 — Later", "M26 — Last"}
	for i, w := range want {
		if list[i].Name != w {
			t.Errorf("position %d = %q, want %q", i, list[i].Name, w)
		}
	}
}

// TestRenderTable covers the shape and the empty-label case.
func TestRenderTable(t *testing.T) {
	got := renderTable([]milestone{
		{Name: "M1 — First", Status: "Done", Labels: []string{"core", "win32"}},
		{Name: "M2 — Second", Status: "Planned"},
	})

	if !strings.HasPrefix(got, "| Milestone | State | Labels |") {
		t.Errorf("table does not start with the header row:\n%s", got)
	}
	if !strings.Contains(got, "| M1 — First | Done | `core`, `win32` |") {
		t.Errorf("labels are not rendered as code spans:\n%s", got)
	}
	// A milestone with no labels must show a placeholder rather than an empty cell,
	// which would render as a broken table.
	if !strings.Contains(got, "| M2 — Second | Planned | — |") {
		t.Errorf("a milestone with no labels is not rendered with a placeholder:\n%s", got)
	}
}

// TestReplaceRegion covers the three states of the markers.
func TestReplaceRegion(t *testing.T) {
	doc := "before\n" + beginMarker + "\nold table\n" + endMarker + "\nafter\n"

	got, err := replaceRegion(doc, "new table\n")
	if err != nil {
		t.Fatalf("replaceRegion: %v", err)
	}
	if !strings.Contains(got, beginMarker+"\nnew table\n"+endMarker) {
		t.Errorf("the region was not replaced:\n%s", got)
	}
	if !strings.Contains(got, "before") || !strings.Contains(got, "after") {
		t.Errorf("the surrounding text was lost:\n%s", got)
	}
	if strings.Contains(got, "old table") {
		t.Errorf("the old table survived:\n%s", got)
	}

	// Markers present but in the wrong order must be refused rather than mangling the
	// document.
	if _, err := replaceRegion(endMarker+"\n"+beginMarker, "x"); err == nil {
		t.Error("reversed markers were accepted")
	}

	// No markers at all must be refused too, so the tool can never silently mangle a
	// README that has been restructured.
	if _, err := replaceRegion("no markers here", "x"); err == nil {
		t.Error("a document with no markers was accepted")
	}
}

// TestFetchMilestones exercises the whole HTTP path against a stub, including
// pagination. This is what verifies the JSON shapes, which nothing else covers.
func TestFetchMilestones(t *testing.T) {
	page := func(cursor string, names []string) map[string]interface{} {
		results := []map[string]interface{}{}
		for _, n := range names {
			results = append(results, map[string]interface{}{
				"properties": map[string]interface{}{
					"Name": map[string]interface{}{
						"title": []map[string]string{{"plain_text": n}},
					},
					"Status": map[string]interface{}{
						"select": map[string]string{"name": "Planned"},
					},
					"Label": map[string]interface{}{
						"multi_select": []map[string]string{{"name": "ui"}},
					},
				},
			})
		}
		body := map[string]interface{}{"results": results, "has_more": false}
		if cursor != "" {
			body["next_cursor"] = cursor
			body["has_more"] = true
		}
		return body
	}

	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization header = %q", got)
		}
		var req struct {
			StartCursor string `json:"start_cursor"`
		}
		json.NewDecoder(r.Body).Decode(&req)

		if req.StartCursor == "" {
			json.NewEncoder(w).Encode(page("cursor-2", []string{"M2 — Second"}))
			return
		}
		json.NewEncoder(w).Encode(page("", []string{"M1 — First"}))
	}))
	defer srv.Close()

	// Point the tool at the stub. The base URL is a package variable so the test does
	// not have to reach the real Notion.
	old := notionBaseURL
	notionBaseURL = srv.URL
	defer func() { notionBaseURL = old }()

	got, err := fetchMilestones("test-token", "db-id", "2022-06-28")
	if err != nil {
		t.Fatalf("fetchMilestones: %v", err)
	}
	if calls != 2 {
		t.Errorf("made %d requests, want 2 across a paginated response", calls)
	}
	if len(got) != 2 {
		t.Fatalf("got %d milestones, want 2", len(got))
	}
	// Sorted, so M1 comes first.
	if got[0].Name != "M1 — First" {
		t.Errorf("first milestone = %q, want M1", got[0].Name)
	}
	if got[0].Status != "Planned" {
		t.Errorf("status = %q, want Planned", got[0].Status)
	}
	if len(got[0].Labels) != 1 || got[0].Labels[0] != "ui" {
		t.Errorf("labels = %v, want [ui]", got[0].Labels)
	}
}

// TestFetchMilestonesReportsAnError checks that a rejected token surfaces as an error
// rather than as an empty roadmap, which would blank the README.
func TestFetchMilestonesReportsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"message":"API token is invalid."}`))
	}))
	defer srv.Close()

	old := notionBaseURL
	notionBaseURL = srv.URL
	defer func() { notionBaseURL = old }()

	if _, err := fetchMilestones("bad", "db-id", "2022-06-28"); err == nil {
		t.Error("an invalid token did not produce an error")
	}
}

// TestRunRefusesToBlankTheReadme: an empty database must not wipe the table.
func TestRunRefusesToBlankTheReadme(t *testing.T) {
	dir := t.TempDir()
	readme := filepath.Join(dir, "README.md")
	original := "before\n" + beginMarker + "\n| Milestone |\n| --- |\n| M1 — One |\n" + endMarker + "\nafter\n"
	if err := os.WriteFile(readme, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	// A stub that returns nothing.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()

	old := notionBaseURL
	notionBaseURL = srv.URL
	defer func() { notionBaseURL = old }()

	if err := run("token", "db", readme, true, false, "2022-06-28"); err == nil {
		t.Error("an empty database was accepted; the README would have been blanked")
	}

	after, err := os.ReadFile(readme)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != original {
		t.Error("the README was modified despite the refusal")
	}
}

// TestRunCheckReportsDrift covers the mode a CI job would call.
func TestRunCheckReportsDrift(t *testing.T) {
	dir := t.TempDir()
	readme := filepath.Join(dir, "README.md")
	original := "before\n" + beginMarker + "\nstale\n" + endMarker + "\nafter\n"
	if err := os.WriteFile(readme, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"results":[{"properties":{
			"Name":{"title":[{"plain_text":"M1 — Fresh"}]},
			"Status":{"select":{"name":"Done"}},
			"Label":{"multi_select":[{"name":"core"}]}}}]}`))
	}))
	defer srv.Close()

	old := notionBaseURL
	notionBaseURL = srv.URL
	defer func() { notionBaseURL = old }()

	if err := run("token", "db", readme, false, true, "2022-06-28"); err == nil {
		t.Error("-check did not report that the README is stale")
	}

	// With -write it should be brought up to date.
	if err := run("token", "db", readme, true, false, "2022-06-28"); err != nil {
		t.Fatalf("-write: %v", err)
	}
	after, err := os.ReadFile(readme)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(after), "M1 — Fresh") {
		t.Errorf("the README was not updated:\n%s", after)
	}
	if !strings.Contains(string(after), "before") || !strings.Contains(string(after), "after") {
		t.Errorf("the surrounding text was lost:\n%s", after)
	}
}
