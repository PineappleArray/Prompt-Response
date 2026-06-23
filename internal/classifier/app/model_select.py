SMALL     = "qwen2.5-1.5B-instruct-awq"
CODE      = "qwen2.5-coder-7b-instruct-awq"
MEDIUM    = "qwen2.5-14b-instruct-awq"
REASONING = "qwen2.5-7B-instruct-awq"
LARGE     = "qwen2.5-72b-instruct"

CODE_SIGNALS = {"html", "css", "javascript", "python", "code",
                "function", "script", "api", "sql", "regex",
                "website", "app", "debug", "error", "compile",
                "algorithm", "class", "import", "return"}

# Tier metadata. MODEL_TO_TIER / TIER_TO_MODEL translate between a model id
# and its tier name; TIER_PRIORITY ranks tiers for up-tier-only escalation.
# The priorities match the priority field in config.yaml so the Python and
# Go sides agree on what "a higher tier" means.
MODEL_TO_TIER = {
    SMALL:     "small",
    CODE:      "code",
    MEDIUM:    "medium",
    REASONING: "reasoning",
    LARGE:     "large",
}

TIER_TO_MODEL = {
    "small":     SMALL,
    "code":      CODE,
    "medium":    MEDIUM,
    "reasoning": REASONING,
    "large":     LARGE,
}

TIER_PRIORITY = {
    "small":     1,
    "code":      2,
    "medium":    3,
    "large":     4,
    "reasoning": 5,
}

# Task types produced by nvidia/prompt-task-and-complexity-classifier.
# These must stay in sync with heuristicTaskType in internal/classifier/classifier.go.
_SUMMARIZATION_TYPES = {"summarization"}
_EXTRACTION_TYPES    = {"extraction"}
_CODE_TYPES          = {"code generation"}
_QA_TYPES            = {"qa", "closed qa", "open qa"}
_CLASSIFICATION_TYPES = {"classification"}
_DIALOGUE_TYPES      = {"dialogue"}
_GENERATION_TYPES    = {"text generation"}


def _base_pick(r, text=""):
    """Static, stateless model choice from the classifier signals.

    This is the original routing heuristic, kept unchanged so a request with
    no conversation history routes exactly as it always has.

    Task type routing (matches config.yaml tier rules and Go basePick):
      - Code Generation              → code tier
      - Code signals in prompt text  → code tier (when score < 0.60)
      - Summarization / Extraction   → medium tier (moderate capability needed)
      - Classification, QA variants  → small tier (when score and reasoning are low)
      - Dialogue                     → maintains current tier via clamp_up caller
      - High reasoning + score       → large tier
      - High score + domain + const  → large tier
      - Default                      → reasoning tier (capable catch-all)
    """
    task      = r.get("task_type", "").lower()
    score     = r.get("prompt_complexity_score", 0.0)
    reasoning = r.get("reasoning", 0.0)
    domain    = r.get("domain_knowledge", 0.0)
    constraint = r.get("constraint_ct", 0.0)

    # Code routing
    if task in _CODE_TYPES:
        return CODE
    for word in CODE_SIGNALS:
        if word in task:
            return CODE
    if text and ("```" in text or "def " in text or "class " in text or "function " in text):
        if score < 0.60:
            return CODE

    # Summarization and Extraction route to medium: they need reasonable
    # capability but rarely require deep multi-step reasoning.
    if task in _SUMMARIZATION_TYPES or task in _EXTRACTION_TYPES:
        if score < 0.55:
            return MEDIUM

    # Simple QA / Classification with low complexity
    is_simple = task in _QA_TYPES or task in _CLASSIFICATION_TYPES
    if is_simple and score < 0.15 and reasoning < 0.15:
        return SMALL

    # High-complexity routing
    if reasoning >= 0.70 and score >= 0.55:
        return LARGE
    if score >= 0.65 and domain >= 0.80 and constraint >= 0.60:
        return LARGE

    return REASONING


def _clamp_up(model_id, current_tier):
    """Raise model_id to current_tier when the conversation is already pinned
    to a more capable tier. A conversation's tier only ever goes up."""
    picked_tier = MODEL_TO_TIER.get(model_id, "")
    if TIER_PRIORITY.get(current_tier, 0) > TIER_PRIORITY.get(picked_tier, 0):
        return TIER_TO_MODEL.get(current_tier, model_id)
    return model_id


def pick(r, text="", current_tier=""):
    """Choose a model for a request.

    current_tier is the tier the conversation has already been routed to on a
    previous turn (empty for the first turn). The static heuristic decides the
    base tier; if the conversation is already pinned higher, the choice is
    clamped up so multi-turn conversations stay consistent and never downgrade.
    """
    chosen = _base_pick(r, text)
    if current_tier:
        chosen = _clamp_up(chosen, current_tier)
    return chosen
