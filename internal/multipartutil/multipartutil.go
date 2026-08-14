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
	"io"
	"mime/multipart"
	"net/textproto"
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

// CreateFilePart adds a file part named field to mw, using filename and,
// when mediaType is non-empty, a Content-Type header carrying it. An empty
// mediaType falls back to mw.CreateFormFile, which (per net/http's sniffing
// convention) always writes "application/octet-stream".
func CreateFilePart(mw *multipart.Writer, field, filename, mediaType string) (io.Writer, error) {
	if err := ValidField("filename", filename); err != nil {
		return nil, err
	}
	if err := ValidField("media type", mediaType); err != nil {
		return nil, err
	}
	if mediaType == "" {
		return mw.CreateFormFile(field, filename)
	}
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, field, filename))
	h.Set("Content-Type", mediaType)
	return mw.CreatePart(h)
}

// ApplyProviderOptionsForm writes providerOptions[name] (when it is a
// non-empty map[string]any) as extra multipart form fields, each value
// stringified with fmt.Sprint. Used for multipart-body requests, where
// there's no single JSON object to merge into.
func ApplyProviderOptionsForm(mw *multipart.Writer, providerOptions map[string]any, name string) error {
	opts, _ := providerOptions[name].(map[string]any)
	for k, v := range opts {
		if err := ValidField("provider option field name", k); err != nil {
			return err
		}
		sv := fmt.Sprint(v)
		if err := ValidField("provider option field value", sv); err != nil {
			return err
		}
		if err := mw.WriteField(k, sv); err != nil {
			return err
		}
	}
	return nil
}
