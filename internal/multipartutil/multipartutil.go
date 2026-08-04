// Package multipartutil holds a small guard shared by every provider that
// builds a multipart/form-data request from caller-controlled strings
// (file MediaType/Filename, ProviderOptions keys/values, and similar
// caller-derived field names or values).
//
// Go's mime/multipart.Writer writes MIME headers verbatim: CreatePart takes
// a textproto.MIMEHeader and writes its values into the wire format with no
// CR/LF validation, and WriteField/CreateFormField only escape a
// backslash-or-double-quote via escapeQuotes — they do not escape CR or LF
// in the field name. That's unlike net/http, whose Header.Set-then-write
// path validates header values before writing them to the wire. A
// caller-supplied MediaType, filename, or field name/value containing
// "\r\n" can therefore inject extra CRLF-terminated header lines or forge
// an entirely new multipart part, smuggling extra form fields past the
// application's intended request shape.
package multipartutil

import (
	"fmt"
	"strings"
)

// ValidField reports an error if s contains a carriage return, line feed,
// or double-quote — the characters that let a value break out of a
// multipart header or field and forge parts, since multipart.Writer does
// not validate them. kind names what s is for the error message, e.g.
// "media type" or "field name".
func ValidField(kind, s string) error {
	if strings.ContainsAny(s, "\r\n\"") {
		return fmt.Errorf("invalid %s: contains CR, LF, or quote", kind)
	}
	return nil
}
