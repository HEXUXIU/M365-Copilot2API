package web

import "m365-copilot2api/internal/chathub"

func imageProgressStageFromEvent(event chathub.StreamEvent) string {
	if event.Kind != "progress" && event.Kind != "reasoning" {
		return ""
	}
	if event.MessageType != "Progress" {
		return ""
	}
	if event.ContentType != "GraphicArt" && event.ContentType != "ImageGeneration" && event.ContentType != "image" {
		return ""
	}
	if event.ContentOrigin != "ImageGeneration" && event.ContentOrigin != "GraphicArt" {
		return ""
	}
	return "generating"
}
