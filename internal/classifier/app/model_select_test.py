"""Unit tests for model_select.pick.

Pure-Python: no model weights, no network. Run from this directory with

    python3 model_select_test.py
"""

from model_select import (
    pick, _base_pick, _clamp_up,
    SMALL, CODE, REASONING, LARGE,
    MODEL_TO_TIER, TIER_PRIORITY,
)

# Representative classifier outputs and the tier _base_pick assigns them.
SIMPLE_QA   = {"task_type": "QA", "prompt_complexity_score": 0.05, "reasoning": 0.05}
HARD_REASON = {"task_type": "Open QA", "prompt_complexity_score": 0.60, "reasoning": 0.75}
CODE_TASK   = {"task_type": "Code Generation", "prompt_complexity_score": 0.30}
DEFAULT     = {"task_type": "Brainstorming", "prompt_complexity_score": 0.40, "reasoning": 0.30}

BASE_CASES = [
    ("simple qa -> small",   SIMPLE_QA,   SMALL),
    ("hard reasoning -> large", HARD_REASON, LARGE),
    ("code task -> code",    CODE_TASK,   CODE),
    ("default -> reasoning", DEFAULT,     REASONING),
]


def test_base_pick_unchanged():
    for name, r, want in BASE_CASES:
        got = _base_pick(r)
        assert got == want, f"{name}: _base_pick = {got}, want {want}"


def test_pick_without_current_tier_matches_base():
    # With no conversation history, pick must be byte-identical to _base_pick.
    for name, r, _ in BASE_CASES:
        assert pick(r) == _base_pick(r), f"{name}: pick diverged from _base_pick"
        assert pick(r, current_tier="") == _base_pick(r), f"{name}: empty tier diverged"


def test_pick_never_downgrades():
    # Conversation pinned to large; a trivial follow-up still routes to large.
    assert pick(SIMPLE_QA, current_tier="large") == LARGE
    assert pick(CODE_TASK, current_tier="large") == LARGE
    # Pinned to reasoning (the highest tier) — nothing downgrades it.
    assert pick(SIMPLE_QA, current_tier="reasoning") == REASONING


def test_pick_escalates_when_classifier_goes_higher():
    # Conversation pinned low; a hard turn is allowed to escalate.
    assert pick(HARD_REASON, current_tier="small") == LARGE
    assert pick(HARD_REASON, current_tier="code") == LARGE


def test_pick_keeps_tier_when_equal():
    # Classifier picks the same tier the conversation is pinned to.
    assert pick(SIMPLE_QA, current_tier="small") == SMALL


def test_clamp_up_uses_tier_priority():
    assert _clamp_up(SMALL, "large") == LARGE     # raised
    assert _clamp_up(LARGE, "small") == LARGE     # not lowered
    assert _clamp_up(SMALL, "") == SMALL          # no pin
    assert _clamp_up(SMALL, "gigantic") == SMALL  # unknown tier ignored


def test_tier_priority_is_total_order():
    # Every model's tier must have a priority so escalation is well-defined.
    for model_id, tier in MODEL_TO_TIER.items():
        assert tier in TIER_PRIORITY, f"{model_id}: tier {tier} missing from TIER_PRIORITY"


def _run():
    tests = [v for k, v in sorted(globals().items()) if k.startswith("test_")]
    failed = 0
    for t in tests:
        try:
            t()
            print(f"PASS {t.__name__}")
        except AssertionError as e:
            failed += 1
            print(f"FAIL {t.__name__}: {e}")
    print(f"\n{len(tests) - failed}/{len(tests)} passed")
    return failed


if __name__ == "__main__":
    import sys
    sys.exit(1 if _run() else 0)
