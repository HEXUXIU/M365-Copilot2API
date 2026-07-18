package chathub

type Attachment struct {
	Type     string `json:"type"`
	URL      string `json:"url,omitempty"`
	Name     string `json:"name,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}
