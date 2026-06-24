import json
import httpx
from fastapi import FastAPI, Request
from fastapi.responses import StreamingResponse

ROUTES = {
    "gemma4:31b":  "http://vllm-gemma:8000",
    "qwen3.6:27b": "http://vllm-qwen36:8001",
}
DEFAULT = "http://vllm-gemma:8000"

SKIP_RESP_HEADERS = {"transfer-encoding", "content-encoding", "content-length"}

app = FastAPI()


@app.get("/health/liveliness")
async def health():
    return {"status": "ok"}


@app.get("/v1/models")
async def models():
    seen, data = set(), []
    async with httpx.AsyncClient(timeout=10) as client:
        for upstream in set(ROUTES.values()):
            try:
                r = await client.get(f"{upstream}/v1/models")
                for m in r.json().get("data", []):
                    if m["id"] not in seen:
                        seen.add(m["id"])
                        data.append(m)
            except Exception:
                pass
    return {"object": "list", "data": data}


@app.api_route("/{path:path}", methods=["GET", "POST", "DELETE"])
async def proxy(request: Request, path: str):
    body = await request.body()
    upstream = DEFAULT
    if body:
        try:
            upstream = ROUTES.get(json.loads(body).get("model", ""), DEFAULT)
        except Exception:
            pass

    async def stream(client: httpx.AsyncClient, resp: httpx.Response):
        try:
            async for chunk in resp.aiter_bytes():
                yield chunk
        finally:
            await resp.aclose()
            await client.aclose()

    client = httpx.AsyncClient(timeout=600)
    req = client.build_request(
        method=request.method,
        url=f"{upstream}/{path}",
        content=body,
        headers={k: v for k, v in request.headers.items()
                 if k.lower() not in ("host", "content-length")},
        params=request.query_params,
    )
    resp = await client.send(req, stream=True)
    return StreamingResponse(
        stream(client, resp),
        status_code=resp.status_code,
        headers={k: v for k, v in resp.headers.items()
                 if k.lower() not in SKIP_RESP_HEADERS},
        media_type=resp.headers.get("content-type"),
    )
