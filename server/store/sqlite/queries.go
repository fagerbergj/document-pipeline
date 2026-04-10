package sqlite

import (
	"embed"
	"fmt"
	"strings"
)

//go:embed queries/*.sql
var queryFS embed.FS

// q holds all named SQL queries loaded at startup.
// Keys are "<file>.<Name>", e.g. "artifacts.Insert".
var q = mustLoadQueries()

func mustLoadQueries() map[string]string {
	m, err := loadQueries()
	if err != nil {
		panic("load SQL queries: " + err.Error())
	}
	return m
}

// loadQueries reads every *.sql file from the embedded queries/ directory and
// parses named queries delimited by "-- name: <Name>" comment lines.
// Each query is stored under the key "<stem>.<Name>".
func loadQueries() (map[string]string, error) {
	entries, err := queryFS.ReadDir("queries")
	if err != nil {
		return nil, err
	}

	m := make(map[string]string)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		stem := strings.TrimSuffix(e.Name(), ".sql")
		data, err := queryFS.ReadFile("queries/" + e.Name())
		if err != nil {
			return nil, err
		}
		if err := parseQueryFile(stem, string(data), m); err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
	}
	return m, nil
}

// parseQueryFile splits a SQL file on "-- name: <Name>" lines and adds each
// named block to m under the key "<stem>.<Name>".
func parseQueryFile(stem, content string, m map[string]string) error {
	var (
		currentName string
		buf         strings.Builder
	)

	flush := func() {
		if currentName == "" {
			return
		}
		m[stem+"."+currentName] = strings.TrimSpace(buf.String())
		buf.Reset()
	}

	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "-- name:") {
			flush()
			currentName = strings.TrimSpace(strings.TrimPrefix(trimmed, "-- name:"))
			continue
		}
		if currentName != "" {
			buf.WriteString(line)
			buf.WriteByte('\n')
		}
	}
	flush()
	return nil
}
