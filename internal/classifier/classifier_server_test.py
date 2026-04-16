import json
from datasets import load_dataset
from transformers import AutoTokenizer
from collections import Counter
import classifier_server

# Model identifiers
SMALL     = "qwen2.5-1.5B-instruct-awq"   
CODE      = "qwen2.5-coder-7b-instruct-awq"
REASONING = "qwen2.5-7B-instruct-awq"       
LARGE     = "qwen2.5-72b-instruct"

# DeepInfra pricing ($ per 1M tokens), with projections for tiers not directly listed
COST_PER_1M = {
    SMALL:     {"input": 0.02, "output": 0.05, "source": "projected_param_ratio"},
    CODE:      {"input": 0.04, "output": 0.10, "source": "projected_same_tier_as_7b"},
    REASONING: {"input": 0.04, "output": 0.10, "source": "deepinfra_verified"},
    LARGE:     {"input": 0.12, "output": 0.39, "source": "deepinfra_verified"},
}

# Load tokenizer once — all Qwen2.5 models share the same tokenizer
TOKENIZER = AutoTokenizer.from_pretrained("Qwen/Qwen2.5-7B-Instruct")

# Placeholder until you have real model responses. Tune or replace with measured averages.
ASSUMED_COMPLETION_TOKENS = 300


def count_tokens(text: str) -> int:
    return len(TOKENIZER.encode(text, add_special_tokens=False))


def cost(prompt_tokens: int, completion_tokens: int, model_id: str) -> float:
    p = COST_PER_1M[model_id]
    return (prompt_tokens * p["input"] + completion_tokens * p["output"]) / 1_000_000


def route_all(prompts):
    """Classify each prompt once, return (model_id, prompt) pairs."""
    results = []
    for prompt in prompts:
        truncated = classifier_server.smart_truncate(prompt)
        r = classifier_server.classify_prompt(truncated)
        chosen = classifier_server.pick(r)
        results.append((chosen, prompt))
    return results


def compute_costs(results, completion_tokens=ASSUMED_COMPLETION_TOKENS):
    """Compute routed cost vs. always-LARGE baseline."""
    rows = []
    for model_id, prompt in results:
        pt = count_tokens(prompt)
        routed = cost(pt, completion_tokens, model_id)
        baseline = cost(pt, completion_tokens, LARGE)
        rows.append({
            "prompt": prompt[:80],
            "routed_to": model_id,
            "prompt_tokens": pt,
            "completion_tokens_assumed": completion_tokens,
            "routed_cost": routed,
            "baseline_cost": baseline,
            "savings": baseline - routed,
        })
    return rows


def summarize(rows):
    total_routed = sum(r["routed_cost"] for r in rows)
    total_baseline = sum(r["baseline_cost"] for r in rows)
    savings_pct = (total_baseline - total_routed) / total_baseline if total_baseline else 0
    distribution = Counter(r["routed_to"] for r in rows)

    print(f"\n{'='*60}")
    print(f"Prompts evaluated:   {len(rows)}")
    print(f"Total routed cost:   ${total_routed:.6f}")
    print(f"Total baseline cost: ${total_baseline:.6f}  (always-{LARGE})")
    print(f"Savings:             {savings_pct:.1%}")
    print(f"\nRouting distribution:")
    for model, count in distribution.most_common():
        share = count / len(rows)
        print(f"  {model:40s} {count:>3d}  ({share:.1%})")
    print(f"\nNote: completion tokens assumed at {ASSUMED_COMPLETION_TOKENS}/request.")
    print(f"Replace with measured values after running real inference.")


# --- Main ---

ds = load_dataset("HuggingFaceH4/mt_bench_prompts")
prompts = [ex["prompt"][0] if isinstance(ex["prompt"], list) else ex["prompt"]
           for ex in ds["train"]]

results = route_all(prompts)
rows = compute_costs(results)
summarize(rows)

# Save raw data for later analysis
with open("cost_results.json", "w") as f:
    json.dump(rows, f, indent=2)