import secrets

from fastapi import Depends, FastAPI, Header, HTTPException
from fastapi.responses import JSONResponse

from navigator.config import Settings
from navigator.llm import LLM, ProviderError
from navigator.models import AnalyzeRequest, AnalyzeResponse
from navigator.retrieval import Corpus
from navigator.service import Navigator


def create_app(settings: Settings | None = None, navigator=None):
    settings = settings or Settings.from_env()
    service = navigator or Navigator(Corpus(settings.corpus_path), LLM(settings))
    app = FastAPI(title="AI-buddy Task Navigator", version="1.0.0")

    async def authorize(authorization: str = Header(default="")):
        if not settings.service_token:
            raise HTTPException(503, detail="service_token_not_configured")
        if not secrets.compare_digest(
            authorization, "Bearer " + settings.service_token
        ):
            raise HTTPException(401, detail="unauthorized")

    @app.exception_handler(ProviderError)
    async def provider_error(_, error):
        return JSONResponse(
            status_code=error.status, content={"error": {"code": error.code}}
        )

    @app.get("/health")
    async def health():
        return {"status": "ok"}

    @app.post(
        "/v1/analyze", response_model=AnalyzeResponse, dependencies=[Depends(authorize)]
    )
    async def analyze(request: AnalyzeRequest):
        return await service.analyze(request)

    return app


app = create_app()
