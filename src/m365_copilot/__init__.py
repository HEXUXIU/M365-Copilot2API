import os

def _load_dotenv():
    """Load .env file into environment variables (no external dependency)."""
    candidates = [
        os.path.join(os.getcwd(), ".env"),
        os.path.join(os.path.dirname(os.path.dirname(os.path.dirname(__file__))), ".env"),
    ]
    for env_path in candidates:
        if os.path.exists(env_path):
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
            return

_load_dotenv()

from .auth import TokenManager, TokenRefreshError
from .client import M365Client
from .models import MODELS, lookup_model
from .payload import build_url, build_payload, build_conversation_payload

__version__ = "0.6.0"
