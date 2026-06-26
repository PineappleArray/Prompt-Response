from fastapi import FastAPI
from app.models import Req, Resp, ConfigReq
from app import classifier_server
from app.model_select import pick, MODEL_TO_TIER

app = FastAPI(title="Prompt Classifier", version="0.1.0")


def score_bucket(score: float) -> str:
    """Coarse complexity band. Must match types.ScoreBucket on the Go side."""
    if score < 0.15:
        return "b0"
    if score < 0.35:
        return "b1"
    if score < 0.55:
        return "b2"
    if score < 0.75:
        return "b3"
    return "b4"


@app.post("/classify", response_model=Resp)
def classify(req: Req):
    text = req.prompt or req.user_message
    r = classifier_server.classify_prompt(text)
    model_id = pick(r, text=text, current_tier=req.current_tier)
    score = r.get("prompt_complexity_score", 0.0)
    return Resp(
        tier=MODEL_TO_TIER.get(model_id, "small"),
        score=score,
        signals={k: v for k, v in r.items() if isinstance(v, float)},
        build_reason=r.get("task_type", ""),
        bucket=score_bucket(score),
    )


@app.post("/classify_batch", response_model=list[Resp])
def classify_batch(reqs: list[Req]):
    texts = [r.prompt or r.user_message for r in reqs]
    batch_r = classifier_server.classify_batch(texts)
    out = []
    for req, r in zip(reqs, batch_r):
        model_id = pick(r, text=req.prompt or req.user_message, current_tier=req.current_tier)
        score = r.get("prompt_complexity_score", 0.0)
        out.append(Resp(
            tier=MODEL_TO_TIER.get(model_id, "small"),
            score=score,
            signals={k: v for k, v in r.items() if isinstance(v, float)},
            build_reason=r.get("task_type", ""),
            bucket=score_bucket(score),
        ))
    return out


@app.post("/configure")
def configure(req: ConfigReq):
    if req.score_weights:
        classifier_server.update_score_weights(req.score_weights)
    return {"status": "ok"}


@app.get("/health")
def health():
    return {"status": "ok"}
