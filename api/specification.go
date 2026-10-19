// Package api хранит публичное описание HTTP API.
package api

import _ "embed"

//go:embed openapi.json
var Specification []byte
