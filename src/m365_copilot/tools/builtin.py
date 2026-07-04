import datetime, random, ast, operator

from .provider import provider


def _safe_eval(expr):
    allowed = {
        ast.Add: operator.add, ast.Sub: operator.sub,
        ast.Mult: operator.mul, ast.Div: operator.truediv,
        ast.FloorDiv: operator.floordiv, ast.Mod: operator.mod,
        ast.Pow: operator.pow, ast.USub: operator.neg,
        ast.UAdd: operator.pos,
    }
    allowed_nodes = (ast.Expression, ast.Constant, ast.BinOp, ast.UnaryOp,
                     ast.Add, ast.Sub, ast.Mult, ast.Div, ast.FloorDiv,
                     ast.Mod, ast.Pow, ast.USub, ast.UAdd)

    tree = ast.parse(expr, mode="eval")
    for node in ast.walk(tree):
        if type(node) not in allowed_nodes:
            raise ValueError(f"不允许的操作: {type(node).__name__}")
        if isinstance(node, ast.Constant) and not isinstance(node.value, (int, float)):
            raise ValueError(f"不允许的常量: {type(node.value).__name__}")

    compiled = compile(tree, "<safe>", "eval")
    return eval(compiled, {"__builtins__": {}}, allowed)


@provider.register(name="get_current_time", description="获取当前日期和时间")
def get_current_time() -> str:
    return datetime.datetime.now().strftime("%Y-%m-%d %H:%M:%S")


@provider.register(name="calculate", description="执行数学计算")
def calculate(expression: str) -> str:
    allowed = set("0123456789+-*/(). ")
    if not all(c in allowed for c in expression):
        return "错误：表达式包含不允许的字符"
    try:
        result = _safe_eval(expression)
        return f"{expression} = {result}"
    except Exception as e:
        return f"计算错误: {e}"


@provider.register(name="roll_dice", description="掷骰子")
def roll_dice(sides: int = 6) -> str:
    result = random.randint(1, sides)
    return f"Rolled {result} (d{sides})"
