import re, json
from typing import Optional, Dict, Tuple


def _extract_json_objects(text: str):
    """Extract all balanced JSON objects from text using bracket matching."""
    results = []
    i = 0
    while i < len(text):
        if text[i] == '{':
            depth = 1
            j = i + 1
            in_string = False
            escape = False
            while j < len(text) and depth > 0:
                c = text[j]
                if escape:
                    escape = False
                elif c == '\\':
                    escape = True
                elif c == '"' and not escape:
                    in_string = not in_string
                elif not in_string:
                    if c == '{':
                        depth += 1
                    elif c == '}':
                        depth -= 1
                j += 1
            if depth == 0:
                results.append(text[i:j])
                i = j
                continue
        i += 1
    return results


class ToolCallDetector:
    @staticmethod
    def _normalize_args(args):
        if args is None:
            return {}
        if isinstance(args, str):
            try:
                return json.loads(args)
            except json.JSONDecodeError:
                return {"raw": args}
        return args

    @staticmethod
    def _extract_name_and_args(data: dict):
        if not isinstance(data, dict):
            return None, None
        name = data.get("name") or data.get("tool")
        if not name and "function" in data and isinstance(data["function"], dict):
            name = data["function"].get("name")
            if name:
                return name, data["function"].get("arguments")
        if name:
            return name, data.get("arguments")
        return None, None

    @staticmethod
    def detect(text: str) -> Optional[Tuple[str, Dict]]:
        blocks = re.findall(r"```(?:json)?\s*\n(.*?)\n```", text, re.DOTALL)
        for block in blocks:
            try:
                data = json.loads(block.strip())
                if isinstance(data, dict):
                    name, args = ToolCallDetector._extract_name_and_args(data)
                    if name:
                        return name, ToolCallDetector._normalize_args(args)
            except json.JSONDecodeError:
                continue

        for obj_str in _extract_json_objects(text):
            if '"name"' not in obj_str and '"tool"' not in obj_str and '"function"' not in obj_str:
                continue
            try:
                data = json.loads(obj_str)
                if isinstance(data, dict):
                    name, args = ToolCallDetector._extract_name_and_args(data)
                    if name:
                        return name, ToolCallDetector._normalize_args(args)
            except json.JSONDecodeError:
                continue
        return None
