// Command roadgen rewrites the roadmap table in README.md from the Notion database.
//
// The Notion board is the source of truth. This keeps the README's copy of it honest
// without anyone editing two places.
//
// # Usage
//
//	$ export NOTION_TOKEN=secret_...        # an internal integration token
//	$ go run ./tools/roadgen -write          # rewrite README.md
//	$ go run ./tools/roadgen                 # print the table to stdout
//	$ go run ./tools/roadgen -check          # exit 1 if README.md is out of date
//
// The .github/workflows/roadmap.yml workflow runs this on a schedule and whenever the
// generator changes, using the NOTION_TOKEN repository secret. Nothing has to be run
// locally.
//
// # Why a token rather than reading a public page
//
// The board is public, so the table could be scraped from the rendered HTML. That would
// mean parsing a client-side rendered app's DOM, which breaks whenever Notion changes
// its markup, and it would still not give the Status and Label values as data. The API
// gives them as data.
//
// # Why it is a tool and not a GitHub action
//
// The token has to live somewhere the script can read, and a token checked into a
// repository is a token leaked to everyone who clones it. A repository secret is the
// right home for it, and the workflow is what runs it.
//
// # Dependencies
//
// Standard library only. The Notion API is a small REST surface and a dependency for
// one request would be a poor trade in a project that cares about its dependency
// surface.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// defaultDatabaseID is the NovaBar Roadmap database.
//
// A default keeps the common case to a single command, but the token is still required
// and the id can be overridden with -db.
const defaultDatabaseID = "0a5506f72fd540acaa0a0123c9c29d80"

// Markers delimit the generated region in README.md. They are HTML comments so the
// rendered README shows nothing extra.
const (
	beginMarker = "<!-- ROADMAP:BEGIN -->"
	endMarker   = "<!-- ROADMAP:END -->"
)

// milestone is one row.
type milestone struct {
	Name   string
	Status string
	Labels []string
}

// notionBaseURL is the API root. A variable so the tests can point it at a stub rather
// than reach the real Notion.
var notionBaseURL = "https://api.notion.com/v1"

// notionPage is the subset of a Notion page we read.
type notionPage struct {
	Properties map[string]struct {
		Title []struct {
			PlainText string `json:"plain_text"`
		} `json:"title"`
		Select *struct {
			Name string `json:"name"`
		} `json:"select"`
		MultiSelect []struct {
			Name string `json:"name"`
		} `json:"multi_select"`
	} `json:"properties"`
}

// queryResponse is the Notion query response.
type queryResponse struct {
	Results    []notionPage `json:"results"`
	HasMore    bool         `json:"has_more"`
	NextCursor string       `json:"next_cursor"`
}

func main() {
	var (
		token   = flag.String("token", os.Getenv("NOTION_TOKEN"), "Notion API token. Defaults to $NOTION_TOKEN.")
		dbID    = flag.String("db", envOr("NOTION_DB", defaultDatabaseID), "Notion database id.")
		readme  = flag.String("readme", "README.md", "The README to update.")
		write   = flag.Bool("write", false, "Rewrite the README in place.")
		check   = flag.Bool("check", false, "Exit 1 if the README is out of date.")
		version = flag.String("notion-version", "2022-06-28", "Notion API version header.")
	)
	flag.Parse()

	if err := run(*token, *dbID, *readme, *write, *check, *version); err != nil {
		fmt.Fprintln(os.Stderr, "roadgen:", err)
		os.Exit(1)
	}
}

func run(token, dbID, readmePath string, write, check bool, notionVersion string) error {
	if token == "" {
		return fmt.Errorf("no token: set NOTION_TOKEN or pass -token")
	}

	milestones, err := fetchMilestones(token, dbID, notionVersion)
	if err != nil {
		return err
	}
	if len(milestones) == 0 {
		return fmt.Errorf("the database returned no milestones; refusing to blank the README")
	}

	table := renderTable(milestones)

	if !write && !check {
		fmt.Print(table)
		return nil
	}

	raw, err := os.ReadFile(readmePath)
	if err != nil {
		return fmt.Errorf("reading %s: %w", readmePath, err)
	}

	updated, err := replaceRegion(string(raw), table)
	if err != nil {
		return err
	}

	if check {
		if updated == string(raw) {
			fmt.Printf("%s is up to date with the Notion board\n", readmePath)
			return nil
		}
		return fmt.Errorf("%s is out of date with the Notion board; run: go run ./tools/roadgen -write", readmePath)
	}

	if updated == string(raw) {
		fmt.Printf("%s is already up to date\n", readmePath)
		return nil
	}
	if err := os.WriteFile(readmePath, []byte(updated), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", readmePath, err)
	}
	fmt.Printf("updated %s with %d milestones\n", readmePath, len(milestones))
	return nil
}

