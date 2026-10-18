#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-or-later
"""Finite local fixture acceptance. No image pulls, provider APIs, or grade publication."""
import argparse
import copy
import datetime
import fcntl
import hashlib
import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import time
import uuid

p = argparse.ArgumentParser()
p.add_argument('--cairn-binary', required=True)
p.add_argument('--outfitter-binary', required=True)
p.add_argument('--image', required=True)
p.add_argument('--output', required=True)
a = p.parse_args()
cairn, outfitter = [str(Path(x).resolve()) for x in (a.cairn_binary, a.outfitter_binary)]
sha = lambda b: hashlib.sha256(b).hexdigest()
lock = open('/tmp/educloud-docker-acceptance.lock', 'a')
fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
root = (Path('.runtime') / ('cairn-outfitter-' + uuid.uuid4().hex)).resolve()
root.mkdir(parents=True, mode=0o700)
state = root / 'broker'
checks, jobs, processes, controls, cleanup = [], [], [], [], []
started = time.monotonic()


def check(name, ok):
    checks.append({'name': name, 'passed': bool(ok)})
    if not ok:
        raise AssertionError(name)


def run(argv, expected=0, **kw):
    result = subprocess.run(argv, capture_output=True, text=True, timeout=50, **kw)
    if result.returncode != expected:
        raise AssertionError((argv[0:3], result.returncode, result.stdout[:1000], result.stderr[:1000]))
    return result.stdout


def broker(verb, *flags, directory=None, expected=0):
    return json.loads(run([outfitter, '--state-dir', str(directory or state), verb, *flags], expected))


def git_fixture(name, files):
    repo, snapshot = root / (name + '-repo'), root / (name + '-snapshot')
    repo.mkdir(); snapshot.mkdir()
    run(['git', 'init', '-q', str(repo)])
    for name, content in files.items():
        (repo / name).write_bytes(content)
        (snapshot / name).write_bytes(content)
    run(['git', '-C', str(repo), 'add', '.'])
    run(['git', '-C', str(repo), '-c', 'user.name=Synthetic Fixture', '-c',
         'user.email=synthetic@example.invalid', '-c', 'commit.gpgsign=false', 'commit', '-qm', 'fixture'])
    return run(['git', '-C', str(repo), 'rev-parse', 'HEAD']).strip(), snapshot


def make_job(name, program, project='synthetic'):
    revision, snapshot = git_fixture(name, {'submission.py': program.encode()})
    config = dict(synthetic_only=True, binary=outfitter, binary_sha256=sha(Path(outfitter).read_bytes()),
                  state_dir=str(state), binding_dir=str(root / 'bindings'), authority_id=authority,
                  project_id=project, job_id=name, submission_revision=revision,
                  policy=dict(repository=dict(host='github', namespace='synthetic', name='instructor'),
                              revision=policy_revision, path='grading.json', sha256=sha(policy_bytes)))
    path = root / (name + '.json')
    path.write_text(json.dumps(config)); path.chmod(0o600)
    job = dict(name=name, config=config, path=path, input=snapshot)
    jobs.append(job)
    return job


def command(job, mode='run'):
    return [cairn, '--config', str(job['path']), '--mode', mode,
            '--input', str(job['input']), '--policy', str(policy_snapshot)]


def invoke(job, mode='run', expected=0):
    return json.loads(run(command(job, mode), expected))


def receipt(job, directory=None):
    return broker('consumer-status', '--consumer', 'cairn-synthetic-v1', '--project-id',
                  job['config']['project_id'], '--job-id', job['name'], directory=directory)


