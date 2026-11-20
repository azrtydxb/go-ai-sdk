package provider

import "fmt"

// UnsupportedFeatureError is returned by a provider asked for a request
// option its API has no equivalent for. Providers return it instead of
// silently dropping the option, so a caller relying on it (structured
// output, logprobs) learns the request would not behave as asked.
type UnsupportedFeatureError struct {
	Provider string // provider name, as ProviderName() reports it
	Feature  string // the option, e.g. "ResponseFormat" or "Logprobs"
	Detail   string // optional extra context
}

// Error implements error.
func (e *UnsupportedFeatureError) Error() string {
	msg := fmt.Sprintf("%s: %s is not supported", e.Provider, e.Feature)
	if e.Detail != "" {
		msg += ": " + e.Detail
	}
	return msg
}
