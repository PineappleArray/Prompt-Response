from fastapi import FastAPI
from app.models import Req, Resp
from app import classifier_server
from app.model_select import pick

app = FastAPI(title="Prompt Classifier", version="0.1.0")

MODEL_TO_TIER = {
    "qwen2.5-1.5B-instruct-awq": "small",
    "qwen2.5-coder-7b-instruct-awq": "code",
    "qwen2.5-7B-instruct-awq": "reasoning",
    "qwen2.5-72b-instruct": "large",
}

@app.post("/classify", response_model=Resp)
def classify(req: Req):
    text = req.prompt or req.user_message
    truncated = classifier_server.smart_truncate(text)
    r = classifier_server.classify_prompt(truncated)
    model_id = pick(r)
    return Resp(
        tier=MODEL_TO_TIER.get(model_id, "small"),
        score=r.get("prompt_complexity_score", 0.0),
        signals={k: v for k, v in r.items() if isinstance(v, float)},
        build_reason=r.get("task_type", ""),
    )

@app.get("/health")
def health():
    return {"status": "ok"}