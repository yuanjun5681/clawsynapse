#!/usr/bin/env python3
"""Docker e2e harness for ClawSynapse P0: real nats:2.11.9 (JetStream+mTLS),
two daemon containers, simulated Codex fixture. Verifies wire format
consistency, streaming execution, strict failure semantics and access control.
Usage: python3 tests/e2e/runner.py [--keep]
"""
import json, os, pathlib, shutil, socket, subprocess, sys, time

HERE = pathlib.Path(__file__).resolve().parent
REPO = HERE.parent.parent
CERTS = HERE / 'certs'
RUNTIME = HERE / 'runtime'
KEEP = '--keep' in sys.argv
PROJECT = 'clawsynapse-e2e'
BIG_INT = 9007199254740993


def sh(*args, check=True, env=None):
    merged = dict(os.environ)
    if env:
        merged.update(env)
    return subprocess.run(args, cwd=HERE, env=merged, capture_output=True, text=True, check=check)


def compose(*args, check=True):
    return sh('docker', 'compose', '-p', PROJECT, '-f', 'compose.yml', *args, check=check)


def node_exec(node, *args, check=True):
    return compose('exec', '-T', '-e', 'API_TOKEN_PATH=/data/api.token', node, *args, check=check)


def cli(node, *args):
    out = node_exec(node, 'clawsynapse', '--json', *args, check=False)
    assert out.returncode == 0, f'{node} clawsynapse {" ".join(args)}: {out.stdout}{out.stderr}'
    return json.loads(out.stdout)


def api(node, method, path, body=None, auth=True):
    token = node_exec(node, 'cat', '/data/api.token').stdout.strip()
    # curl -f: nonzero exit on HTTP >= 400, body on stdout for success.
    cmd = ['curl', '-fsS', '-X', method, '-H', 'Content-Type: application/json']
    if auth:
        cmd += ['-H', f'Authorization: Bearer {token}']
    if body is not None:
        cmd += ['-d', body]
    cmd.append(f'http://127.0.0.1:18080{path}')
    return node_exec(node, *cmd, check=False)


def eventually(fn, description, timeout=30):
    end = time.monotonic() + timeout
    last = None
    while time.monotonic() < end:
        try:
            result = fn()
            if result:
                return result
        except (AssertionError, KeyError, TypeError, subprocess.CalledProcessError, json.JSONDecodeError) as error:
            last = error
        time.sleep(0.2)
    raise AssertionError(f'{description}: {last}')


def ensure_certs():
    if (CERTS / 'server.crt').exists():
        return
    shutil.rmtree(CERTS, ignore_errors=True)
    CERTS.mkdir()
    sh('openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-keyout', str(CERTS / 'ca.key'),
       '-out', str(CERTS / 'ca.crt'), '-days', '1', '-subj', '/CN=e2e-ca')
    sh('openssl', 'req', '-newkey', 'rsa:2048', '-nodes', '-keyout', str(CERTS / 'server.key'),
       '-out', str(CERTS / 'server.csr'), '-subj', '/CN=nats')
    ext = CERTS / 'server.ext'
    ext.write_text('subjectAltName=DNS:nats,DNS:localhost\n')
    sh('openssl', 'x509', '-req', '-in', str(CERTS / 'server.csr'), '-CA', str(CERTS / 'ca.crt'),
       '-CAkey', str(CERTS / 'ca.key'), '-CAcreateserial', '-days', '1',
       '-out', str(CERTS / 'server.crt'), '-extfile', str(ext))
    sh('openssl', 'req', '-newkey', 'rsa:2048', '-nodes', '-keyout', str(CERTS / 'client.key'),
       '-out', str(CERTS / 'client.csr'), '-subj', '/CN=e2e-node')
    sh('openssl', 'x509', '-req', '-in', str(CERTS / 'client.csr'), '-CA', str(CERTS / 'ca.crt'),
       '-CAkey', str(CERTS / 'ca.key'), '-CAcreateserial', '-days', '1', '-out', str(CERTS / 'client.crt'))


def request_events(node, request_id):
    result = api(node, 'GET', '/v1/messages')
    if result.returncode != 0:
        return []
    data = json.loads(result.stdout)['data']['items']
    return [e for e in data if e.get('requestId') == request_id]


def publish_raw(instruction, agent=None):
    payload = {'targetNode': PEER_B, 'type': 'task.request',
               'payload': {'instruction': instruction}}
    if agent:
        payload['agentId'] = agent
    result = api('node-a', 'POST', '/v1/publish', json.dumps(payload))
    assert result.returncode == 0, f'publish failed: {result.stderr} {result.stdout}'
    return json.loads(result.stdout)['data']['messageId']


def failed_event(request_id, timeout=15):
    return eventually(lambda: next((e for e in request_events('node-a', request_id)
                                    if e['type'] == 'run.failed'), None), f'failure for {request_id}', timeout)


