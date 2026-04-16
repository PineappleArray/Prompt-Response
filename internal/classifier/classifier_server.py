import numpy as np
import torch
import torch.nn as nn
from fastapi import FastAPI
from huggingface_hub import PyTorchModelHubMixin
from pydantic import BaseModel
from transformers import AutoConfig, AutoModel, AutoTokenizer
from huggingface_hub import hf_hub_download
import json
from safetensors.torch import load_file

CODE_SIGNALS = {"html", "css", "javascript", "python", "code",  #Made as the current model used to fit will sometimes
                "function", "script", "api", "sql", "regex",    #miss some of the coding prompts
                "website", "app", "debug", "error", "compile",
                "algorithm", "class", "import", "return"}

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
        self.backbone = AutoModel.from_config(cfg)   # empty shell
        self.target_names = list(target_sizes.keys())
        self.task_type_map = task_type_map
        self.weights_map = weights_map
        self.divisor_map = divisor_map
        self.heads = nn.ModuleList(
            [MulticlassHead(self.backbone.config.hidden_size, target_sizes[name])
            for name in self.target_names]
        )
        self.pool = MeanPooling()

    def _score(self, preds, target):
        w = np.array(self.weights_map[target])
        d = self.divisor_map[target]
        return float((preds.detach().cpu().numpy() * w).sum(axis=1)[0] / d)

    def forward(self, batch):
        out = self.backbone(
            input_ids=batch["input_ids"], attention_mask=batch["attention_mask"]
        )
        pooled = self.pool(out.last_hidden_state, batch["attention_mask"])
        logits = [torch.softmax(h(pooled), dim=1) for h in self.heads]

        r = {}
        for name, lg in zip(self.target_names, logits):
            if name == "task_type":
                idx = int(torch.topk(lg, k=1, dim=1).indices[0, 0].item())
                r["task_type"] = self.task_type_map[str(idx)]
            elif name in self.weights_map and name in self.divisor_map:
                r[name] = self._score(lg, name)
            # anything else (e.g. no_label_reason) is ignored for routing

        r["prompt_complexity_score"] = (
            0.35 * r.get("creativity_scope", 0.0)
            + 0.25 * r.get("reasoning", 0.0)
            + 0.15 * r.get("constraint_ct", 0.0)
            + 0.15 * r.get("domain_knowledge", 0.0)
            + 0.05 * r.get("contextual_knowledge", 0.0)
            + 0.05 * r.get("number_of_few_shots", 0.0)
        )
        return r


#DEVICE = "cuda" if torch.cuda.is_available() else "cpu"
#MODEL = "nvidia/prompt-task-and-complexity-classifier"
#tokenizer = AutoTokenizer.from_pretrained(MODEL)
#model = CustomModel.from_pretrained(MODEL).to(DEVICE).eval()


DEVICE = "cuda" if torch.cuda.is_available() else "cpu"
MODEL = "nvidia/prompt-task-and-complexity-classifier"

# 1. Pull the config to get target_sizes / maps
config_path = hf_hub_download(repo_id=MODEL, filename="config.json")
with open(config_path) as f:
    cfg_json = json.load(f)

target_sizes   = cfg_json["target_sizes"]
task_type_map  = cfg_json["task_type_map"]
weights_map    = cfg_json["weights_map"]
divisor_map    = cfg_json["divisor_map"]

# 2. Build an empty model with the right shape
model = CustomModel(
    target_sizes=target_sizes,
    task_type_map=task_type_map,
    weights_map=weights_map,
    divisor_map=divisor_map,
)

# 3. Download and load the actual weights
try:
    weights_path = hf_hub_download(repo_id=MODEL, filename="model.safetensors")
    state_dict = load_file(weights_path)
except Exception:
    weights_path = hf_hub_download(repo_id=MODEL, filename="pytorch_model.bin")
    state_dict = torch.load(weights_path, map_location="cpu")

# Remap NVIDIA's flat head_N.* names to ModuleList-style heads.N.*
remapped = {}
for k, v in state_dict.items():
    if k.startswith("head_"):
        # "head_0.fc.weight" -> "heads.0.fc.weight"
        idx_and_rest = k[len("head_"):]
        idx, rest = idx_and_rest.split(".", 1)
        remapped[f"heads.{idx}.{rest}"] = v
    else:
        remapped[k] = v

missing, unexpected = model.load_state_dict(remapped, strict=False)
print(f"Missing keys ({len(missing)}):", missing[:5])
print(f"Unexpected keys ({len(unexpected)}):", unexpected[:5])

model = model.to(DEVICE).eval()
tokenizer = AutoTokenizer.from_pretrained(MODEL)
print("Head order:", model.target_names)


# Print head order once on startup so you can sanity-check it.
print("Head order from config:", model.target_names)

SMALL = "qwen2.5-1.5B-instruct-awq"
CODE = "qwen2.5-coder-7b-instruct-awq"
REASONING = "qwen2.5-7B-instruct-awq"
LARGE = "qwen2.5-72b-instruct"


def pick(r):
    # Written to ensure that the classifier would not misclassify prompts that is associated with code
    if r.get("reasoning", 0) >= 0.80 or r.get("prompt_complexity_score", 0) >= 0.80:
        return LARGE
    for word in CODE_SIGNALS:
        if word in r.get("task_type").lower():
            return CODE
    if "QA" in r.get("task_type"):
        return SMALL 
    if r.get("task_type") == "Text Generation" and r.get("domain_knowledge") >= 0.60:
        return REASONING
    if r.get("task_type") == "Code Generation":
        return CODE
    if r.get("reasoning", 0) >= 0.5 or r.get("prompt_complexity_score", 0) >= 0.5:
        return REASONING
    if r.get("domain", 0) >= 0.75 and r.get("prompt_complexity_score", 0) >= 0.35:
        return REASONING
    if r.get("prompt_complexity_score", 0) >= 0.5:
        return REASONING
    
    return SMALL


@torch.inference_mode()
def classify_prompt(prompt: str) -> dict:
    enc = tokenizer(
        [prompt], return_tensors="pt", add_special_tokens=True,
        max_length=512, padding="max_length", truncation=True,
    ).to(DEVICE)
    return model(enc)

def smart_truncate(text, max_length=512):
    tokens = tokenizer.encode(text, add_special_tokens=False)
    if len(tokens) <= max_length:
        return text
    half = max_length // 2
    truncated = tokens[:half] + tokens[-half:]
    return tokenizer.decode(truncated)


app = FastAPI()


class Req(BaseModel):
    prompt: str


class Resp(BaseModel):
    model: str
    task_type: str
    reasoning: float
    complexity: float


@app.post("/classify", response_model=Resp)
def classify(req: Req):
    text = req.prompt
    r = classify_prompt(req.prompt)
    return Resp(
        model=pick(r, text),
        task_type=r["task_type"],
        reasoning=r.get("reasoning", 0.0),
        complexity=r.get("prompt_complexity_score", 0.0),
    )


@app.get("/health")
def health():
    return {"ok": True}


# Run directly for quick testing: `python classifier_server.py`
if __name__ == "__main__":
    prompts = [
        "What's the capital of France?",
        "Write a Python function to reverse a linked list.",
        "Prove that the sum of two odd numbers is always even.",
        "Draft a short poem about autumn leaves.",
    ]
    for p in prompts:
        r = classify_prompt(p)
        print(f"\n{p}")
        for k, v in r.items():
            print(f"  {k}: {v}")
        print(f"  -> {pick(r)}")