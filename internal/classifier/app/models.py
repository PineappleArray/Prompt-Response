from pydantic import BaseModel


class Req(BaseModel):
    prompt: str = ""              # kept for backward compat
    system_prompt: str = ""
    user_message: str = ""
    token_count: int = 0
    has_code: bool = False
    conv_turns: int = 0
    current_tier: str = ""        # tier the conversation is already pinned to


class Resp(BaseModel):
    tier: str
    score: float
    signals: dict[str, float]
    build_reason: str
    bucket: str = ""              # complexity-score band for the request
