import json
import logging
import threading

import numpy as np
import torch
import torch.nn as nn
from huggingface_hub import PyTorchModelHubMixin, hf_hub_download
from safetensors.torch import load_file
from transformers import AutoConfig, AutoModel, AutoTokenizer

logger = logging.getLogger(__name__)

# Score weights for prompt_complexity_score. Pushed from Go config via /configure
# so both the heuristic and DeBERTa paths compute the same composite score.
_score_weights: dict[str, float] = {
    "creativity": 0.20,
    "reasoning":  0.40,
    "constraint": 0.15,
    "domain":     0.15,
    "contextual": 0.05,
    "few_shots":  0.05,
}

def update_score_weights(weights: dict[str, float]) -> None:
    _score_weights.update(weights)

_tokenizer_lock = threading.Lock()


class MeanPooling(nn.Module):
    def forward(self, h, mask):
        m = mask.unsqueeze(-1).expand(h.size()).float()
        return (h * m).sum(1) / torch.clamp(m.sum(1), min=1e-9)


class MulticlassHead(nn.Module):
    def __init__(self, in_size, n):
        super().__init__()
        self.fc = nn.Linear(in_size, n)

    def forward(self, x):
        return self.fc(x)


class CustomModel(nn.Module, PyTorchModelHubMixin):
    def __init__(self, target_sizes, task_type_map, weights_map, divisor_map):
        super().__init__()
        cfg = AutoConfig.from_pretrained("microsoft/deberta-v3-base")
        self.backbone = AutoModel.from_config(cfg)
        self.target_names = list(target_sizes.keys())
        self.task_type_map = task_type_map
        self.weights_map = weights_map
        self.divisor_map = divisor_map
        self._weight_arrays: dict[str, np.ndarray] = {k: np.array(v) for k, v in weights_map.items()}
        # precomputed at init so forward has no per-call dict membership tests
        self._scoring_heads: frozenset[str] = frozenset(k for k in weights_map if k in divisor_map)
        self.heads = nn.ModuleList(
            [MulticlassHead(self.backbone.config.hidden_size, target_sizes[name])
             for name in self.target_names]
        )
        self.pool = MeanPooling()

    def forward(self, batch):
        out = self.backbone(
            input_ids=batch["input_ids"], attention_mask=batch["attention_mask"]
        )
        pooled = self.pool(out.last_hidden_state, batch["attention_mask"])
        # return raw softmax tensors — no numpy/Python logic inside the graph
        return [torch.softmax(h(pooled), dim=1) for h in self.heads]


# ---------------------------------------------------------------------------
# Load model and tokenizer at module level (runs once on import)
# ---------------------------------------------------------------------------

DEVICE = "cuda" if torch.cuda.is_available() else "cpu"
MODEL_REPO = "nvidia/prompt-task-and-complexity-classifier"

config_path = hf_hub_download(repo_id=MODEL_REPO, filename="config.json")
with open(config_path) as f:
    cfg_json = json.load(f)

target_sizes  = cfg_json["target_sizes"]
task_type_map = cfg_json["task_type_map"]
weights_map   = cfg_json["weights_map"]
divisor_map   = cfg_json["divisor_map"]

_orig_model = CustomModel(
    target_sizes=target_sizes,
    task_type_map=task_type_map,
    weights_map=weights_map,
    divisor_map=divisor_map,
)

try:
    weights_path = hf_hub_download(repo_id=MODEL_REPO, filename="model.safetensors")
    state_dict = load_file(weights_path)
except Exception:
    weights_path = hf_hub_download(repo_id=MODEL_REPO, filename="pytorch_model.bin")
    state_dict = torch.load(weights_path, map_location="cpu")

remapped = {}
for k, v in state_dict.items():
    if k.startswith("head_"):
        idx_and_rest = k[len("head_"):]
        idx, rest = idx_and_rest.split(".", 1)
        remapped[f"heads.{idx}.{rest}"] = v
    else:
        remapped[k] = v

missing, unexpected = _orig_model.load_state_dict(remapped, strict=False)
logger.info("model weights loaded: missing=%d unexpected=%d", len(missing), len(unexpected))
if missing:
    logger.debug("missing keys (first 5): %s", missing[:5])
if unexpected:
    logger.debug("unexpected keys (first 5): %s", unexpected[:5])

tokenizer = AutoTokenizer.from_pretrained(MODEL_REPO)

# move to device and set eval mode BEFORE compiling so the graph targets the right device
_orig_model = _orig_model.to(DEVICE).eval()
# torch.compile on CPU triggers Inductor C++ codegen that recompiles for every
# new sequence length — makes first call per shape take 30-120s, not a win.
# Only compile on CUDA where kernel fusion actually helps.
if DEVICE == "cuda":
    classifier_model = torch.compile(_orig_model, dynamic=True)
