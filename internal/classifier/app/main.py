from fastapi import FastAPI
from app.models import Req, Resp
from app import classifier_server

app = FastAPI(title="Prompt Classifier", version="0.1.0")


@app.post("/classify", response_model=Resp)
def classify(req: Req):
    truncated = classifier_server.smart_truncate(req.prompt)
    r = classifier_server.classify_prompt(truncated)
    return Resp(
        model=classifier_server.pick(r),
        task_type=r["task_type"],
        reasoning=r.get("reasoning", 0.0),
        complexity=r.get("prompt_complexity_score", 0.0),
    )

@app.get("/health")
def health():
    return {"status": "ok"}