package chathub

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type attachmentRoundTripFunc func(*http.Request) (*http.Response, error)

func (f attachmentRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestUploadAttachmentsReturnsSanitizerFailure(t *testing.T) {
	client := &Client{HTTPClient: &http.Client{Transport: attachmentRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusBadRequest, Status: "400 Bad Request", Body: io.NopCloser(strings.NewReader(`{"error":"fileSanitizer rejected image"}`)), Header: make(http.Header)}, nil
	})}}
	attachments := []Attachment{{Type: "image", MimeType: "image/png", URL: "data:image/png;base64,aGVsbG8="}}
	err := client.uploadAttachments(context.Background(), Account{AccessToken: "token"}, "conversation", attachments)
	if err == nil || !strings.Contains(err.Error(), "fileSanitizer") {
		t.Fatalf("upload error=%v", err)
	}
	if attachments[0].DocID != "" {
		t.Fatalf("failed upload unexpectedly produced doc id: %#v", attachments[0])
	}
}

func TestChatPayloadDoesNotRepeatUploadedImageData(t *testing.T) {
	marker := "BASE64_SHOULD_NOT_REACH_CHATHUB"
	payload := chatPayload(Request{Text: "inspect", Attachments: []Attachment{{Type: "image", MimeType: "image/png", URL: "data:image/png;base64," + marker, DocID: "doc-1", FileType: "png", Name: "image.png"}}}, "request", true)
	if strings.Contains(payload, marker) || strings.Contains(payload, "imageBase64") {
		t.Fatalf("uploaded image data leaked into ChatHub payload: %s", payload)
	}
	if !strings.Contains(payload, `"id":"doc-1"`) || !strings.Contains(payload, `"messageAnnotationType":"ImageFile"`) {
		t.Fatalf("uploaded image annotation missing: %s", payload)
	}
}

func TestUploadAttachmentsRejectsOversizedDataBeforeHTTP(t *testing.T) {
	called := false
	client := &Client{HTTPClient: &http.Client{Transport: attachmentRoundTripFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return nil, nil
	})}}
	encoded := strings.Repeat("A", ((maxAttachmentMiB<<20)*4/3)+8)
	err := client.uploadAttachments(context.Background(), Account{}, "conversation", []Attachment{{Type: "image", URL: "data:image/png;base64," + encoded}})
	if err == nil || !strings.Contains(err.Error(), "exceeds") || called {
		t.Fatalf("err=%v called=%t", err, called)
	}
}
