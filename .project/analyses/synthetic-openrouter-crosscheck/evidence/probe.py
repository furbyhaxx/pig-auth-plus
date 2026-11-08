#!/usr/bin/env python3
"""Compare Synthetic's streamed prompt usage, converted with pig-auth-plus' factors, to OpenRouter's
prompt_tokens for the same content. Keys are read from PiG's auth.json and never printed."""
import json, re, sys, time, urllib.request, urllib.error, pathlib, copy

ROOT = pathlib.Path(__file__).resolve().parents[4]
AUTH = json.load(open(pathlib.Path.home() / ".pig/agent/auth.json"))
SYN_KEY, OR_KEY = AUTH["synthetic"]["key"], AUTH["openrouter"]["key"]
OUT = pathlib.Path(__file__).parent / "results.json"

# model on Synthetic -> (tokenizer repo for factor lookup, OpenRouter id)
MODELS = {
    "hf:deepseek-ai/DeepSeek-V4.1-Flash-1017": ("deepseek-ai/DeepSeek-V4.1-Flash-1017", "deepseek/deepseek-v4.1-flash-1017"),
    "syn:large:text": ("deepseek-ai/DeepSeek-V4.1-Flash-1017", "deepseek/deepseek-v4.1-flash-1017"),
    "hf:moonshotai/Kimi-K3": ("moonshotai/Kimi-K3", "moonshotai/kimi-k3"),
    "hf:moonshotai/Kimi-K3.1-Pro": ("moonshotai/Kimi-K3.1-Pro", "moonshotai/kimi-k3.1-pro"),
    "hf:zai-org/GLM-5.3": ("zai-org/GLM-5.3", "z-ai/glm-5.3"),
    "hf:zai-org/GLM-5.3-Flash": ("zai-org/GLM-5.3-Flash", "z-ai/glm-5.3-flash"),
    "hf:zai-org/GLM-4.7-Flash": ("zai-org/GLM-4.7-Flash", "z-ai/glm-4.7-flash"),
    "hf:openai/gpt-oss-120b": ("openai/gpt-oss-120b", "openai/gpt-oss-120b"),
    "hf:Qwen/Qwen3.8-27B": ("Qwen/Qwen3.8-27B-FP8", "qwen/qwen3.8-27b"),
    "syn:small:text": ("zai-org/GLM-4.7-Flash", "z-ai/glm-4.7-flash"),
    "syn:large:vision": ("moonshotai/Kimi-K3", "moonshotai/kimi-k3"),
    "syn:small:vision": ("deepseek-ai/DeepSeek-V4.1-Flash-1017", "deepseek/deepseek-v4.1-flash-1017"),
    "infinite:small:vision": ("deepseek-ai/DeepSeek-V4.1-Flash-1017", "deepseek/deepseek-v4.1-flash-1017"),
}
if len(sys.argv) > 1:
    MODELS = {k: v for k, v in MODELS.items() if any(a in k for a in sys.argv[1:])}

units = (ROOT / "internal/providers/synthetic/units.go").read_text()
FACTORS = {m.group(1): float(m.group(2)) for m in re.finditer(r'"([^"]+)":\s+([0-9.]+),', units)}

prose = "".join((ROOT / f"docs/{n}.md").read_text() for n in ("quotas", "models", "configuration"))[:24000]
jtext = (ROOT / "internal/providers/synthetic/catalog.json").read_text()[:24000]
sysprompt = (ROOT / "AGENTS.md").read_text()[:6000]

def tool(name, desc, props, req):
    return {"name": name, "description": desc, "input_schema": {"type": "object", "properties": props, "required": req}}
TOOLS = [
    tool("read", "Read the contents of a file. Supports text files and images. Output is truncated to 2000 lines or 50KB.", {"path": {"type": "string", "description": "Path to the file to read"}, "offset": {"type": "number", "description": "Line to start from"}, "limit": {"type": "number", "description": "Maximum number of lines"}}, ["path"]),
    tool("bash", "Execute a bash command in the current working directory. Returns stdout and stderr.", {"command": {"type": "string", "description": "Shell command"}, "timeout": {"type": "number", "description": "Timeout in seconds"}}, ["command"]),
    tool("edit", "Edit a single file using exact text replacement.", {"path": {"type": "string"}, "edits": {"type": "array", "items": {"type": "object", "properties": {"oldText": {"type": "string"}, "newText": {"type": "string"}}, "required": ["oldText", "newText"]}}}, ["path", "edits"]),
    tool("write", "Write content to a file, creating parent directories.", {"path": {"type": "string"}, "content": {"type": "string"}}, ["path", "content"]),
    tool("grep", "Search file contents for a pattern.", {"pattern": {"type": "string"}, "path": {"type": "string"}, "ignoreCase": {"type": "boolean"}, "limit": {"type": "number"}}, ["pattern"]),
    tool("find", "Search for files by glob pattern.", {"pattern": {"type": "string"}, "path": {"type": "string"}}, ["pattern"]),
    tool("ls", "List directory contents.", {"path": {"type": "string"}, "limit": {"type": "number"}}, []),
]

