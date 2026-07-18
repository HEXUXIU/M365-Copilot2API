package chathub

import "encoding/json"

type Event struct {
	Type       int             `json:"type,omitempty"`
	Target     string          `json:"target,omitempty"`
	Invocation string          `json:"invocationId,omitempty"`
	Kind       string          `json:"kind"`
	Arguments  json.RawMessage `json:"arguments,omitempty"`
	Item       json.RawMessage `json:"item,omitempty"`
	Error      json.RawMessage `json:"error,omitempty"`
	Raw        json.RawMessage `json:"raw"`
}

// SemanticEvent exposes tool-like M365 progress without discarding the native frame.
type SemanticEvent struct {
	Kind        string   `json:"kind"`
	ContentType string   `json:"contentType,omitempty"`
	MessageType string   `json:"messageType,omitempty"`
	Text        string   `json:"text,omitempty"`
	Queries     []string `json:"queries,omitempty"`
	HiddenText  string   `json:"hiddenText,omitempty"`
	Raw         Event    `json:"raw"`
}

func normalize(raw json.RawMessage) Event {
	var x struct {
		Type       int             `json:"type"`
		Target     string          `json:"target"`
		Invocation string          `json:"invocationId"`
		Arguments  json.RawMessage `json:"arguments"`
		Item       json.RawMessage `json:"item"`
		Error      json.RawMessage `json:"error"`
	}
	_ = json.Unmarshal(raw, &x)
	kind := "unknown"
	switch {
	case x.Type == 6:
		kind = "ping"
	case x.Type == 1 && x.Target == "update":
		kind = "update"
	case x.Type == 2:
		kind = "result"
	case x.Type == 3 && len(x.Error) > 0:
		kind = "error"
	case x.Type == 3:
		kind = "complete"
	case x.Target != "":
		kind = "target"
	}
	return Event{Type: x.Type, Target: x.Target, Invocation: x.Invocation, Kind: kind, Arguments: x.Arguments, Item: x.Item, Error: x.Error, Raw: append(json.RawMessage(nil), raw...)}
}

func NormalizeEvents(raw []json.RawMessage) []Event {
	out := make([]Event, 0, len(raw))
	for _, r := range raw {
		out = append(out, normalize(r))
	}
	return out
}

func SemanticEvents(raw []json.RawMessage) []SemanticEvent {
	var out []SemanticEvent
	for _, r := range raw {
		e := normalize(r)
		if e.Kind != "update" {
			continue
		}
		var a []struct {
			Messages []struct {
				Text          string   `json:"text"`
				ContentType   string   `json:"contentType"`
				MessageType   string   `json:"messageType"`
				SearchQueries []string `json:"searchQueries"`
				HiddenText    string   `json:"hiddenText"`
			} `json:"messages"`
		}
		if json.Unmarshal(e.Arguments, &a) != nil {
			continue
		}
		for _, arg := range a {
			for _, m := range arg.Messages {
				kind := "message"
				switch m.ContentType {
				case "SearchResults":
					kind = "search.progress"
				case "Code":
					kind = "code.progress"
				}
				if m.MessageType == "Progress" && kind == "message" {
					kind = "tool.progress"
				}
				out = append(out, SemanticEvent{Kind: kind, ContentType: m.ContentType, MessageType: m.MessageType, Text: m.Text, Queries: m.SearchQueries, HiddenText: m.HiddenText, Raw: e})
			}
		}
	}
	return out
}
