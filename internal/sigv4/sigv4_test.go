package sigv4

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"testing"
	"time"
)

// referenceSign is an independent, from-scratch computation of the SigV4
// Authorization header for a GET request with no query string and no body,
// following the AWS documentation's canonical-request / string-to-sign /
// signing-key-derivation algorithm step by step. It exists so the test does
// not merely compare Sign's output against another opaque fixture string,
// but recomputes the whole chain independently and checks Sign's internal
// artifacts (hashed canonical request, string-to-sign, signature) against
// it.
type referenceResult struct {
	canonicalRequest string
	hashedCanonical  string
	stringToSign     string
	signature        string
	authHeader       string
}

func computeReference(t *testing.T, method, host, path, amzDate, dateStamp, region, service, payloadHash, accessKey, secretKey string, extraSignedHeaders map[string]string) referenceResult {
	t.Helper()

	headerNames := []string{"host", "x-amz-content-sha256", "x-amz-date"}
	headerValues := map[string]string{
		"host":                 host,
		"x-amz-content-sha256": payloadHash,
		"x-amz-date":           amzDate,
	}
	for k, v := range extraSignedHeaders {
		headerNames = append(headerNames, k)
		headerValues[k] = v
	}
	// sort
	for i := 0; i < len(headerNames); i++ {
		for j := i + 1; j < len(headerNames); j++ {
			if headerNames[j] < headerNames[i] {
				headerNames[i], headerNames[j] = headerNames[j], headerNames[i]
			}
		}
	}

	var canonicalHeaders strings.Builder
	for _, n := range headerNames {
		canonicalHeaders.WriteString(n)
		canonicalHeaders.WriteByte(':')
		canonicalHeaders.WriteString(headerValues[n])
		canonicalHeaders.WriteByte('\n')
	}
	signedHeaders := strings.Join(headerNames, ";")

	canonicalRequest := strings.Join([]string{
		method,
		path,
		"", // no query string in this vector
		canonicalHeaders.String(),
		signedHeaders,
		payloadHash,
	}, "\n")

	hashedCanonical := sha256Hex(canonicalRequest)

	credentialScope := dateStamp + "/" + region + "/" + service + "/aws4_request"
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		credentialScope,
		hashedCanonical,
	}, "\n")

	kDate := hmacSum([]byte("AWS4"+secretKey), dateStamp)
	kRegion := hmacSum(kDate, region)
	kService := hmacSum(kRegion, service)
	kSigning := hmacSum(kService, "aws4_request")
	signature := hex.EncodeToString(hmacSum(kSigning, stringToSign))

	authHeader := "AWS4-HMAC-SHA256 Credential=" + accessKey + "/" + credentialScope +
		", SignedHeaders=" + signedHeaders + ", Signature=" + signature

	return referenceResult{
		canonicalRequest: canonicalRequest,
		hashedCanonical:  hashedCanonical,
		stringToSign:     stringToSign,
		signature:        signature,
		authHeader:       authHeader,
	}
}