# Each case: system, messages in a neutral form: ("user", text) | ("assistant_tool", id, name, input) | ("tool_result", id, text)
CASES = {
    "short": dict(system=None, tools=False, msgs=[("user", "What is the capital of France?")]),
    "prose": dict(system=None, tools=False, msgs=[("user", prose)]),
    "json": dict(system=None, tools=False, msgs=[("user", jtext)]),
    "tools": dict(system=None, tools=True, msgs=[("user", "List the files here.")]),
    "session": dict(system=sysprompt, tools=True, msgs=[
        ("user", "Read docs/tokens.md and summarize it."),
        ("assistant_tool", "toolu_01", "read", {"path": "docs/tokens.md"}),
        ("tool_result", "toolu_01", (ROOT / "docs/tokens.md").read_text()[:7000]),
        ("user", "Now list the headings."),
    ]),
}

import os
if os.environ.get("LARGE"):
    big = "".join(p.read_text() + "\n" for p in sorted(ROOT.glob("**/*.md")) if ".worktrees" not in p.parts)[:300000]
    CASES = {"large": dict(system=None, tools=False, msgs=[("user", big)])}

def anth_body(model, case):
    msgs = []
    for m in case["msgs"]:
        if m[0] == "user":
            msgs.append({"role": "user", "content": m[1]})
        elif m[0] == "assistant_tool":
            msgs.append({"role": "assistant", "content": [{"type": "tool_use", "id": m[1], "name": m[2], "input": m[3]}]})
        else:
            msgs.append({"role": "user", "content": [{"type": "tool_result", "tool_use_id": m[1], "content": m[2]}]})
    body = {"model": model, "max_tokens": 16, "stream": True, "messages": msgs}
    if case["system"]: body["system"] = case["system"]
    if case["tools"]: body["tools"] = TOOLS
    return body

def oai_body(model, case):
    msgs = []
    if case["system"]: msgs.append({"role": "system", "content": case["system"]})
    for m in case["msgs"]:
        if m[0] == "user":
            msgs.append({"role": "user", "content": m[1]})
        elif m[0] == "assistant_tool":
            msgs.append({"role": "assistant", "content": None, "tool_calls": [{"id": m[1], "type": "function", "function": {"name": m[2], "arguments": json.dumps(m[3])}}]})
        else:
            msgs.append({"role": "tool", "tool_call_id": m[1], "content": m[2]})
    body = {"model": model, "max_tokens": 64, "messages": msgs, "usage": {"include": True}}
    if case["tools"]:
        body["tools"] = [{"type": "function", "function": {"name": t["name"], "description": t["description"], "parameters": t["input_schema"]}} for t in TOOLS]
    return body

def post(url, headers, body, timeout=180):
    req = urllib.request.Request(url, json.dumps(body).encode(), {"Content-Type": "application/json", **headers})
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            return r.status, r.read().decode()
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode()[:300]
    except Exception as e:
        return 0, str(e)[:300]

def synthetic(model, case):
    status, text = post("https://api.synthetic.new/anthropic/v1/messages", {"x-api-key": SYN_KEY, "anthropic-version": "2023-06-01"}, anth_body(model, case))
    if status != 200: return {"error": f"{status} {text}"}
    usage = {}
    for line in text.splitlines():
        if line.startswith("data:"):
            try: ev = json.loads(line[5:])
            except ValueError: continue
            u = (ev.get("message") or {}).get("usage") or ev.get("usage")
            if u: usage.update({k: v for k, v in u.items() if isinstance(v, int)})
    i, cr, cw = usage.get("input_tokens", 0), usage.get("cache_read_input_tokens", 0), usage.get("cache_creation_input_tokens", 0)
    if cw == i: cw = 0
    return {"input": i, "cacheRead": cr, "cacheWrite": cw, "reported": i + cr + cw}

def openrouter(model, case):
    status, text = post("https://openrouter.ai/api/v1/chat/completions", {"Authorization": "Bearer " + OR_KEY}, oai_body(model, case))
    if status != 200: return {"error": f"{status} {text}"}
    d = json.loads(text)
    u = d.get("usage") or {}
    return {"prompt_tokens": u.get("prompt_tokens"), "provider": d.get("provider"), "cached": (u.get("prompt_tokens_details") or {}).get("cached_tokens")}

results = json.loads(OUT.read_text()) if OUT.exists() else {}
for syn_id, (repo, or_id) in MODELS.items():
    factor = FACTORS.get(repo)
    for cname, case in CASES.items():
        key = f"{syn_id}|{cname}"
        if key in results and "error" not in results[key]["synthetic"] and "error" not in results[key]["openrouter"]: continue
        s, o = synthetic(syn_id, case), openrouter(or_id, case)
        ours = round(s["reported"] / factor) if factor and "reported" in s else None
        ref = o.get("prompt_tokens")
        row = {"synthetic": s, "openrouter": o, "factor": factor, "ours": ours, "ratio": round(ours / ref, 4) if ours and ref else None}
        results[key] = row
        print(f"{syn_id:42} {cname:8} syn={s.get('reported')} ours={ours} or={ref} ratio={row['ratio']} via={o.get('provider')} {s.get('error','')[:60]}{o.get('error','')[:80]}", flush=True)
        OUT.write_text(json.dumps(results, indent=1))
        time.sleep(1)
