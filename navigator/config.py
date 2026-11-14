import os
from dataclasses import dataclass, field
from pathlib import Path

from dotenv import load_dotenv

ROOT = Path(__file__).resolve().parents[1]


@dataclass
class Settings:
    api_key: str = field(default="", repr=False)
    base_url: str = "https://polza.ai/api/v1"
    model: str = "qwen/qwen3.8-flash"
    service_token: str = field(default="", repr=False)
    corpus_path: Path = ROOT / "data" / "demo_sources.json"

    @classmethod
    def from_env(cls):
        load_dotenv(ROOT / ".env", override=False)
        return cls(
            api_key=os.getenv("LLM_API_KEY", ""),
            base_url=os.getenv("LLM_BASE_URL", cls.base_url).rstrip("/"),
            model=os.getenv("LLM_MODEL", cls.model),
            service_token=os.getenv("AI_SERVICE_TOKEN", ""),
            corpus_path=Path(os.getenv("CORPUS_PATH", str(cls.corpus_path))),
        )