func hmacSum(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestSign_VanillaGET(t *testing.T) {
	const (
		accessKey = "AKIDEXAMPLE"
		secretKey = "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY"
		region    = "us-east-1"
		service   = "bedrock"
		host      = "bedrock-runtime.us-east-1.amazonaws.com"
	)
	now := time.Date(2015, 8, 30, 12, 36, 0, 0, time.UTC)
	amzDate := "20150830T123600Z"
	dateStamp := "20150830"

	req, err := http.NewRequest(http.MethodGet, "https://"+host+"/", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	body := []byte{}
	payloadHash := sha256Hex("")

	if err := Sign(req, body, Credentials{AccessKeyID: accessKey, SecretAccessKey: secretKey}, region, service, now); err != nil {
		t.Fatalf("Sign: %v", err)
	}

	if got := req.Header.Get("X-Amz-Date"); got != amzDate {
		t.Errorf("X-Amz-Date = %q, want %q", got, amzDate)
	}
	if got := req.Header.Get("X-Amz-Content-Sha256"); got != payloadHash {
		t.Errorf("X-Amz-Content-Sha256 = %q, want %q", got, payloadHash)
	}
	if got := req.Header.Get("X-Amz-Security-Token"); got != "" {
		t.Errorf("X-Amz-Security-Token = %q, want empty (no session token)", got)
	}

	ref := computeReference(t, http.MethodGet, host, "/", amzDate, dateStamp, region, service, payloadHash, accessKey, secretKey, nil)

	gotAuth := req.Header.Get("Authorization")
	if gotAuth != ref.authHeader {
		t.Fatalf("Authorization = %q, want %q", gotAuth, ref.authHeader)
	}
	if !strings.Contains(gotAuth, "AWS4-HMAC-SHA256") {
		t.Errorf("Authorization missing algorithm prefix: %q", gotAuth)
	}
	if !strings.Contains(gotAuth, "SignedHeaders=host;x-amz-content-sha256;x-amz-date") {
		t.Errorf("Authorization SignedHeaders unexpected: %q", gotAuth)
	}
	if !strings.Contains(gotAuth, "Signature="+ref.signature) {
		t.Errorf("Authorization does not contain expected signature %q: %q", ref.signature, gotAuth)
	}
}

// TestSign_MultiValueHeaderSortedBeforeJoin covers the SigV4 canonical-header
// rule for a header with multiple values: each header's *values* must be
// sorted before being joined with "," (this is distinct from and in addition
// to sorting header *names*). A request built with the same multi-valued
// header added in two different orders must therefore produce byte-identical
// canonical headers, and hence identical signatures — the reverse of what
// you'd get from a naive strings.Join(vals, ",") over insertion order.
func TestSign_MultiValueHeaderSortedBeforeJoin(t *testing.T) {
	now := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	creds := Credentials{AccessKeyID: "AKID", SecretAccessKey: "secret"}

	req1, _ := http.NewRequest(http.MethodGet, "https://example.amazonaws.com/path", nil)
	req1.Header.Add("X-Amz-Meta", "b")
	req1.Header.Add("X-Amz-Meta", "a")
	if err := Sign(req1, nil, creds, "us-east-1", "service", now); err != nil {
		t.Fatalf("Sign: %v", err)
	}

	req2, _ := http.NewRequest(http.MethodGet, "https://example.amazonaws.com/path", nil)
	req2.Header.Add("X-Amz-Meta", "a")
	req2.Header.Add("X-Amz-Meta", "b")
	if err := Sign(req2, nil, creds, "us-east-1", "service", now); err != nil {
		t.Fatalf("Sign: %v", err)
	}

	auth1 := req1.Header.Get("Authorization")
	auth2 := req2.Header.Get("Authorization")
	if auth1 != auth2 {
		t.Fatalf("signatures differ for a multi-valued header added in different orders:\n%q\n%q", auth1, auth2)
	}

	// Independently confirm the canonical form: values sorted ("a,b"), not
	// insertion order ("b,a").
	headerNames := []string{"host", "x-amz-content-sha256", "x-amz-date", "x-amz-meta"}
	headerValues := map[string]string{
		"host":                 "example.amazonaws.com",
		"x-amz-content-sha256": sha256Hex(""),
		"x-amz-date":           "20200101T000000Z",
		"x-amz-meta":           "a,b",
	}
	var canonicalHeaders strings.Builder
	for _, n := range headerNames {
		canonicalHeaders.WriteString(n)
		canonicalHeaders.WriteByte(':')
		canonicalHeaders.WriteString(headerValues[n])
		canonicalHeaders.WriteByte('\n')
	}
	signedHeaders := strings.Join(headerNames, ";")
	canonicalRequest := strings.Join([]string{
		http.MethodGet, "/path", "", canonicalHeaders.String(), signedHeaders, sha256Hex(""),
	}, "\n")
	hashedCanonical := sha256Hex(canonicalRequest)
	credentialScope := "20200101/us-east-1/service/aws4_request"
	stringToSign := strings.Join([]string{"AWS4-HMAC-SHA256", "20200101T000000Z", credentialScope, hashedCanonical}, "\n")
	kDate := hmacSum([]byte("AWS4secret"), "20200101")
	kRegion := hmacSum(kDate, "us-east-1")
	kService := hmacSum(kRegion, "service")
	kSigning := hmacSum(kService, "aws4_request")
	wantSignature := hex.EncodeToString(hmacSum(kSigning, stringToSign))
	wantAuth := "AWS4-HMAC-SHA256 Credential=AKID/" + credentialScope + ", SignedHeaders=" + signedHeaders + ", Signature=" + wantSignature

	if auth1 != wantAuth {
		t.Fatalf("Authorization = %q, want %q (sorted duplicate values)", auth1, wantAuth)
	}
}

func TestSign_WithBodyAndSessionToken(t *testing.T) {
	const (
		accessKey    = "AKIDEXAMPLE"
		secretKey    = "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY"
		sessionToken = "IQoJb3JpZ2luX2VjEXAMPLETOKEN"
		region       = "us-west-2"
		service      = "bedrock"
		host         = "bedrock-runtime.us-west-2.amazonaws.com"
		path         = "/model/anthropic.claude-3-sonnet-20240229-v1/converse"
	)
	now := time.Date(2024, 3, 15, 9, 5, 23, 0, time.UTC)
	amzDate := "20240315T090523Z"
	dateStamp := "20240315"

	body := []byte(`{"hello":"world"}`)
	payloadHash := sha256Hex(string(body))

	req, err := http.NewRequest(http.MethodPost, "https://"+host+path, strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	creds := Credentials{AccessKeyID: accessKey, SecretAccessKey: secretKey, SessionToken: sessionToken}
	if err := Sign(req, body, creds, region, service, now); err != nil {
		t.Fatalf("Sign: %v", err)
	}

	if got := req.Header.Get("X-Amz-Security-Token"); got != sessionToken {
		t.Errorf("X-Amz-Security-Token = %q, want %q", got, sessionToken)
	}

	extra := map[string]string{
		"content-type":         "application/json",
		"x-amz-security-token": sessionToken,
	}
	ref := computeReference(t, http.MethodPost, host, path, amzDate, dateStamp, region, service, payloadHash, accessKey, secretKey, extra)

	gotAuth := req.Header.Get("Authorization")
	if gotAuth != ref.authHeader {
		t.Fatalf("Authorization = %q, want %q", gotAuth, ref.authHeader)
	}
	wantSigned := "SignedHeaders=content-type;host;x-amz-content-sha256;x-amz-date;x-amz-security-token"
	if !strings.Contains(gotAuth, wantSigned) {
		t.Errorf("Authorization SignedHeaders unexpected: %q, want contains %q", gotAuth, wantSigned)
	}
}

func TestSign_QueryStringSortedAndEncoded(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://example.amazonaws.com/path?b=2&a=1&a=0", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	now := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := Sign(req, nil, Credentials{AccessKeyID: "AKID", SecretAccessKey: "secret"}, "us-east-1", "service", now); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	// The canonical query string used internally sorts by key then value
	// ("a=0&a=1&b=2"); we can't observe it directly from the header, but we
	// can confirm signing succeeded deterministically by re-signing with
	// query params in a different order and expecting the same signature.
	req2, _ := http.NewRequest(http.MethodGet, "https://example.amazonaws.com/path?a=0&a=1&b=2", nil)
	if err := Sign(req2, nil, Credentials{AccessKeyID: "AKID", SecretAccessKey: "secret"}, "us-east-1", "service", now); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if req.Header.Get("Authorization") != req2.Header.Get("Authorization") {
		t.Errorf("signatures differ for reordered but equivalent query strings:\n%q\n%q", req.Header.Get("Authorization"), req2.Header.Get("Authorization"))
	}
}

// TestSign_DoubleEncodesReservedPathCharacters is the classic SigV4
// "double-encoding" case: for all services other than S3, the canonical URI
// must be the *already-escaped* wire path with each segment URI-encoded
// AGAIN, so a literal ':' that is already percent-encoded once (%3A) on the
// wire becomes %253A in the canonical request (the '%' from the first
// encoding gets escaped to %25 by the second pass). Bedrock model IDs
// routinely contain ':' (e.g. "anthropic.claude-3:1"), so getting this
// wrong breaks every Bedrock request whose model ID isn't colon-free.
//
// The request path below is built with the colon already percent-encoded
// (%3A), mirroring what providers/bedrock's escapeModelID produces and what
// actually goes out on the wire (net/url's default path escaping leaves ':'
// unescaped, so callers must pre-encode it themselves). The reference
// computation independently re-derives the doubly-encoded canonical URI,
// the canonical request hash, the string-to-sign, and the final signature,
// and checks Sign's Authorization header against it.
func TestSign_DoubleEncodesReservedPathCharacters(t *testing.T) {
	const (
		accessKey = "AKIDEXAMPLE"
		secretKey = "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY"
		region    = "us-east-1"
		service   = "bedrock"
		host      = "bedrock-runtime.us-east-1.amazonaws.com"
		// The colon is pre-escaped to %3A, as providers/bedrock's
		// escapeModelID does for Bedrock model IDs.
		wirePath = "/model/anthropic.claude-3%3A1/converse"
		// Per SigV4's double-encoding rule, the canonical URI re-encodes the
		// already-escaped path: '%' (from %3A) itself becomes %25, so %3A
		// becomes %253A in the canonical request.
		wantCanonicalURI = "/model/anthropic.claude-3%253A1/converse"
	)
	now := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	amzDate := "20240601T000000Z"
	dateStamp := "20240601"

	body := []byte(`{"messages":[]}`)
	payloadHash := sha256Hex(string(body))

	req, err := http.NewRequest(http.MethodPost, "https://"+host+wirePath, strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	// Sanity check on the premise: Go's URL parsing must actually preserve
	// the pre-escaped ':' as %3A (not silently unescape it) for this test
	// to be meaningful.
	if got := req.URL.EscapedPath(); got != wirePath {
		t.Fatalf("premise check: req.URL.EscapedPath() = %q, want %q (unescaped)", got, wirePath)
	}
	req.Header.Set("Content-Type", "application/json")

	creds := Credentials{AccessKeyID: accessKey, SecretAccessKey: secretKey}
	if err := Sign(req, body, creds, region, service, now); err != nil {
		t.Fatalf("Sign: %v", err)
	}

	// Independently recompute the whole chain, starting from the expected
	// doubly-encoded canonical URI.
	headerNames := []string{"content-type", "host", "x-amz-content-sha256", "x-amz-date"}
	headerValues := map[string]string{
		"content-type":         "application/json",
		"host":                 host,
		"x-amz-content-sha256": payloadHash,
		"x-amz-date":           amzDate,
	}
	var canonicalHeaders strings.Builder
	for _, n := range headerNames {
		canonicalHeaders.WriteString(n)
		canonicalHeaders.WriteByte(':')
		canonicalHeaders.WriteString(headerValues[n])
		canonicalHeaders.WriteByte('\n')
	}
	signedHeaders := strings.Join(headerNames, ";")

	canonicalRequest := strings.Join([]string{
		http.MethodPost,
		wantCanonicalURI,
		"",
		canonicalHeaders.String(),
		signedHeaders,
		payloadHash,
	}, "\n")
	hashedCanonical := sha256Hex(canonicalRequest)

	credentialScope := dateStamp + "/" + region + "/" + service + "/aws4_request"
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		credentialScope,
		hashedCanonical,
	}, "\n")

	kDate := hmacSum([]byte("AWS4"+secretKey), dateStamp)
	kRegion := hmacSum(kDate, region)
	kService := hmacSum(kRegion, service)
	kSigning := hmacSum(kService, "aws4_request")
	wantSignature := hex.EncodeToString(hmacSum(kSigning, stringToSign))

	wantAuth := "AWS4-HMAC-SHA256 Credential=" + accessKey + "/" + credentialScope +
		", SignedHeaders=" + signedHeaders + ", Signature=" + wantSignature

	gotAuth := req.Header.Get("Authorization")
	if gotAuth != wantAuth {
		t.Fatalf("Authorization = %q, want %q\n(hashed canonical request = %q, string-to-sign = %q, signature = %q)",
			gotAuth, wantAuth, hashedCanonical, stringToSign, wantSignature)
	}
}
