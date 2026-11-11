// Package apigen holds the strict server interface and models that oapi-codegen
// generates from api/openapi.yaml. Don't edit the generated file: change the
// contract and run `go run ./cmd/dev gen`.
package apigen

//go:generate go tool oapi-codegen -config oapi-codegen.yaml ../../../api/openapi.yaml
