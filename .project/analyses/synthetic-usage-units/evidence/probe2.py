import json, os, sys, urllib.request, uuid
from tokenizers import Tokenizer
from huggingface_hub import hf_hub_download
key = json.load(open(os.path.expanduser('~/.pig/agent/auth.json')))['synthetic']['key']
H = {'x-api-key': key, 'content-type': 'application/json', 'anthropic-version': '2023-06-01'}
def call(model, text, stream):
    body = json.dumps({'model': model, 'max_tokens': 1, 'stream': stream, 'messages': [{'role': 'user', 'content': text}]}).encode()
    r = urllib.request.urlopen(urllib.request.Request('https://api.synthetic.new/anthropic/v1/messages', body, H), timeout=180)
    if not stream:
        return json.load(r)['usage']
    out = {}
    for line in r:
        line = line.decode().strip()
        if line.startswith('data:'):
            e = json.loads(line[5:])
            if e['type'] == 'message_start': out['start'] = e['message']['usage']
            if e['type'] == 'message_delta': out['delta'] = e.get('usage')
    return out
for model, repo in [('hf:deepseek-ai/DeepSeek-V4.1-Flash-1017','deepseek-ai/DeepSeek-V4.1-Flash-1017'), ('hf:zai-org/GLM-4.7-Flash','zai-org/GLM-4.7-Flash'), ('hf:moonshotai/Kimi-K3.1-Pro', None), ('hf:openai/gpt-oss-120b','openai/gpt-oss-120b')]:
    tok = Tokenizer.from_file(hf_hub_download(repo, 'tokenizer.json')) if repo else None
    for name in ('prose', 'json'):
        text = uuid.uuid4().hex + ' ' + open(name + '.txt').read()
        real = len(tok.encode(text, add_special_tokens=False).ids) if tok else None
        print(model, name, 'real', real, 'nonstream', call(model, text, False), flush=True)
        text2 = uuid.uuid4().hex + ' ' + open(name + '.txt').read()
        print(model, name, 'real', real, 'stream', json.dumps(call(model, text2, True)), flush=True)