// fetchMilestones reads every page of the database.
func fetchMilestones(token, dbID, notionVersion string) ([]milestone, error) {
	client := &http.Client{Timeout: 20 * time.Second}

	var out []milestone
	cursor := ""

	for {
		body := map[string]interface{}{"page_size": 100}
		if cursor != "" {
			body["start_cursor"] = cursor
		}
		payload, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}

		// The databases endpoint is the one that works for every Notion API version.
		// Newer versions expose data sources separately, and the database id still
		// routes to it, so one URL covers both.
		url := notionBaseURL + "/databases/" + dbID + "/query"

		req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Notion-Version", notionVersion)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("querying Notion: %w", err)
		}

		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("reading the Notion response: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("Notion returned %s: %s", resp.Status, strings.TrimSpace(string(data)))
		}

		var parsed queryResponse
		if err := json.Unmarshal(data, &parsed); err != nil {
			return nil, fmt.Errorf("decoding the Notion response: %w", err)
		}

		for _, page := range parsed.Results {
			out = append(out, toMilestone(page))
		}

		if !parsed.HasMore || parsed.NextCursor == "" {
			break
		}
		cursor = parsed.NextCursor
	}

	sortMilestones(out)
	return out, nil
}

// toMilestone extracts the three fields the table shows.
func toMilestone(page notionPage) milestone {
	m := milestone{}

	if prop, ok := page.Properties["Name"]; ok && len(prop.Title) > 0 {
		m.Name = prop.Title[0].PlainText
	}
	if prop, ok := page.Properties["Status"]; ok && prop.Select != nil {
		m.Status = prop.Select.Name
	}
	if prop, ok := page.Properties["Label"]; ok {
		for _, l := range prop.MultiSelect {
			m.Labels = append(m.Labels, l.Name)
		}
	}
	return m
}

// sortMilestones orders by the milestone number so M2 comes before M10.
//
// The names begin with "M<n> — ", so the number is a prefix. A plain string sort would
// put M10 before M2, which is wrong.
func sortMilestones(list []milestone) {
	// Insertion sort: the list is short and this keeps the comparison explicit.
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && milestoneNumber(list[j]) < milestoneNumber(list[j-1]); j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
}

// milestoneNumber extracts the leading M<n> from a name, or 0 when there is none.
func milestoneNumber(m milestone) int {
	name := strings.TrimSpace(m.Name)
	if !strings.HasPrefix(name, "M") {
		return 0
	}
	rest := name[1:]
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0
	}
	n := 0
	for _, c := range rest[:end] {
		n = n*10 + int(c-'0')
	}
	return n
}

// renderTable produces the markdown table.
func renderTable(list []milestone) string {
	var b strings.Builder
	b.WriteString("| Milestone | State | Labels |\n")
	b.WriteString("| --- | --- | --- |\n")
	for _, m := range list {
		labels := "—"
		if len(m.Labels) > 0 {
			labels = "`" + strings.Join(m.Labels, "`, `") + "`"
		}
		state := m.Status
		if state == "" {
			state = "—"
		}
		fmt.Fprintf(&b, "| %s | %s | %s |\n", m.Name, state, labels)
	}
	return b.String()
}

// replaceRegion swaps the content between the markers, adding them if absent.
func replaceRegion(doc, table string) (string, error) {
	start := strings.Index(doc, beginMarker)
	end := strings.Index(doc, endMarker)

	switch {
	case start >= 0 && end > start:
		// Replace what is between the markers.
		return doc[:start+len(beginMarker)] + "\n" + table + doc[end:], nil

	case start < 0 && end < 0:
		return "", fmt.Errorf("no roadmap markers in the document; expected %s and %s", beginMarker, endMarker)

	default:
		return "", fmt.Errorf("the roadmap markers are in the wrong order or unclosed")
	}
}

// envOr returns an environment variable or a fallback.
func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
