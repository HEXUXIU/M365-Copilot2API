from typing import Dict, List, Callable, Any, Optional
import json, inspect


class Tool:
    def __init__(self, name: str, description: str, func: Callable,
                 parameters: Optional[Dict] = None):
        self.name = name
        self.description = description
        self.func = func
        self.parameters = parameters or self._extract_params(func)

    def _extract_params(self, func: Callable) -> Dict:
        sig = inspect.signature(func)
        params = {}
        required = []
        for param_name, param in sig.parameters.items():
            if param_name == "self":
                continue
            param_type = "string"
            if param.annotation != inspect.Parameter.empty:
                if param.annotation == int:
                    param_type = "integer"
                elif param.annotation == bool:
                    param_type = "boolean"
            params[param_name] = {"type": param_type}
            if param.default == inspect.Parameter.empty:
                required.append(param_name)
        return {"type": "object", "properties": params, "required": required}

    def to_dict(self) -> Dict:
        return {
            "name": self.name,
            "description": self.description,
            "parameters": self.parameters,
        }

    def execute(self, **kwargs) -> Any:
        return self.func(**kwargs)


class MCPProvider:
    def __init__(self):
        self.tools: Dict[str, Tool] = {}

    def register(self, name: str = None, description: str = None):
        def decorator(func: Callable) -> Callable:
            tool_name = name or func.__name__
            tool_desc = description or func.__doc__ or tool_name
            self.tools[tool_name] = Tool(tool_name, tool_desc, func)
            return func
        return decorator

    def register_tool(self, tool: Tool):
        self.tools[tool.name] = tool

    def get_tools(self) -> List[Dict]:
        return [tool.to_dict() for tool in self.tools.values()]

    def execute_tool(self, name: str, arguments: Dict = None) -> Any:
        if name not in self.tools:
            raise ValueError(f"Tool '{name}' not found")
        return self.tools[name].execute(**(arguments or {}))

    def get_tool_prompt(self) -> str:
        if not self.tools:
            return ""
        lines = []
        for tool in self.tools.values():
            params_desc = []
            props = tool.parameters.get("properties", {})
            required = tool.parameters.get("required", [])
            for pname, pdef in props.items():
                mark = " (required)" if pname in required else ""
                params_desc.append(f"  - {pname}: {pdef['type']}{mark}")
            lines.append(
                f"\n工具: {tool.name}\n"
                f"描述: {tool.description}\n"
                f"参数:\n" + "\n".join(params_desc)
            )
        return (
            "你可以调用以下工具来帮助回答问题。"
            "当需要使用工具时，请输出 JSON 格式的工具调用：\n"
            "```json\n"
            '{"function": {"name": "工具名", "arguments": {...}}}\n'
            "```\n"
            "\n可用工具：" + "\n".join(lines)
        )


provider = MCPProvider()
