package iconlibrary

import (
	"encoding/json"
	"io"
	"net/url"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
)

// HeadReferences parses native customize_head without executing HTML or scripts.
// Removed selections fall back to default; malformed configuration fails closed.
func HeadReferences(head string) (map[string]bool, error) {
	bad := func() (map[string]bool, error) { return nil, fault(502) }
	if len(head) > 1024*1024 || !utf8.ValidString(head) {
		return bad()
	}
	z := html.NewTokenizer(strings.NewReader(head))
	contents := []string{}
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			if z.Err() != io.EOF {
				return bad()
			}
			break
		}
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			continue
		}
		token := z.Token()
		if token.Data != "meta" {
			continue
		}
		ids, values := []string{}, []string{}
		for _, attr := range token.Attr {
			if attr.Key == "id" {
				ids = append(ids, attr.Val)
			}
			if attr.Key == "content" {
				values = append(values, attr.Val)
			}
		}
		matches := false
		for _, id := range ids {
			if id == "openlist-icon-config" {
				matches = true
			}
		}
		if matches {
			if len(ids) != 1 || len(values) != 1 {
				return bad()
			}
			contents = append(contents, values[0])
		}
	}
	if len(contents) > 1 {
		return bad()
	}
	refs := map[string]bool{}
	if len(contents) == 1 {
		decoded, err := url.PathUnescape(contents[0])
		if err != nil || !utf8.ValidString(decoded) {
			return bad()
		}
		decoder := json.NewDecoder(strings.NewReader(decoded))
		decoder.UseNumber()
		raw, err := uniqueValue(decoder, 0)
		if err != nil {
			return bad()
		}
		if _, err = decoder.Token(); err != io.EOF {
			return bad()
		}
		obj, ok := raw.(map[string]any)
		if !ok || obj["version"] != json.Number("1") {
			return bad()
		}
		selections, ok := obj["selections"].(map[string]any)
		if !ok || len(selections) > 512 {
			return bad()
		}
		for _, raw := range selections {
			value, ok := raw.(string)
			if !ok {
				return bad()
			}
			if validAsset(value) {
				refs[value] = true
			}
		}
	} else if strings.Contains(head, "OPENLIST-ICON-THEME") || strings.Contains(head, "openlist-icon-config") {
		return bad()
	}
	return refs, nil
}

func uniqueValue(d *json.Decoder, depth int) (any, error) {
	if depth > 64 {
		return nil, fault(502)
	}
	t, err := d.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return t, nil
	}
	switch delim {
	case '{':
		obj := map[string]any{}
		for d.More() {
			k, err := d.Token()
			if err != nil {
				return nil, err
			}
			key, ok := k.(string)
			if !ok {
				return nil, fault(502)
			}
			if _, exists := obj[key]; exists {
				return nil, fault(502)
			}
			v, err := uniqueValue(d, depth+1)
			if err != nil {
				return nil, err
			}
			obj[key] = v
		}
		end, err := d.Token()
		if err != nil || end != json.Delim('}') {
			return nil, fault(502)
		}
		return obj, nil
	case '[':
		a := []any{}
		for d.More() {
			v, err := uniqueValue(d, depth+1)
			if err != nil {
				return nil, err
			}
			a = append(a, v)
		}
		end, err := d.Token()
		if err != nil || end != json.Delim(']') {
			return nil, fault(502)
		}
		return a, nil
	default:
		return nil, fault(502)
	}
}