def run():
    global PEER_B
    ensure_certs()
    shutil.rmtree(RUNTIME, ignore_errors=True)
    sh('docker', 'build', '-f', str(HERE / 'Dockerfile.node'), '-t', 'clawsynapse-e2e-node', str(REPO))
    compose('down', '-v', '--remove-orphans', check=False)
    try:
        compose('up', '-d')
        eventually(lambda: socket.create_connection(('127.0.0.1', 24222), timeout=0.5).close() or True,
                   'nats container port')
        eventually(lambda: cli('node-a', 'health')['ok'] and cli('node-b', 'health')['ok'], 'daemon health')
        print('PASS: containers up; nats mTLS and both daemons healthy')

        anon = api('node-a', 'GET', '/v1/health', auth=False)
        assert anon.returncode != 0, 'anonymous request accepted'
        print('PASS: API bearer auth enforced')

        peer_b = eventually(lambda: next((p for p in cli('node-a', 'peers')['data']['items']), None), 'signed discovery')
        PEER_B = peer_b['nodeId']
        def challenge():
            try:
                cli('node-a', 'auth', 'challenge', '--target', PEER_B)
            except AssertionError:
                return False
            return any(p['authStatus'] == 'authenticated' for p in cli('node-a', 'peers')['data']['items'])
        eventually(challenge, 'authentication')
        def request_trust():
            try:
                cli('node-a', 'trust', 'request', '--target', PEER_B)
            except AssertionError:
                return False
            return True
        eventually(request_trust, 'trust request')
        pending = eventually(lambda: cli('node-b', 'trust', 'pending')['data']['items'], 'pending trust')
        cli('node-b', 'trust', 'approve', '--request-id', pending[0]['requestId'])
        eventually(lambda: any(p['trustStatus'] == 'trusted' for p in cli('node-a', 'peers')['data']['items']),
                   'trust established')
        print('PASS: discovery, auth and trust over container network')

        # P1 node attributes: signed announce carries displayName+labels; filters distinguish
        # scalar exact match from set membership; local remarks survive restart and rediscovery.
        labeled = eventually(lambda: next((p for p in cli('node-a', 'peers')['data']['items']
                                           if p.get('displayName') == 'e2e-gateway'), None), 'labeled discovery')
        assert labeled['labels'].get('env') == 'e2e'
        assert labeled['labels'].get('role') == 'gateway'
        assert set(labeled['labels'].get('caps', [])) == {'chat'}
        self_a = cli('node-a', 'health')['data']['self']
        assert self_a['displayName'] == 'e2e-甲节点', self_a
        assert set(self_a['labels']['caps']) == {'chat', 'task.stream'}
        print('PASS: signed announce carries displayName and labels on both nodes')

        assert len(cli('node-a', 'peers', '--label', 'env=e2e')['data']['items']) == 1
        assert len(cli('node-a', 'peers', '--label', 'role=worker')['data']['items']) == 0
        assert len(cli('node-a', 'peers', '--label-has', 'caps=chat')['data']['items']) == 1
        assert len(cli('node-a', 'peers', '--label', 'caps=chat')['data']['items']) == 0
        assert len(cli('node-a', 'peers', '--name', 'gateway')['data']['items']) == 1
        assert len(cli('node-a', 'peers', '--name', 'no-such-node')['data']['items']) == 0
        print('PASS: label exact vs set-member and name filters behave as documented')

        put = api('node-a', 'PUT', f'/v1/peers/{PEER_B}/attrs',
                  json.dumps({'alias': '乙-备注', 'description': 'e2e 机房', 'labels': {'floor': '3'}}))
        assert put.returncode == 0, put.stdout
        got = json.loads(api('node-a', 'GET', f'/v1/peers/{PEER_B}/attrs').stdout)['data']['attrs']
        assert got['alias'] == '乙-备注' and got['description'] == 'e2e 机房'
        assert got['labels'] == {'floor': '3'}
        assert next(p for p in cli('node-a', 'peers')['data']['items'])['localAlias'] == '乙-备注'
        assert api('node-a', 'PUT', f'/v1/peers/{PEER_B}/attrs',
                   json.dumps({'labels': {'caps': 'scalar-not-allowed'}})).returncode != 0
        print('PASS: local remarks stored and reserved caps type rejected')

        cli('node-a', 'peer', 'label', PEER_B, 'zone', 'a', 'b')
        merged = json.loads(api('node-a', 'GET', f'/v1/peers/{PEER_B}/attrs').stdout)['data']['attrs']
        assert merged['labels'] == {'floor': '3', 'zone': ['a', 'b']}, merged
        print('PASS: CLI peer label read-merge-write keeps existing keys')

        compose('restart', 'node-a')
        def remarks_survive_restart():
            peer = next((p for p in cli('node-a', 'peers')['data']['items'] if p['nodeId'] == PEER_B), None)
            return peer if peer and peer.get('localAlias') == '乙-备注' else None
        restarted = eventually(remarks_survive_restart, 'local remarks survive restart and rediscovery', timeout=45)
        assert restarted['labels'].get('env') == 'e2e' and restarted['displayName'] == 'e2e-gateway'
        print('PASS: local remarks survive daemon restart while remote attrs refresh freely')

        assert api('node-a', 'DELETE', f'/v1/peers/{PEER_B}/attrs').returncode == 0
        cleared = json.loads(api('node-a', 'GET', f'/v1/peers/{PEER_B}/attrs').stdout)['data']['attrs']
        assert 'alias' not in cleared and 'labels' not in cleared, cleared
        print('PASS: DELETE clears local remarks')

        # Wire format consistency: large integers, unicode and metadata survive end to end.
        body = {
            'targetNode': PEER_B, 'type': 'task.request',
            'payload': {'instruction': 'smoke_success',
                        'input': {'large': BIG_INT, 'text': '中文 emoji 🦞'}},
            'metadata': {'correlation': BIG_INT},
        }
        sent = api('node-a', 'POST', '/v1/publish', json.dumps(body))
        assert sent.returncode == 0, sent.stderr
        request_id = json.loads(sent.stdout)['data']['messageId']

        received = eventually(lambda: next((m for m in json.loads(api('node-b', 'GET', '/v1/messages').stdout)['data']['items']
                                            if m['id'] == request_id), None), 'structured request received')
        assert received['protocolVersion'] == 'v2'
        assert received['payload']['instruction'] == 'smoke_success'
        assert received['payload']['input'] == {'large': BIG_INT, 'text': '中文 emoji 🦞'}
        assert received['metadata']['correlation'] == BIG_INT
        assert received['expiresAt'] > received['ts'] and received['sig'] and 'content' not in received
        print('PASS: v2 payload, large integers, unicode and metadata identical on both nodes')

        started = eventually(lambda: next((e for e in request_events('node-a', request_id)
                                           if e['type'] == 'run.started'), None), 'run.started', timeout=5)
        assert not any(e['type'] == 'run.completed' for e in request_events('node-a', request_id)), 'completed before delta'
        delta = eventually(lambda: next((e for e in request_events('node-a', request_id)
                                         if e['type'] == 'run.output.delta'), None), 'run.output.delta', timeout=5)
        assert delta['payload']['text'] == 'early e2e answer'
        assert not any(e['type'] == 'run.completed' for e in request_events('node-a', request_id)), 'completed before fixture delay'
        def full_stream():
            evs = [e for e in request_events('node-a', request_id)
                   if e['type'] in ('run.started', 'run.output.delta', 'run.completed')]
            return evs if any(e['type'] == 'run.completed' for e in evs) else None
        stream = eventually(full_stream, 'run.completed')
        assert [e['seq'] for e in stream] == [1, 2, 3], stream
        assert len({e['runId'] for e in stream}) == 1 and all(e['requestId'] == request_id for e in stream)
        print('PASS: streaming events ordered and correlated; delta precedes completion')

        event = failed_event(publish_raw('smoke_fail'))
        assert event['payload']['code'] == 'native_failed', event
        event = failed_event(publish_raw('smoke_unstructured'))
        assert event['payload']['code'] == 'native_schema', event
        event = failed_event(publish_raw('smoke_hang'))
        assert event['payload']['code'] == 'timeout' and event['payload']['indeterminate'] is True, event
        event = failed_event(publish_raw('smoke_big'))
        assert event['payload']['code'] in ('event_limit', 'output_limit'), event
        event = failed_event(publish_raw('ok', agent='not-authorized'))
        assert event['payload']['code'] == 'unsupported_agent', event
        print('PASS: partial failure, unstructured output, timeout, oversized event and unauthorized agent never complete')

        for bad in ['{"targetNode":"x","bogus":1}',
                    '{"targetNode":"a","targetNode":"b"}',
                    '{"targetNode":"x"} {"extra":1}']:
            rejected = api('node-a', 'POST', '/v1/publish', bad)
            assert rejected.returncode != 0, f'invalid body accepted: {bad}'
        print('PASS: strict request decoding rejects unknown fields, duplicate keys and trailing JSON')

        # Codex rejects every named agent by contract, allowed or not.
        allowed = publish_raw('smoke_success', agent='worker-a')
        assert failed_event(allowed)['payload']['code'] == 'unsupported_agent'
        print('PASS: codex agentId routing rejects by contract')

        print('E2E COMPLETE')
        if KEEP:
            print('environment kept: docker compose -p', PROJECT, '-f tests/e2e/compose.yml ...')
            return
    finally:
        if not KEEP:
            compose('down', '-v', '--remove-orphans', check=False)


if __name__ == '__main__':
    try:
        run()
    except Exception:
        logs = compose('logs', '--tail=200', check=False)
        print(logs.stdout)
        print(logs.stderr, file=sys.stderr)
        raise
