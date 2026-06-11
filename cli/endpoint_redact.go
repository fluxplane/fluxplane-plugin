package cli

import (
	"net/url"
	"regexp"
	"strings"

	fpendpoint "github.com/fluxplane/fluxplane-endpoint"
	"github.com/fluxplane/fluxplane-plugin/management"
)

// Endpoint URLs may embed credentials (mysql://user:pass@host/db). Display
// surfaces redact the password to xxxxx; stored values are untouched — the
// host resolves the real URL at invoke time.

var urlUserinfoPattern = regexp.MustCompile(`(://[^/@:\s]+):([^@/\s]+)@`)

func redactEndpointURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || !strings.Contains(trimmed, "@") {
		return raw
	}
	if parsed, err := url.Parse(trimmed); err == nil && parsed.User != nil {
		if _, has := parsed.User.Password(); has {
			parsed.User = url.UserPassword(parsed.User.Username(), "xxxxx")
			return parsed.String()
		}
		return raw
	}
	// Unparseable but credential-shaped: best-effort regex redaction.
	return urlUserinfoPattern.ReplaceAllString(raw, "$1:xxxxx@")
}

func redactEndpointRef(ref fpendpoint.EndpointRef) fpendpoint.EndpointRef {
	ref.URL = redactEndpointURL(ref.URL)
	return ref
}

func redactEndpointRecord(record fpendpoint.Record) fpendpoint.Record {
	record.EndpointRef = redactEndpointRef(record.EndpointRef)
	return record
}

func redactEndpointListResult(result management.EndpointListResult) management.EndpointListResult {
	for i := range result.Endpoints {
		result.Endpoints[i] = redactEndpointRecord(result.Endpoints[i])
	}
	return result
}

func redactEndpointGetResult(result management.EndpointGetResult) management.EndpointGetResult {
	result.Endpoint = redactEndpointRef(result.Endpoint)
	result.Record = redactEndpointRecord(result.Record)
	return result
}

func redactEndpointSaveResult(result management.EndpointSaveResult) management.EndpointSaveResult {
	result.Endpoint = redactEndpointRef(result.Endpoint)
	result.Record = redactEndpointRecord(result.Record)
	return result
}
