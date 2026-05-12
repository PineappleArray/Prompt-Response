from fastapi import FastAPI
import classifier_server
from internal.classifier import app
from types import Resp
import internal.classifier.app.classifier_server as classifier_server
import internal.classifier.app.models as model

@app.post("/classify", response_model=Resp)
def classify(req: model.Req):
    text = req.prompt
    r = classifier_server.classify_prompt(req.prompt)
    return Resp(
        model=classifier_server.pick(r, text),
        task_type=r["task_type"],
        reasoning=r.get("reasoning", 0.0),
        complexity=r.get("prompt_complexity_score", 0.0),
    )


@app.get("/health")
def health():
    return {"ok": True}