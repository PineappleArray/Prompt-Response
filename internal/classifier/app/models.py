from pydantic import BaseModel

class Req(BaseModel):
    prompt: str


class Resp(BaseModel):
    model: str
    task_type: str
    reasoning: float
    complexity: float

class Req(BaseModel):
    prompt: str