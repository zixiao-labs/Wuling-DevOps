// Package schemas embeds the versioned editor schemas served by wuling-api.
// Keeping the JSON source beside the embed declarations gives editors and the
// public /.well-known endpoint one canonical copy.
package schemas

import _ "embed"

// WorkflowV1 is the public schema for .wuling/workflows/*.yml and *.yaml.
//
//go:embed wuling-workflow.schema.json
var WorkflowV1 []byte

// RunnerConfigV1 is the public schema for runner-config.yaml. Its versioned
// endpoint name stays stable even as it describes runner-config version 1 and
// version 2 documents for backwards-compatible editor support.
//
//go:embed runner-config.schema.json
var RunnerConfigV1 []byte
