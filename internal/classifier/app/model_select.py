SMALL     = "qwen2.5-1.5B-instruct-awq"
CODE      = "qwen2.5-coder-7b-instruct-awq"
REASONING = "qwen2.5-7B-instruct-awq"
LARGE     = "qwen2.5-72b-instruct"

CODE_SIGNALS = {"html", "css", "javascript", "python", "code",
                "function", "script", "api", "sql", "regex",
                "website", "app", "debug", "error", "compile",
                "algorithm", "class", "import", "return"}

def pick(r, text=""):
    task      = r.get("task_type", "")
    score     = r.get("prompt_complexity_score", 0.0)
    reasoning = r.get("reasoning", 0.0)
    domain    = r.get("domain_knowledge", 0.0)
    creativity = r.get("creativity_scope", 0.0)
    constraint = r.get("constraint_ct", 0.0)


    if task == "Code Generation":
        return CODE
    for word in CODE_SIGNALS:
        if word in task.lower():
            return CODE
    # catch code blocks in the actual prompt text
    if text and ("```" in text or "def " in text or "class " in text or "function " in text):
        # only if score is low enough that it's a straightforward code task
        if score < 0.60:
            return CODE

    is_qa = "QA" in task or task == "Classification"

    # very low complexity QA — genuinely simple questions
    if is_qa and score < 0.15 and reasoning < 0.15:
        return SMALL

    if (reasoning >= 0.70 and score >= 0.55):
        return LARGE
    if (score >= 0.65 and domain >= 0.80 and constraint >= 0.60):
        return LARGE


    return REASONING