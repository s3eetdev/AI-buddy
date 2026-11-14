import json

import httpx

from navigator.config import Settings


class ProviderError(Exception):
    def __init__(self, code: str, status: int = 502):
        self.code, self.status = code, status
        super().__init__(code)


class LLM:
    def __init__(self, settings: Settings):
        self.settings = settings

    async def complete(self, system: str, payload: dict, max_tokens: int = 6500):
        if not self.settings.api_key:
            raise ProviderError("llm_not_configured", 503)
        try:
            async with httpx.AsyncClient(timeout=90, follow_redirects=False) as client:
                response = await client.post(
                    self.settings.base_url + "/chat/completions",
                    headers={"Authorization": f"Bearer {self.settings.api_key}"},
                    json={
                        "model": self.settings.model,
                        "messages": [
                            {"role": "system", "content": system},
                            {
                                "role": "user",
                                "content": json.dumps(payload, ensure_ascii=False),
                            },
                        ],
                        "temperature": 0.1,
                        "reasoning": {"enabled": False},
                        "max_tokens": max_tokens,
                        "response_format": {"type": "json_object"},
                    },
                )
        except httpx.TimeoutException:
            raise ProviderError("llm_timeout", 504) from None
        except httpx.HTTPError:
            raise ProviderError("llm_unavailable", 502) from None
        if response.status_code != 200:
            code = (
                "llm_rate_limited"
                if response.status_code == 429
                else "llm_request_failed"
            )
            raise ProviderError(code, 503 if response.status_code == 429 else 502)
        try:
            data = response.json()
            choice = data["choices"][0]
            if choice.get("finish_reason") == "length":
                raise ValueError("truncated")
            result = json.loads(choice["message"]["content"])
            if not isinstance(result, dict):
                raise ValueError("object required")
            usage = {
                k: v
                for k, v in data.get("usage", {}).items()
                if k in {"prompt_tokens", "completion_tokens", "total_tokens"}
                and type(v) is int
                and v >= 0
            }
            return result, usage
        except (ValueError, KeyError, IndexError, TypeError, AttributeError):
            raise ProviderError("llm_invalid_response") from None