else:
    classifier_model = _orig_model

logger.info("model ready: device=%s compiled=%s head_order=%s", DEVICE, DEVICE == "cuda", _orig_model.target_names)

def _tokenize(text: str, max_length: int = 512) -> dict[str, torch.Tensor]:
    """Smart-truncating tokenizer: single lock acquire, no re-tokenization."""
    with _tokenizer_lock:
        ids = tokenizer.encode(text, add_special_tokens=False)
        if len(ids) > max_length - 2:
            half = (max_length - 2) // 2
            ids = ids[:half] + ids[-half:]
    cls_id, sep_id = tokenizer.cls_token_id, tokenizer.sep_token_id
    full_ids = (([cls_id] if cls_id is not None else []) + ids +
                ([sep_id] if sep_id is not None else []))
    input_ids = torch.tensor([full_ids], dtype=torch.long)
    attention_mask = torch.ones_like(input_ids)
    return {"input_ids": input_ids, "attention_mask": attention_mask}


@torch.inference_mode()
def classify_prompt(prompt: str) -> dict:
    enc = _tokenize(prompt)
    # move tensors to device outside the tokenizer lock
    enc = {k: v.to(DEVICE) for k, v in enc.items()}
    logits = classifier_model(enc)

    r: dict = {}
    for name, lg in zip(_orig_model.target_names, logits):
        lg_np = lg.cpu().numpy()
        if name == "task_type":
            r["task_type"] = _orig_model.task_type_map[str(int(lg_np[0].argmax()))]
        elif name in _orig_model._scoring_heads:
            w = _orig_model._weight_arrays[name]
            d = _orig_model.divisor_map[name]
            r[name] = float((lg_np * w).sum(axis=1)[0] / d)

    sw = _score_weights
    r["prompt_complexity_score"] = (
        sw["creativity"] * r.get("creativity_scope", 0.0)
        + sw["reasoning"]  * r.get("reasoning", 0.0)
        + sw["constraint"] * r.get("constraint_ct", 0.0)
        + sw["domain"]     * r.get("domain_knowledge", 0.0)
        + sw["contextual"] * r.get("contextual_knowledge", 0.0)
        + sw["few_shots"]  * r.get("number_of_few_shots", 0.0)
    )
    return r


def _pad_to(encs: list[dict], device: str) -> dict[str, torch.Tensor]:
    max_len = max(e["input_ids"].shape[1] for e in encs)
    ids, masks = [], []
    for e in encs:
        pad = max_len - e["input_ids"].shape[1]
        ids.append(torch.nn.functional.pad(e["input_ids"], (0, pad)))
        masks.append(torch.nn.functional.pad(e["attention_mask"], (0, pad)))
    return {
        "input_ids": torch.cat(ids).to(device),
        "attention_mask": torch.cat(masks).to(device),
    }


@torch.inference_mode()
def classify_batch(prompts: list[str]) -> list[dict]:
    encs = [_tokenize(p) for p in prompts]
    batch = _pad_to(encs, DEVICE)
    logits_list = classifier_model(batch)

    results = []
    for i in range(len(prompts)):
        r: dict = {}
        for name, lg in zip(_orig_model.target_names, logits_list):
            lg_np = lg[i:i+1].cpu().numpy()
            if name == "task_type":
                r["task_type"] = _orig_model.task_type_map[str(int(lg_np[0].argmax()))]
            elif name in _orig_model._scoring_heads:
                w = _orig_model._weight_arrays[name]
                d = _orig_model.divisor_map[name]
                r[name] = float((lg_np * w).sum(axis=1)[0] / d)
        sw = _score_weights
        r["prompt_complexity_score"] = (
            sw["creativity"] * r.get("creativity_scope", 0.0)
            + sw["reasoning"]  * r.get("reasoning", 0.0)
            + sw["constraint"] * r.get("constraint_ct", 0.0)
            + sw["domain"]     * r.get("domain_knowledge", 0.0)
            + sw["contextual"] * r.get("contextual_knowledge", 0.0)
            + sw["few_shots"]  * r.get("number_of_few_shots", 0.0)
        )
        results.append(r)
    return results


def smart_truncate(text: str, max_length: int = 512) -> str:
    """Returns text truncated to max_length tokens using head+tail strategy."""
    with _tokenizer_lock:
        tokens = tokenizer.encode(text, add_special_tokens=False)
    if len(tokens) <= max_length:
        return text
    half = max_length // 2
    truncated = tokens[:half] + tokens[-half:]
    with _tokenizer_lock:
        return tokenizer.decode(truncated)