package web

import "testing"

func testTools() []map[string]any {
	return []map[string]any{
		{"type": "function", "function": map[string]any{"name": "get_weather", "parameters": map[string]any{"type": "object", "required": []any{"city"}, "properties": map[string]any{"city": map[string]any{"type": "string"}}}}},
		{"type": "function", "function": map[string]any{"name": "get_time", "parameters": map[string]any{"type": "object", "properties": map[string]any{"city": map[string]any{"type": "string"}}}}},
	}
}

func TestSchemaValidNestedAndEnum(t *testing.T) {
	fn := map[string]any{"parameters": map[string]any{"type": "object", "required": []any{"query"}, "additionalProperties": false, "properties": map[string]any{"query": map[string]any{"type": "object", "required": []any{"city"}, "properties": map[string]any{"city": map[string]any{"type": "string"}, "unit": map[string]any{"type": "string", "enum": []any{"c", "f"}}}}, "days": map[string]any{"type": "integer"}}}}
	if err := schemaValid(map[string]any{"query": map[string]any{"city": "Paris", "unit": "c"}, "days": float64(2)}, fn); err != nil {
		t.Fatal(err)
	}
	if err := schemaValid(map[string]any{"query": map[string]any{"city": "Paris", "unit": "kelvin"}}, fn); err == nil {
		t.Fatal("expected enum rejection")
	}
	if err := schemaValid(map[string]any{"query": map[string]any{"city": "Paris"}, "extra": true}, fn); err == nil {
		t.Fatal("expected additional property rejection")
	}
	if err := schemaValid(map[string]any{"query": map[string]any{"city": "Paris"}, "days": 1.5}, fn); err == nil {
		t.Fatal("expected integer rejection")
	}
}
