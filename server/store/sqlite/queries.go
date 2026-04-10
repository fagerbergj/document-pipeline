package sqlite

import (
	"embed"
	"strings"
)

//go:embed queries
var queryFS embed.FS

// q holds all SQL queries loaded at startup.
// Keys are "<entity>.<Operation>", e.g. "artifacts.Insert".
// Each query lives in queries/<entity>/<Operation>.sql.
var q = mustLoadQueries()

func mustLoadQueries() map[string]string {
	m := make(map[string]string)
	dirs, err := queryFS.ReadDir("queries")
	if err != nil {
		panic("load queries: " + err.Error())
	}
	for _, dir := range dirs {
		if !dir.IsDir() {
			continue
		}
		files, err := queryFS.ReadDir("queries/" + dir.Name())
		if err != nil {
			panic("load queries: " + err.Error())
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".sql") {
				continue
			}
			path := "queries/" + dir.Name() + "/" + f.Name()
			content, err := queryFS.ReadFile(path)
			if err != nil {
				panic("load queries: " + err.Error())
			}
			stem := strings.TrimSuffix(f.Name(), ".sql")
			m[dir.Name()+"."+stem] = strings.TrimSpace(string(content))
		}
	}
	return m
}
