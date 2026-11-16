from fastapi.testclient import TestClient


def test_uvicorn_entrypoint_exposes_health():
    from main import app

    response = TestClient(app).get("/health")
    assert response.status_code == 200
    assert response.json() == {"status": "ok"}
