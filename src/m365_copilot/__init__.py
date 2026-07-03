import os

def _load_dotenv():
    """Load .env file into environment variables (no external dependency)."""
    env_path = os.path.join(os.getcwd(), ".env")
    if not os.path.exists(env_path):
        return
    with open(env_path, encoding="utf-8") as f:
        for line in f:
            line = line.strip()
            if not line or line.startswith("#") or "=" not in line:
                continue
            key, _, value = line.partition("=")
            key = key.strip()
            value = value.strip()
            if key and key not in os.environ:
                os.environ[key] = value

_load_dotenv()

from .auth import TokenManager, TokenRefreshError
from .client import M365Client
from .models import MODELS, lookup_model
from .payload import build_url, build_payload, build_conversation_payload

__version__ = "0.6.0"
