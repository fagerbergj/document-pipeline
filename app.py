from contextlib import asynccontextmanager

from fastapi import FastAPI
from fastapi.templating import Jinja2Templates

templates = Jinja2Templates(directory="ui/templates")


@asynccontextmanager
async def lifespan(_app: FastAPI):
    # Phase 2: initialise DB, load pipeline config, start worker
    yield


app = FastAPI(title="document-pipeline", lifespan=lifespan)


@app.get("/healthz")
async def healthz():
    return {"status": "ok"}
