package domain

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

type RichTextAttrs struct {
	Level        *int32  `json:"level,omitempty"`
	Start        *int32  `json:"start,omitempty"`
	Checked      *bool   `json:"checked,omitempty"`
	Language     *string `json:"language,omitempty"`
	Href         *string `json:"href,omitempty"`
	AttachmentID *string `json:"attachmentId,omitempty"`
	Alt          *string `json:"alt,omitempty"`
	Title        *string `json:"title,omitempty"`
	TextAlign    *string `json:"textAlign,omitempty"`
	Colspan      *int32  `json:"colspan,omitempty"`
	Rowspan      *int32  `json:"rowspan,omitempty"`
	Colwidth     []int32 `json:"colwidth,omitempty"`
}

type RichTextMark struct {
	Type  string         `json:"type"`
	Attrs *RichTextAttrs `json:"attrs,omitempty"`
}

type RichTextNode struct {
	Type    string          `json:"type"`
	Attrs   *RichTextAttrs  `json:"attrs,omitempty"`
	Content []*RichTextNode `json:"content,omitempty"`
	Text    *string         `json:"text,omitempty"`
	Marks   []RichTextMark  `json:"marks,omitempty"`
}

type RichTextDocument struct {
	Type    string          `json:"type"`
	Content []*RichTextNode `json:"content"`
}

var allowedNodeTypes = map[string]struct{}{
	"paragraph": {}, "heading": {}, "bulletList": {}, "orderedList": {}, "listItem": {},
	"taskList": {}, "taskItem": {}, "blockquote": {}, "codeBlock": {}, "horizontalRule": {},
	"hardBreak": {}, "text": {}, "image": {}, "attachment": {}, "table": {}, "tableRow": {},
	"tableHeader": {}, "tableCell": {},
}

var allowedMarkTypes = map[string]struct{}{
	"bold": {}, "italic": {}, "strike": {}, "underline": {}, "code": {}, "link": {},
}

func (d RichTextDocument) Validate() error {
	if d.Type != "doc" || d.Content == nil {
		return &ValidationError{Field: "content", Reason: "must be a ProseMirror doc with a content array"}
	}
	count := 0
	for _, node := range d.Content {
		if err := validateRichTextNode(node, 1, &count); err != nil {
			return err
		}
	}
	return nil
}

func validateRichTextNode(node *RichTextNode, depth int, count *int) error {
	if node == nil {
		return &ValidationError{Field: "content", Reason: "contains a null node"}
	}
	if depth > 64 {
		return &ValidationError{Field: "content", Reason: "exceeds the maximum nesting depth"}
	}
	*count++
	if *count > 100000 {
		return &ValidationError{Field: "content", Reason: "contains too many nodes"}
	}
	if _, ok := allowedNodeTypes[node.Type]; !ok {
		return &ValidationError{Field: "content", Reason: fmt.Sprintf("contains unsupported node type %q", node.Type)}
	}
	if node.Type == "text" {
		if node.Text == nil || node.Content != nil || node.Attrs != nil {
			return &ValidationError{Field: "content", Reason: "contains an invalid text node"}
		}
	} else if node.Text != nil {
		return &ValidationError{Field: "content", Reason: "non-text nodes must not contain text"}
	}
	for _, mark := range node.Marks {
		if _, ok := allowedMarkTypes[mark.Type]; !ok {
			return &ValidationError{Field: "content", Reason: fmt.Sprintf("contains unsupported mark type %q", mark.Type)}
		}
		if mark.Type == "link" {
			if mark.Attrs == nil || mark.Attrs.Href == nil || validateLink(*mark.Attrs.Href) != nil {
				return &ValidationError{Field: "content", Reason: "contains an invalid link mark"}
			}
		}
	}
	if node.Attrs != nil {
		if err := validateRichTextAttrs(node.Type, node.Attrs); err != nil {
			return err
		}
	}
	for _, child := range node.Content {
		if err := validateRichTextNode(child, depth+1, count); err != nil {
			return err
		}
	}
	return nil
}

func validateRichTextAttrs(nodeType string, attrs *RichTextAttrs) error {
	var joined error
	if attrs.Level != nil && (*attrs.Level < 1 || *attrs.Level > 6) {
		joined = errors.Join(joined, &ValidationError{Field: "content", Reason: "heading level must be between 1 and 6"})
	}
	if attrs.Start != nil && *attrs.Start < 1 {
		joined = errors.Join(joined, &ValidationError{Field: "content", Reason: "ordered list start must be positive"})
	}
	if attrs.AttachmentID != nil && ValidateID("content.attachmentId", *attrs.AttachmentID) != nil {
		joined = errors.Join(joined, &ValidationError{Field: "content", Reason: "contains an invalid attachment ID"})
	}
	if (nodeType == "image" || nodeType == "attachment") && attrs.AttachmentID == nil {
		joined = errors.Join(joined, &ValidationError{Field: "content", Reason: "attachment nodes require attachmentId"})
	}
	if attrs.TextAlign != nil {
		switch *attrs.TextAlign {
		case "left", "center", "right", "justify":
		default:
			joined = errors.Join(joined, &ValidationError{Field: "content", Reason: "contains an invalid text alignment"})
		}
	}
	if attrs.Colspan != nil && (*attrs.Colspan < 1 || *attrs.Colspan > 100) {
		joined = errors.Join(joined, &ValidationError{Field: "content", Reason: "table colspan is out of range"})
	}
	if attrs.Rowspan != nil && (*attrs.Rowspan < 1 || *attrs.Rowspan > 100) {
		joined = errors.Join(joined, &ValidationError{Field: "content", Reason: "table rowspan is out of range"})
	}
	return joined
}

func validateLink(value string) error {
	if len(value) > 2048 || strings.ContainsAny(value, "\r\n") {
		return errors.New("invalid link")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed == nil {
		return errors.New("invalid link")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "mailto":
		return nil
	default:
		return errors.New("invalid link scheme")
	}
}
