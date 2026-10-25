package rdash

import (
	"net/url"
	"strings"

	"github.com/tsdlamongan/whcms/backend/internal/integration/redact"
)

// redactedPlaceholder replaces sensitive values in integration logs.
const redactedPlaceholder = redact.Placeholder

// maxRawBodyLog caps non-JSON response bodies persisted to integration logs.
const maxRawBodyLog = 512

// redactor masks keys (case-insensitive) from logged request/response bodies
// per CONTRACTS.md section 3, plus RDash-specific secrets (EPP auth codes).
var redactor = redact.Keys(
	"apikey", "api_key", "password", "password_confirmation", "passwd",
	"token", "signature", "authorization", "secret", "epp", "epp_code", "auth_code",
)

// redactJSON parses raw JSON and masks every sensitive key recursively. When
// the body is not valid JSON a truncated raw string is logged under "raw" so
// the integration log still captures something useful.
func redactJSON(raw []byte) any { return redactor.BodyWrapped(raw, maxRawBodyLog) }

// redactForm mirrors redactJSON for application/x-www-form-urlencoded request
// bodies logged via ports.IntegrationCall.Request: single-value fields log as
// a plain string, repeated fields (e.g. nameserver[0]/[1]/...) as a list.
func redactForm(v url.Values) any { return redactor.Form(v) }

// redactValue walks a decoded JSON value and masks sensitive map keys,
// returning a redacted copy.
func redactValue(v any) any { return redactor.Value(v) }

// redactResponse redacts a response body for the integration log. The
// document upload link endpoint returns the bearer URL itself as `data`
// (under no sensitive key), so its body is never logged.
func redactResponse(path string, raw []byte) any {
	if strings.Contains(path, "/documents/link") {
		return map[string]any{"data": redactedPlaceholder}
	}
	return redactJSON(raw)
}
