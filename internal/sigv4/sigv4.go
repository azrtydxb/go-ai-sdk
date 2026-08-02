// Package sigv4 implements AWS Signature Version 4 request signing
// (https://docs.aws.amazon.com/general/latest/gr/signature-version-4.html),
// sufficient for signing requests to AWS service APIs such as Bedrock
// Runtime.
package sigv4

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Credentials are the AWS credentials used to sign a request.
type Credentials struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string // optional
}

const (
	algorithm   = "AWS4-HMAC-SHA256"
	terminator  = "aws4_request"
	amzDateFmt  = "20060102T150405Z"
	dateOnlyFmt = "20060102"
)

// Sign adds X-Amz-Date, X-Amz-Content-Sha256, an optional
// X-Amz-Security-Token, and Authorization (AWS4-HMAC-SHA256) headers to req,
// signing it for the given AWS region and service. body must be the exact
// bytes that will be sent as the request body (used for the payload hash).
// now is injected for testability; callers should pass time.Now().UTC().
func Sign(req *http.Request, body []byte, creds Credentials, region, service string, now time.Time) error {
	now = now.UTC()
	amzDate := now.Format(amzDateFmt)
	dateStamp := now.Format(dateOnlyFmt)

	payloadHash := hexSHA256(body)

	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	if creds.SessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", creds.SessionToken)
	}

	host := req.Host
	if host == "" {
		host = req.URL.Host
	}

	// Determine which headers to sign: host, all x-amz-* headers present on
	// the request, and content-type if present.
	headerValues := map[string]string{"host": host}
	for name, vals := range req.Header {
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, "x-amz-") || lower == "content-type" {
			// SigV4 joins duplicate header values with "," in the order
			// they appear on the wire (this matches aws-sdk-go-v2's and
			// botocore's canonicalization) — NOT sorted. Each individual
			// value is still trimmed/collapsed per canonicalHeaderValue
			// before joining.
			canonVals := make([]string, len(vals))
			for i, v := range vals {
				canonVals[i] = canonicalHeaderValue(v)
			}
			headerValues[lower] = strings.Join(canonVals, ",")
		}
	}

	signedHeaderNames := make([]string, 0, len(headerValues))
	for name := range headerValues {
		signedHeaderNames = append(signedHeaderNames, name)
	}
	sort.Strings(signedHeaderNames)

	var canonicalHeaders strings.Builder
	for _, name := range signedHeaderNames {
		canonicalHeaders.WriteString(name)
		canonicalHeaders.WriteByte(':')
		canonicalHeaders.WriteString(canonicalHeaderValue(headerValues[name]))
		canonicalHeaders.WriteByte('\n')
	}
	signedHeaders := strings.Join(signedHeaderNames, ";")

	canonicalURI := canonicalURIPath(req.URL.EscapedPath())
	canonicalQuery := canonicalQueryString(req.URL.Query())

	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalURI,
		canonicalQuery,
		canonicalHeaders.String(),
		signedHeaders,
		payloadHash,
	}, "\n")

	credentialScope := strings.Join([]string{dateStamp, region, service, terminator}, "/")
	stringToSign := strings.Join([]string{
		algorithm,
		amzDate,
		credentialScope,
		hexSHA256([]byte(canonicalRequest)),
	}, "\n")

	signingKey := deriveSigningKey(creds.SecretAccessKey, dateStamp, region, service)
	signature := hex.EncodeToString(hmacSHA256(signingKey, stringToSign))

	authHeader := algorithm + " " +
		"Credential=" + creds.AccessKeyID + "/" + credentialScope +
		", SignedHeaders=" + signedHeaders +
		", Signature=" + signature
	req.Header.Set("Authorization", authHeader)

	return nil
}

func deriveSigningKey(secret, dateStamp, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secret), dateStamp)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, service)
	return hmacSHA256(kService, terminator)
}

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

func hexSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// canonicalHeaderValue trims leading/trailing whitespace and collapses
// internal whitespace runs to a single space, per the SigV4 spec.
func canonicalHeaderValue(v string) string {
	return strings.Join(strings.Fields(v), " ")
}

// canonicalURIPath URI-encodes each segment of an already-escaped request
// path. Since the input is already percent-encoded (as it appears on the
// wire), this produces the "double URI encoding" required by the SigV4 spec
// for all services other than S3.
func canonicalURIPath(escapedPath string) string {
	if escapedPath == "" {
		return "/"
	}
	segments := strings.Split(escapedPath, "/")
	for i, seg := range segments {
		segments[i] = uriEncode(seg)
	}
	return strings.Join(segments, "/")
}

// canonicalQueryString builds the sorted, encoded canonical query string
// from parsed query parameters.
func canonicalQueryString(q url.Values) string {
	if len(q) == 0 {
		return ""
	}
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var pairs []string
	for _, k := range keys {
		vals := append([]string(nil), q[k]...)
		sort.Strings(vals)
		encodedKey := uriEncode(k)
		for _, v := range vals {
			pairs = append(pairs, encodedKey+"="+uriEncode(v))
		}
	}
	return strings.Join(pairs, "&")
}

// uriEncode percent-encodes s per RFC 3986, leaving only unreserved
// characters (A-Z a-z 0-9 - _ . ~) unescaped, as required by the SigV4
// canonical form.
func uriEncode(s string) string {
	var buf strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if isUnreserved(c) {
			buf.WriteByte(c)
		} else {
			buf.WriteByte('%')
			buf.WriteString(strings.ToUpper(hex.EncodeToString([]byte{c})))
		}
	}
	return buf.String()
}

func isUnreserved(c byte) bool {
	return (c >= 'A' && c <= 'Z') ||
		(c >= 'a' && c <= 'z') ||
		(c >= '0' && c <= '9') ||
		c == '-' || c == '_' || c == '.' || c == '~'
}
