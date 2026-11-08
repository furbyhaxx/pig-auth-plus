#!/usr/bin/env python3
"""OpenRouter prompt_tokens for the same request across individual upstream providers."""
import json, sys, urllib.request
sys.argv = ["probe", "NONE"]
src = open(__file__.replace("providers.py", "probe.py")).read().split("results = json.loads")[0]
g = {"__file__": __file__.replace("providers.py", "probe.py")}
exec(compile(src, "probe.py", "exec"), g)
k = g["OR_KEY"]

def endpoints(model):
    r = urllib.request.Request(f"https://openrouter.ai/api/v1/models/{model}/endpoints", headers={"Authorization": "Bearer " + k})
    return json.load(urllib.request.urlopen(r))["data"]["endpoints"]

for model, case in (("qwen/qwen3.8-27b", "tools"), ("qwen/qwen3.8-27b", "session"), ("moonshotai/kimi-k3", "session")):
    seen = {}
    for e in endpoints(model):
        p = e["provider_name"]
        if p in seen: continue
        body = g["oai_body"](model, g["CASES"][case])
        body["provider"] = {"order": [p], "allow_fallbacks": False}
        s, t = g["post"]("https://openrouter.ai/api/v1/chat/completions", {"Authorization": "Bearer " + k}, body)
        if s == 200: seen[p] = json.loads(t)["usage"]["prompt_tokens"]
        if len(seen) >= 6: break
    print(model, case, seen)
