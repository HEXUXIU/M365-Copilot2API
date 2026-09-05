package chathub

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type attachmentRoundTripFunc func(*http.Request) (*http.Response, error)

func (f attachmentRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestUploadAttachmentsFallsBackInlineOnSanitizerFailure(t *testing.T) {
	client := &Client{HTTPClient: &http.Client{Transport: attachmentRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusBadRequest, Status: "400 Bad Request", Body: io.NopCloser(strings.NewReader(`{"error":"fileSanitizer rejected image"}`)), Header: make(http.Header)}, nil
	})}}
	attachments := []Attachment{{Type: "image", MimeType: "image/png", URL: "data:image/png;base64,aGVsbG8="}}
	err := client.uploadAttachments(context.Background(), Account{AccessToken: "token"}, "conversation", attachments)
	if err != nil {
		t.Fatalf("upload error=%v", err)
	}
	if attachments[0].DocID != "" {
		t.Fatalf("failed upload unexpectedly produced doc id: %#v", attachments[0])
	}
	payload := chatPayload(Request{Text: "inspect", Attachments: attachments}, "request", true)
	if !strings.Contains(payload, `"imageBase64":"aGVsbG8="`) {
		t.Fatalf("inline image fallback missing: %s", payload)
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

func TestUploadAttachmentsCanonicalizesWildcardImageMIME(t *testing.T) {
	const png = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
	client := &Client{HTTPClient: &http.Client{Transport: attachmentRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		if got := form.Get("FileBase64"); !strings.HasPrefix(got, "data:image/png;base64,") {
			t.Fatalf("FileBase64 MIME was not canonicalized: %.40q", got)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"docId":"doc-1","fileName":"image.png","fileType":"png","result":{"value":"Success"}}`)), Header: make(http.Header)}, nil
	})}}
	attachments := []Attachment{{Type: "image", MimeType: "image/*", URL: "data:image/*;base64," + png}}
	if err := client.uploadAttachments(context.Background(), Account{AccessToken: "token"}, "conversation", attachments); err != nil {
		t.Fatal(err)
	}
	if attachments[0].MimeType != "image/png" || attachments[0].DocID != "doc-1" {
		t.Fatalf("canonical attachment=%#v", attachments[0])
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

type attachmentTimeoutError struct{}

func (attachmentTimeoutError) Error() string   { return "TLS handshake timeout" }
func (attachmentTimeoutError) Timeout() bool   { return true }
func (attachmentTimeoutError) Temporary() bool { return true }

func TestUploadAttachmentsRetriesTransientUpload503(t *testing.T) {
	attempts := 0
	client := &Client{HTTPClient: &http.Client{Transport: attachmentRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method %s", r.Method)
		}
		attempts++
		if attempts == 1 {
			return &http.Response{StatusCode: http.StatusServiceUnavailable, Status: "503 Service Unavailable", Body: io.NopCloser(strings.NewReader("busy")), Header: make(http.Header)}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(strings.NewReader(`{"docId":"doc-retry","fileName":"image.png","fileType":"png","result":{"value":"Success"}}`)), Header: make(http.Header)}, nil
	})}}
	attachments := []Attachment{{Type: "image", MimeType: "image/png", URL: "data:image/png;base64,aGVsbG8="}}
	if err := client.uploadAttachments(context.Background(), Account{AccessToken: "token"}, "conversation", attachments); err != nil {
		t.Fatalf("upload error=%v", err)
	}
	if attempts != 2 || attachments[0].DocID != "doc-retry" {
		t.Fatalf("attempts=%d attachment=%#v", attempts, attachments[0])
	}
}

func TestUploadAttachmentsFallsBackInlineAfterTransientUploadRetries(t *testing.T) {
	attempts := 0
	client := &Client{HTTPClient: &http.Client{Transport: attachmentRoundTripFunc(func(*http.Request) (*http.Response, error) {
		attempts++
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Status:     "503 Service Unavailable",
			Body:       io.NopCloser(strings.NewReader("busy")),
			Header:     make(http.Header),
		}, nil
	})}}
	attachments := []Attachment{{Type: "image", MimeType: "image/png", URL: "data:image/png;base64,aGVsbG8="}}
	if err := client.uploadAttachments(context.Background(), Account{AccessToken: "token"}, "conversation", attachments); err != nil {
		t.Fatalf("upload error=%v", err)
	}
	if attempts != attachmentTransientRetries+1 || attachments[0].DocID != "" {
		t.Fatalf("attempts=%d attachment=%#v", attempts, attachments[0])
	}
	payload := chatPayload(Request{Text: "inspect", Attachments: attachments}, "request", true)
	if !strings.Contains(payload, `"imageBase64":"aGVsbG8="`) {
		t.Fatalf("inline image fallback missing after transient retries: %s", payload)
	}
}

func TestUploadAttachmentsRetriesTransientDownloadTimeout(t *testing.T) {
	attempts := 0
	client := &Client{HTTPClient: &http.Client{Transport: attachmentRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet {
			attempts++
			if attempts == 1 {
				return nil, &url.Error{Op: "Get", URL: r.URL.String(), Err: attachmentTimeoutError{}}
			}
			return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(strings.NewReader("hello")), Header: http.Header{"Content-Type": []string{"image/png"}}}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(strings.NewReader(`{"docId":"doc-download-retry","fileName":"image.png","fileType":"png","result":{"value":"Success"}}`)), Header: make(http.Header)}, nil
	})}}
	attachments := []Attachment{{Type: "image", URL: "https://1.1.1.1/image.png"}}
	if err := client.uploadAttachments(context.Background(), Account{AccessToken: "token"}, "conversation", attachments); err != nil {
		t.Fatalf("upload error=%v", err)
	}
	if attempts != 2 || attachments[0].DocID != "doc-download-retry" {
		t.Fatalf("download attempts=%d attachment=%#v", attempts, attachments[0])
	}
}
