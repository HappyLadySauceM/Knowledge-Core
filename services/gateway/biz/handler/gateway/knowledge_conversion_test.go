package gateway

import (
	"testing"

	knowledgev1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/knowledge"
)

func TestToDocumentDataAcceptsAnonymousNoneAccess(t *testing.T) {
	document := completeDocument()
	document.Access = "none"
	document.Published = true
	document.PublicationStatus = "published"

	data, err := toDocumentData(document)
	if err != nil {
		t.Fatalf("toDocumentData() error = %v", err)
	}
	if data.Access != "none" || !data.Published || data.PublicationStatus != "published" {
		t.Fatalf("document = %#v", data)
	}
}

func TestToDocumentDataRejectsUnknownAccess(t *testing.T) {
	document := completeDocument()
	document.Access = "shared"
	if _, err := toDocumentData(document); err == nil {
		t.Fatal("toDocumentData() accepted unknown access")
	}
}

func TestToDocumentPageDataMapsAnonymousPublishedList(t *testing.T) {
	document := completeDocument()
	document.Access = "none"
	document.Published = true
	document.PublicationStatus = "published"
	page := &knowledgev1.DocumentPage{
		Items: []*knowledgev1.Document{document},
		Page:  &knowledgev1.PageInfo{HasMore: false},
	}

	data, err := toDocumentPageData(page)
	if err != nil {
		t.Fatalf("toDocumentPageData() error = %v", err)
	}
	if len(data.Items) != 1 || data.Items[0].Access != "none" || data.Page.HasMore {
		t.Fatalf("page = %#v", data)
	}
}
