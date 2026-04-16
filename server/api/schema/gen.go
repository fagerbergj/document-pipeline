// Package schema contains Go types generated from openapi.yaml.
//
// Regenerate with `make generate-server` (runs `go generate ./server/api/schema/...`).
// The generated file types.gen.go is committed so that clean checkouts build without
// the oapi-codegen binary on PATH; CI's codegen-drift job regenerates and diffs to
// catch schema edits that weren't followed up with a regen.
package schema

//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen --config=config.yaml ../../../openapi.yaml
