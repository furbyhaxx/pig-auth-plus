import json, os, urllib.request, uuid
key = json.load(open(os.path.expanduser('~/.pig/agent/auth.json')))['synthetic']['key']
H = {'x-api-key': key, 'content-type': 'application/json', 'anthropic-version': '2023-06-01'}
models = json.load(urllib.request.urlopen('https://api.synthetic.new/v1/models'))['data']
def call(model, text, stream):
    body = json.dumps({'model': model, 'max_tokens': 1, 'stream': stream, 'messages': [{'role': 'user', 'content': text}]}).encode()
    try:
        r = urllib.request.urlopen(urllib.request.Request('https://api.synthetic.new/anthropic/v1/messages', body, H), timeout=180)
    except urllib.error.HTTPError as e:
        return {'error': e.code, 'body': e.read()[:300].decode('utf8','replace')}
    if not stream:
        u = json.load(r)['usage']; return u
    u = {}
    for line in r:
        line = line.decode().strip()
        if line.startswith('data:'):
            e = json.loads(line[5:])
            if e['type'] == 'message_start': u.update(e['message']['usage'])
            if e['type'] == 'message_delta' and e.get('usage'): u.update(e['usage'])
    return u
p = lambda u: (u.get('input_tokens') or 0) + (u.get('cache_read_input_tokens') or 0)
for m in models:
    for name in ('prose', 'json'):
        a = call(m['id'], uuid.uuid4().hex + ' ' + open(name + '.txt').read(), False)
        b = call(m['id'], uuid.uuid4().hex + ' ' + open(name + '.txt').read(), True)
        f = round(p(b) / p(a), 4) if 'error' not in a and 'error' not in b and p(a) else None
        print(json.dumps({'id': m['id'], 'repo': m.get('hugging_face_id'), 'text': name, 'nonstream': a, 'stream': b, 'factor': f}), flush=True)