def launch(job):
    proc = subprocess.Popen(command(job), stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    processes.append(proc)
    until = time.monotonic() + 12
    while time.monotonic() < until:
        try:
            if receipt(job)['execution_started']:
                return proc
        except AssertionError:
            pass
        time.sleep(.1)
    raise AssertionError('fixture did not start within 12 seconds')


def wait_clean(job):
    until = time.monotonic() + 40
    while time.monotonic() < until:
        r = receipt(job)
        if r['cleanup_verified'] and not r['unresolved_liability']:
            return r
        time.sleep(.2)
    raise AssertionError('bounded cleanup deadline exceeded')


outcome, problem = 'PASS', None
try:
    check('pinned local image already installed', '@sha256:' in a.image and bool(run(['docker', 'image', 'inspect', a.image])))
    broker('project-init', '--project-id', 'synthetic', '--budget-cpu-seconds', '150')
    broker('project-init', '--project-id', 'denied', '--budget-cpu-seconds', '1')
    authority = broker('recovery-status')['authority_id']
    spec = dict(version='1', image=a.image,
                tests=[dict(name='answer', run='python /input/submission.py', points=10,
                            match=dict(expected='42', trim=True))],
                limits=dict(timeout=30_000_000_000, memory_mb=256, cpus=1, network='none'))
    policy_bytes = json.dumps(spec).encode()
    policy_revision, policy_snapshot = git_fixture('instructor', {'grading.json': policy_bytes})
    good = make_job('correct', 'print(42)\n')
    first = invoke(good)
    check('Cairn deterministic correct score and pinned policy', first['result']['score'] == 10
          and first['result']['policy']['sha256'] == sha(policy_bytes)
          and first['result']['submission_revision'] == good['config']['submission_revision']
          and not first['grade_published'])
    original = receipt(good)
    again = invoke(good)
    check('repeated delivery retains one effect and charge', again == first and receipt(good)['compute'] == original['compute'])
    (good['input'] / 'submission.py').write_text('print(41)\n')
    check('changed input is refused before dispatch', invoke(good, expected=2)['unassessable'])
    for field, value in [('submission_revision', 'a' * 40), ('policy', dict(good['config']['policy'], revision='b' * 40))]:
        changed = copy.deepcopy(good['config']); changed[field] = value
        good['path'].write_text(json.dumps(changed))
        check('changed ' + field + ' is refused on restart', invoke(good, 'status', 2)['unassessable'])
    good['path'].write_text(json.dumps(good['config']))
    shutil.rmtree(good['input'])
    check('restart recovers without original submission', invoke(good, 'status') == first)
    wrong = make_job('wrong', 'print(41)\n')
    result = invoke(wrong)
    check('wrong answer remains failed despite successful compute', result['result']['score'] == 0
          and not result['result']['tests'][0]['passed'] and receipt(wrong)['status'] == 'succeeded')
    controls.append({'fixture': 'wrong', 'expected': 42, 'observed': 41, 'score': 0, 'compute': 'succeeded'})
    nonzero = make_job('nonzero', 'print(42)\nraise SystemExit(1)\n')
    result = invoke(nonzero)
    check('matching output with nonzero exit fails', result['result']['score'] == 0 and result['result']['tests'][0]['exit_code'] == 1)
    cancelled = make_job('cancelled', 'import time\ntime.sleep(60)\nprint(42)\n')
    proc = launch(cancelled)
    check('explicit cancellation never publishes result', 'result' not in invoke(cancelled, 'cancel'))
    proc.communicate(timeout=40)
    wait_clean(cancelled)
    check('cancelled work remains unassessable', invoke(cancelled, 'status', 2)['unassessable'])
    killed = make_job('client-killed', 'import time\ntime.sleep(60)\nprint(42)\n')
    proc = launch(killed)
    os.kill(proc.pid, signal.SIGKILL)
    proc.communicate(timeout=40)
    shutil.rmtree(killed['input'])
    observed = wait_clean(killed)
    check('client loss and deadline retain charge without grade', observed['compute']['charged_cpu_seconds'] == 30
          and invoke(killed, 'status', 2)['unassessable'])
    denied = make_job('budget-refused', 'print(42)\n', 'denied')
    check('budget refusal is unassessable', invoke(denied, expected=2)['unassessable'])
    check('repeated refusal is status-only and not admitted', invoke(denied, expected=2)['unassessable']
          and broker('project-status', '--project-id', 'denied')['charged_cpu_seconds'] == 0)
    checkpoint = root / 'checkpoint'
    cp = broker('checkpoint', '--output', str(checkpoint))
    held = root / 'held'
    broker('restore', '--checkpoint', str(checkpoint), '--checkpoint-sha256', cp['checkpoint_sha256'], directory=held)
    cfg = copy.deepcopy(good['config']); cfg['state_dir'] = str(held); cfg['recovery_checkpoint_sha256'] = cp['checkpoint_sha256']
    good['path'].write_text(json.dumps(cfg))
    shutil.rmtree(policy_snapshot)
    check('held authority recovers score without policy or source', invoke(good, 'status') == first)
    check('held recovery never activates broker', broker('recovery-status', directory=held)['admission'] == 'held')
    held_record = json.loads((held / 'ledger.json').read_text())['runs'][original['request_id']]
    (held / 'outputs' / (held_record['output_sha256'] + '.json')).unlink()
    missing = invoke(good, 'status', 2)
    check('missing execution output remains unassessable without refund', missing['unassessable']
          and 'result' not in missing and receipt(good, held)['compute'] == original['compute'])
    usage = broker('project-status', '--project-id', 'synthetic')
    check('five bounded jobs consume exactly 150 CPU seconds', usage['charged_cpu_seconds'] == 150)
    owner = json.loads((state / 'ledger.json').read_text())['owner']
    names = []
    for job in jobs[:-1]:
        rid = receipt(job)['request_id']
        name = 'outfitter-' + sha((owner + ':' + rid).encode())[:32]
        names.extend(run(['docker', 'container', 'ls', '--all', '--filter', 'name=^/' + name + '$', '--format', '{{.Names}}']).splitlines())
    check('independent Docker observation finds no fixture containers', not names)
except Exception as exc:
    outcome, problem = 'FAIL', str(exc)
finally:
    for proc in processes:
        if proc.poll() is None:
            try:
                proc.terminate()
                proc.communicate(timeout=3)
            except subprocess.TimeoutExpired:
                proc.kill(); proc.communicate(timeout=3)
    # Exact request-only cleanup; never prune shared Docker or touch pilot state.
    for job in jobs:
        try:
            r = receipt(job)
            if not r['cleanup_verified']:
                broker('cancel', '--request-id', r['request_id'])
                r = wait_clean(job)
            if not r['cleanup_verified'] or r['unresolved_liability']:
                raise AssertionError('cleanup or liability remains unresolved')
            cleanup.append(dict(job=job['name'], status='PASS', cleanup_verified=True,
                                unresolved_liability=False, compute=r['compute']))
        except Exception as exc:
            # Refusal has no durable broker receipt. Only the specifically
            # bounded refusal account can establish this expected absence.
            refused = False
            if job['config']['project_id'] == 'denied':
                try:
                    u = broker('project-status', '--project-id', 'denied')
                    refused = all(u[k] == 0 for k in ['charged_cpu_seconds', 'reserved_cpu_seconds', 'unknown_cpu_seconds']) and u['budget_cpu_seconds'] == 1
                except Exception:
                    refused = False
            if refused:
                cleanup.append(dict(job=job['name'], status='NOT_ADMITTED', evidence='finite refusal account has no reservations, charge or unknown usage'))
            else:
                cleanup.append(dict(job=job['name'], status='FAIL', error=str(exc)[:1000], unresolved=True))
                outcome = 'FAIL'
                problem = problem or 'cleanup failed or outstanding liability remains; see cleanup evidence'
    output = Path(a.output); output.parent.mkdir(parents=True, exist_ok=True)
    source = {str(f): sha(f.read_bytes()) for base in ['internal/syntheticoutfitter', 'cmd/cairn-outfitter-synthetic', 'tools/synthetic-outfitter'] for f in sorted(Path(base).glob('*')) if f.is_file()}
    output.write_text(json.dumps(dict(status=outcome, error=problem, checks=checks, controls=controls, cleanup=cleanup,
        created_at=datetime.datetime.now(datetime.timezone.utc).isoformat(), elapsed_seconds=round(time.monotonic()-started, 2),
        cairn_revision=run(['git', 'rev-parse', 'HEAD']).strip(), source_sha256=source,
        cairn_binary_sha256=sha(Path(cairn).read_bytes()), outfitter_binary_sha256=sha(Path(outfitter).read_bytes()),
        outfitter_accepted_revision='273bb0148fead33c91fcb92e0cc9e0bacdb929c2', image=a.image,
        fixtures=[dict(job=j['name'], submission_revision=j['config']['submission_revision'], policy=j['config']['policy']) for j in jobs],
        limits=dict(cpus=1, memory_mb=256, pids=64, timeout_seconds=30, max_output_bytes=65536, total_cpu_seconds=150),
        scope='Local synthetic only; no publication, service wiring, live provider, real data or second-human acceptance.'), indent=2) + '\n')
    print(json.dumps({'status': outcome, 'checks': len(checks), 'error': problem, 'evidence': str(output)}))
if outcome != 'PASS':
    raise SystemExit(1)
