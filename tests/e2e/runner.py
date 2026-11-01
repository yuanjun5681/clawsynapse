#!/usr/bin/env python3
"""Real multi-Bot acceptance: host daemons/OpenCode and private Docker JetStream.

A and B use separate real OpenCode bindings. R is an external network peer using
our built-in adapter, not a claimed real Harness. No provider approvals or --auto
flags are supplied. This runner never builds binaries or touches a user daemon.
"""
import argparse
import hashlib
import json
import os
import pathlib
import shutil
import signal
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
import uuid

HERE = pathlib.Path(__file__).resolve().parent


class APIError(RuntimeError):
    def __init__(self, status, body):
        super().__init__(f"HTTP {status}: {body}")
        self.status = status
        self.body = body


def free_port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def executable(value):
    path = shutil.which(value)
    if path is None:
        raise RuntimeError(f"executable unavailable: {value}")
    return str(pathlib.Path(path).resolve())


class Harness:
    def __init__(self, args):
        self.args = args
        self.root = pathlib.Path(tempfile.mkdtemp(prefix="clawsynapse-real-e2e-")).resolve()
        self.project = "clawsynapse-e2e-" + uuid.uuid4().hex[:12]
        self.end = time.monotonic() + args.deadline
        self.env = {k: v for k, v in os.environ.items() if not k.startswith("CLAWSYNAPSE_")}
        self.env["CLAWSYNAPSE_E2E_NATS_PORT"] = str(free_port())
        self.processes = {}
        self.logs = []
        self.daemons = {}
        self.passes = []
        self.report = {"model": args.model, "root": str(self.root), "project": self.project,
                       "harnesses": {"A": "real OpenCode", "B": "real OpenCode", "R": "built-in network peer"}}
        binaries = self.root / "bin"
        binaries.mkdir(mode=0o700)
        # This is a symlink to the actual installed CLI, never an executable fixture.
        (binaries / "opencode").symlink_to(args.opencode_bin)
        self.env["PATH"] = str(binaries) + os.pathsep + self.env.get("PATH", "")

    def remaining(self, maximum):
        remaining = self.end - time.monotonic()
        if remaining <= 0:
            raise TimeoutError("acceptance deadline exceeded")
        return min(maximum, remaining)

    def command(self, *args, check=True, timeout=60):
        result = subprocess.run(args, cwd=self.root, env=self.env, capture_output=True,
                                text=True, timeout=self.remaining(timeout))
        if check and result.returncode:
            raise RuntimeError(f"command failed: {args}\n{result.stdout}\n{result.stderr}")
        return result

    def compose(self, *args, check=True):
        return self.command("docker", "compose", "-p", self.project, "-f", str(HERE / "compose.yml"),
                            *args, check=check, timeout=120)

    def eventually(self, fn, description, timeout=40):
        end = time.monotonic() + self.remaining(timeout)
        last = None
        while time.monotonic() < end:
            for name, proc in self.processes.items():
                if proc.poll() is not None:
                    raise RuntimeError(f"daemon {name} exited with {proc.returncode}")
            try:
                result = fn()
                if result:
                    return result
            except (APIError, urllib.error.URLError, ConnectionError, OSError, KeyError) as error:
                last = error
            time.sleep(0.2)
        raise AssertionError(f"{description}: {last}")

    def passed(self, description):
        self.passes.append(description)
        print("PASS:", description, flush=True)

    def prepare_daemon(self, name):
        root = self.root / name
        root.mkdir(mode=0o700)
        config = {"schemaVersion": 2, "dataDir": str(root),
                  "localApiAddr": f"127.0.0.1:{free_port()}",
                  "adminTokenPath": str(root / "admin.token"),
                  "natsServers": [f"nats://127.0.0.1:{self.env['CLAWSYNAPSE_E2E_NATS_PORT']}"],
                  "maxConcurrent": 2, "logLevel": "debug", "logFormat": "json"}
        path = root / "config.json"
        path.write_text(json.dumps(config))
        path.chmod(0o600)
        self.daemons[name] = {"root": root, "config": path, "url": "http://" + config["localApiAddr"]}
        self.start_daemon(name)

    def start_daemon(self, name):
        spec = self.daemons[name]
        log = open(spec["root"] / "daemon.log", "ab", buffering=0)
        self.logs.append(log)
        command = [self.args.daemon_bin, "--config", str(spec["config"])]
        assert not any(arg.startswith("--auto") for arg in command)
        self.processes[name] = subprocess.Popen(command, cwd=spec["root"], env=self.env,
                                                 stdout=log, stderr=log, start_new_session=True)
        self.eventually(lambda: (spec["root"] / "admin.token").exists() and self.api(name, "GET", "/v2/health"),
                        f"daemon {name} health")

    def token(self, daemon, bot=None):
        root = self.daemons[daemon]["root"]
        path = root / "admin.token" if bot is None else root / "bots" / bot / "credentials" / "api.token"
        return path.read_text().strip()

    def api(self, daemon, method, path, body=None, token="admin", raw=False, expected=200):
        headers = {"Content-Type": "application/json"}
        if token is not None:
            headers["Authorization"] = "Bearer " + (self.token(daemon) if token == "admin" else token)
        request = urllib.request.Request(self.daemons[daemon]["url"] + path, method=method,
                                         data=None if body is None else json.dumps(body).encode(), headers=headers)
        try:
            with urllib.request.urlopen(request, timeout=self.remaining(15)) as response:
                status, payload = response.status, response.read()
        except urllib.error.HTTPError as error:
            status, payload = error.code, error.read()
        if status != expected:
            raise APIError(status, payload.decode(errors="replace"))
        if raw:
            return payload
        result = json.loads(payload)
        if expected < 400:
            assert result["ok"], result
        return result.get("data", result)

    def business(self, daemon, bot, method, suffix, body=None, **kwargs):
        return self.api(daemon, method, f"/v2/bots/{bot}/{suffix}", body, **kwargs)

    def cli(self, daemon, *args, bot=None, check=True):
        command = [self.args.cli_bin, "--api-addr", self.daemons[daemon]["url"], "--token-file",
                   str(self.daemons[daemon]["root"] / "admin.token"), "--json"]
        if bot is not None:
            command += ["--bot", bot]
        return self.command(*command, *args, check=check, timeout=25)

    def create(self, daemon, name, adapter):
        binding = {"adapter": adapter, "timeout": f"{self.args.turn_timeout}s"}
        if adapter == "opencode":
            binding["model"] = self.args.model
        body = {"displayName": name, "trustMode": "explicit", "trustAutoApprove": False,
                "heartbeatInterval": "1s", "announceTtl": "6s", "harness": binding,
                "maxConcurrent": 1, "maxQueued": 2}
        bot = self.api(daemon, "POST", "/v2/bots", body, expected=201)["bot"]
        self.transition(daemon, bot["botId"], "start", "running")
        return bot

    def info(self, daemon, bot):
        return self.api(daemon, "GET", f"/v2/bots/{bot}")["bot"]

    def transition(self, daemon, bot, operation, state):
        self.api(daemon, "POST", f"/v2/bots/{bot}/{operation}", expected=202 if operation in ("start", "stop") else 200)
        def settled():
            info = self.info(daemon, bot)
            if info["state"] == "failed":
                raise AssertionError(f"Bot failed: {info}")
            return info if info["state"] == state else None
        return self.eventually(settled, f"{operation} {bot}", timeout=60)

    def peers(self, daemon, bot):
        return self.business(daemon, bot, "GET", "peers")["items"]

    def peer(self, daemon, bot, remote):
        return next((p for p in self.peers(daemon, bot) if p["botId"] == remote), None)

    def trust(self, source_daemon, source, target_daemon, target):
        self.eventually(lambda: self.peer(source_daemon, source, target) and self.peer(target_daemon, target, source), "signed discovery")
        self.eventually(lambda: self.business(source_daemon, source, "POST", "auth/challenge", {"targetBot": target}), "authentication")
        request = self.eventually(lambda: self.business(source_daemon, source, "POST", "trust/request", {"targetBot": target, "reason": "isolated acceptance"}), "trust request")
        request_id = request["requestId"]
        self.eventually(lambda: any(p["requestId"] == request_id for p in self.business(target_daemon, target, "GET", "trust/pending")["items"]), "pending trust")
        self.business(target_daemon, target, "POST", "trust/approve", {"requestId": request_id})
        self.eventually(lambda: self.peer(source_daemon, source, target).get("trustStatus") == "trusted" and self.peer(target_daemon, target, source).get("trustStatus") == "trusted", "bilateral trust")

    def publish(self, source, target, instruction, session, metadata=None):
        def authenticated():
            sender = self.peer("remote", source, target) or {}
            receiver = self.peer("main", target, source) or {}
            return all(peer.get("authStatus") == "authenticated" and peer.get("trustStatus") == "trusted"
                       for peer in (sender, receiver))
        self.eventually(authenticated, "bilateral authentication after discovery/restart")
        body = {"targetBot": target, "type": "task.request", "sessionKey": session,
                "payload": {"instruction": instruction}, "idempotencyKey": uuid.uuid4().hex}
        if metadata is not None:
            body["metadata"] = metadata
        return self.business("remote", source, "POST", "publish", body)["messageId"]

    def request(self, source, request):
        return self.business("remote", source, "GET", "requests/" + request)

    def completed(self, source, request):
        def terminal():
            state = self.request(source, request)
            if state["status"] in ("failed", "indeterminate"):
                raise AssertionError(f"real Harness failed: {state}")
            return state if state["status"] == "completed" else None
        state = self.eventually(terminal, f"real model completion {request}", timeout=self.args.turn_timeout + 20)
        events = state["events"]
        assert events and events[0]["type"] == "run.started" and events[-1]["type"] == "run.completed", events
        assert len({e["runId"] for e in events}) == 1 and all(e["requestId"] == request for e in events), events
        assert [e["seq"] for e in events] == list(range(1, len(events) + 1)), events
        return "".join(e["payload"].get("text", "") for e in events if e["type"] == "run.output.delta")

    def workspace(self, bot, binding=None):
        root = self.daemons["main"]["root"] / "bots" / bot / "workspace"
        return root if binding is None else root / binding

    def sessions(self, bot, binding):
        root = self.daemons["main"]["root"] / "bots" / bot / "sessions" / binding
        return {str(path.relative_to(root)): json.loads(path.read_text())["sessionId"] for path in root.rglob("*.json")}

    def stop_process(self, name, crash=False):
        proc = self.processes.pop(name, None)
        if proc is None or proc.poll() is not None:
            return
        # Capture only this daemon's descendants, including native CLI process
        # groups. Never signal an unrelated server, provider, or user process.
        rows = self.command("ps", "-axo", "pid=,ppid=", timeout=10).stdout.splitlines()
        parents = {int(pid): int(parent) for pid, parent in (row.split() for row in rows)}
        owned = {proc.pid}
        while True:
            children = {pid for pid, parent in parents.items() if parent in owned}
            if children <= owned:
                break
            owned |= children
        groups = set()
        for pid in owned:
            try:
                group = os.getpgid(pid)
                if group in owned and group != os.getpgrp():
                    groups.add(group)
            except ProcessLookupError:
                pass
        if crash:
            for group in groups:
                try:
                    os.killpg(group, signal.SIGKILL)
                except ProcessLookupError:
                    pass
        else:
            proc.terminate()
        try:
            proc.wait(timeout=20)
        except subprocess.TimeoutExpired:
            proc.kill()
            proc.wait(timeout=5)

    def run(self):
        self.compose("up", "-d", "nats")
        port = int(self.env["CLAWSYNAPSE_E2E_NATS_PORT"])
        def broker_ready():
            with socket.create_connection(("127.0.0.1", port), timeout=1):
                return True
        self.eventually(broker_ready, "private JetStream container")
        self.prepare_daemon("main")
        self.prepare_daemon("remote")
        a = self.create("main", "acceptance-A", "opencode")
        b = self.create("main", "acceptance-B", "opencode")
        r = self.create("remote", "external-R", "default")
        A, B, R = a["botId"], b["botId"], r["botId"]
        self.report["bots"] = {"A": a, "B": b, "R": r}
        assert A != B and a["did"] != b["did"] and self.token("main", A) != self.token("main", B)
        assert self.api("main", "GET", "/v2/health")["version"]
        self.passed("independent A/B identities, scoped credentials, and daemon version")

        self.api("main", "GET", "/v2/health", token=None, expected=401)
        scoped = self.token("main", A)
        self.api("main", "GET", "/v2/bots", token=scoped, expected=403)
        self.api("main", "GET", f"/v2/bots/{B}", token=scoped, expected=403)
        self.api("main", "POST", f"/v2/bots/{A}/stop", token=scoped, expected=403)
        self.api("main", "GET", f"/v2/bots/{A}/config", token=scoped, expected=403)
        self.business("main", A, "GET", "messages", token=scoped)
        assert self.cli("main", "health", check=False).returncode != 0
        result = json.loads(self.cli("main", "health", bot=A[:12]).stdout)
        assert result["data"]["self"]["botId"] == A
        self.passed("anonymous and cross-Bot access denied; CLI requires explicit unambiguous Bot")

        self.trust("remote", R, "main", A)
        self.eventually(lambda: self.peer("main", B, R), "B discovers external R")
        assert self.peer("main", B, R).get("trustStatus") != "trusted"
        assert not any(p["botId"] == R for p in self.business("main", B, "GET", "trust")["trusted"])
        self.business("remote", R, "POST", "publish", {"targetBot": B, "message": "must not execute"}, expected=400)
        self.business("main", A, "PUT", f"peers/{R}/attrs", {"alias": "A-only-remote", "labels": {"zone": "isolated"}})
        assert self.peer("main", A, R)["localAlias"] == "A-only-remote"
        assert not self.peer("main", B, R).get("localAlias")
        self.passed("trust and aliases for the same external peer are Bot-local")
        self.trust("remote", R, "main", B)

        secret_a, secret_b = "A_" + uuid.uuid4().hex, "B_" + uuid.uuid4().hex
        session = "same-remote-session"
        requests = []
        for bot, binding, secret in ((A, a["bindingRevision"], secret_a), (B, b["bindingRevision"], secret_b)):
            workspace = str(self.workspace(bot, binding))
            instruction = (f"Remember this secret for this conversation: {secret}. "
                           "Use bash to run exactly `pwd > .e2e-cwd`. This one marker file is authorized in your own workspace. "
                           "Then reply with the secret only. Do not modify any other files.")
            request = self.publish(R, bot, instruction, session, {"large": 9007199254740993, "unicode": "中文🦞"})
            requests.append(request)
            output = self.completed(R, request)
            assert secret in output, output
            assert (self.workspace(bot, binding) / ".e2e-cwd").read_text().strip() == workspace
        native_a, native_b = self.sessions(A, a["bindingRevision"]), self.sessions(B, b["bindingRevision"])
        assert native_a and native_b and not set(native_a.values()) & set(native_b.values()), (native_a, native_b)
        received = self.business("main", A, "GET", "messages?limit=1000")
        command = next(item for item in received["items"] if item["id"] == requests[0])
        assert command["protocolVersion"] == "v2" and command["metadata"] == {"large": 9007199254740993, "unicode": "中文🦞"}
        assert all(item["id"] != requests[1] for item in received["items"])
        self.passed("two real private OpenCode engines: independent native sessions/cwd and correctly routed v2 replies")

        cursor = received["cursor"]
        marker = self.workspace(B, b["bindingRevision"]) / ".e2e-running"
        request = self.publish(R, B, "Use bash to run exactly `printf ready > .e2e-running; sleep 15; pwd`. Then reply B_STILL_RUNNING and the directory. This writes only the authorized test marker in your workspace.", "stop-isolation")
        self.eventually(marker.exists, "real B native tool is executing", timeout=self.args.turn_timeout)
        assert self.request(R, request)["status"] == "running", self.request(R, request)
        queued = [self.publish(R, B, f"Reply exactly QUEUED_{i}. Do not use tools.", f"bounded-queue-{i}") for i in range(3)]
        def bounded_inbox():
            ids = {item["id"] for item in self.business("main", B, "GET", "messages?limit=1000")["items"]}
            return ids if set(queued[:2]) <= ids else None
        queued_ids = self.eventually(bounded_inbox, "two admitted durable queue entries", timeout=5)
        assert queued[2] not in queued_ids, "Bot exceeded one active plus two queued commands"
        assert all(not self.request(R, item)["events"] for item in queued), "queued work started concurrently with the active native turn"
        self.transition("main", A, "stop", "stopped")
        assert self.info("main", B)["state"] == "running"
        assert "B_STILL_RUNNING" in self.completed(R, request)
        for i, pending in enumerate(queued):
            assert f"QUEUED_{i}" in self.completed(R, pending)
        self.passed("real execution concurrency is Bot-bounded and excess commands stay broker-side until capacity frees")
        stopped = self.business("main", A, "GET", f"messages?after={cursor}")
        assert stopped["cursor"] == cursor and not stopped["items"], stopped
        self.transition("main", A, "start", "running")
        request = self.publish(R, A, "What exact secret did I ask you to remember earlier in this conversation? Reply with that secret only. Do not use tools.", session)
        output = self.completed(R, request)
        assert secret_a in output and secret_b not in output, output
        assert self.sessions(A, a["bindingRevision"]) == native_a
        assert self.peer("main", A, R)["localAlias"] == "A-only-remote"
        replay = self.business("main", A, "GET", f"messages?after={cursor}")
        assert replay["cursor"] > cursor and replay["items"][0]["cursor"] == cursor + 1, replay
        self.passed("stopping A preserves active B execution; fresh A runtime resumes its own session and durable cursor")

        self.transition("main", A, "stop", "stopped")
        config = self.api("main", "GET", f"/v2/bots/{A}/config")["config"]
        config["harness"]["timeout"] = f"{self.args.turn_timeout + 1}s"
        updated = self.api("main", "PUT", f"/v2/bots/{A}/config", config)["bot"]
        assert updated["bindingRevision"] != a["bindingRevision"]
        self.api("main", "PUT", f"/v2/bots/{A}/config", config, expected=409)
        self.transition("main", A, "start", "running")
        request = self.publish(R, A, "Use bash to run exactly `pwd > .e2e-cwd`. This one marker file is authorized in your own workspace. Then reply NEW_BINDING only. Do not modify other files.", session)
        output = self.completed(R, request)
        assert "NEW_BINDING" in output, output
        assert (self.workspace(A, updated["bindingRevision"]) / ".e2e-cwd").read_text().strip() == str(self.workspace(A, updated["bindingRevision"]))
        fresh = self.sessions(A, updated["bindingRevision"])
        assert fresh and not set(fresh.values()) & set(native_a.values())
        self.passed("binding cutover changes cwd and native session namespace; stale config revisions rejected")

        crash_marker = self.workspace(A, updated["bindingRevision"]) / ".e2e-crash-count"
        request = self.publish(R, A, "Use bash to run exactly `printf x >> .e2e-crash-count; sleep 30`. Do not retry that command. Then reply CRASH_TEST_DONE. The marker file is authorized only in your own workspace.", "crash-once")
        self.eventually(crash_marker.exists, "actual Harness admitted before daemon crash", timeout=self.args.turn_timeout)
        self.stop_process("main", crash=True)
        assert crash_marker.read_bytes() == b"x"
        self.start_daemon("main")
        self.eventually(lambda: self.info("main", A)["state"] == "running" and self.info("main", B)["state"] == "running", "desired running Bots recovered", timeout=60)
        state = self.eventually(lambda: (state if (state := self.request(R, request))["status"] == "indeterminate" else None), "uncertain execution recovered without rerun", timeout=40)
        assert state["events"][-1]["payload"]["indeterminate"] and crash_marker.read_bytes() == b"x", state
        assert sum(event["type"] == "run.started" for event in state["events"]) == 1, state
        self.passed("daemon SIGKILL/restart preserves desired state and reports admitted execution uncertain, never blindly reruns")

        content = b"shared immutable file entity\n" + uuid.uuid4().hex.encode()
        source = self.root / "authorized-import.txt"
        source.write_bytes(content)
        imported = self.business("remote", R, "POST", "transfer/import", {"filePath": str(source)}, expected=201)["transfer"]["transferId"]
        self.business("main", A, "POST", "transfer/import", {"filePath": str(source)}, token=scoped, expected=403)
        grants = {}
        copies = {}
        for bot in (A, B):
            transfer = self.business("remote", R, "POST", f"transfer/{imported}/forward", {"targetBot": bot})["transferId"]
            def received_file():
                data = self.business("main", bot, "GET", "transfer/" + transfer)["transfer"]
                if data["status"] != "completed":
                    return None
                # Dot-prefixed files are in-flight copies; only the atomic final
                # transfer ID publication proves a workspace file is complete.
                for candidate in self.workspace(bot).glob("*/transfers/*"):
                    if not candidate.name.startswith(".") and candidate.is_file() and not candidate.is_symlink():
                        if candidate.read_bytes() == content:
                            return data, candidate
                return None
            record, path = self.eventually(received_file, "Bot-scoped materialized file", timeout=60)
            assert path.is_relative_to(self.workspace(bot)) and path.read_bytes() == content, record
            assert self.business("main", bot, "GET", f"transfer/{transfer}/file", raw=True) == content
            grants[bot] = transfer
            copies[bot] = path
        digest = hashlib.sha256(content).hexdigest()
        objects = list((self.daemons["main"]["root"] / "blobs" / "sha256").rglob(digest))
        assert len(objects) == 1 and objects[0].read_bytes() == content, objects
        copies[A].write_bytes(b"independent writable A copy\n")
        assert copies[B].read_bytes() == content and objects[0].read_bytes() == content
        assert self.business("main", A, "GET", f"transfer/{grants[A]}/file", raw=True) == content
        refs = []
        for bot, transfer in grants.items():
            ref_path = self.daemons["main"]["root"] / "bots" / bot / "transfers" / (transfer + ".json")
            ref = json.loads(ref_path.read_text())
            assert ref["botId"] == bot and ref["digest"] == digest, ref
            refs.append(str(ref_path))
        self.business("main", A, "GET", f"transfer/{grants[B]}/file", expected=404)
        self.business("main", A, "DELETE", "transfer/" + grants[A])
        assert self.business("main", B, "GET", f"transfer/{grants[B]}/file", raw=True) == content
        self.transition("main", B, "stop", "stopped")
        self.transition("main", B, "archive", "archived")
        self.stop_process("main")
        self.start_daemon("main")
        self.eventually(lambda: self.info("main", A)["state"] == "running", "A restarts after daemon lifecycle")
        assert self.info("main", B)["archived"] and self.info("main", B)["state"] == "archived"
        assert self.business("main", B, "GET", f"transfer/{grants[B]}/file", raw=True) == content
        assert objects[0].exists()
        self.passed("one shared immutable entity, independent grants/workspace copies, and archived reference retention across restart")
        self.business("main", A, "POST", "trust/revoke", {"targetBot": R})
        archived_trust = self.business("main", B, "GET", "trust")
        assert any(peer["botId"] == R for peer in archived_trust["trusted"]), archived_trust
        assert not any(peer["botId"] == R for peer in self.business("main", A, "GET", "trust")["trusted"])
        self.passed("revoking A's remote trust leaves archived B's trust unchanged")
        # A missing archived reference root is damage, not zero grants. Startup
        # collection and read-only recovery must preserve the sole shared entity.
        self.stop_process("main")
        reference_root = self.daemons["main"]["root"] / "bots" / B / "transfers"
        saved_references = self.root / "archived-B-reference-backup"
        reference_root.rename(saved_references)
        self.start_daemon("main")
        self.eventually(lambda: self.info("main", A)["state"] == "running", "healthy A survives damaged archived B")
        self.business("main", B, "GET", f"transfer/{grants[B]}/file", expected=500)
        assert not reference_root.exists() and objects[0].exists()
        self.stop_process("main")
        saved_references.rename(reference_root)
        self.start_daemon("main")
        assert self.business("main", B, "GET", f"transfer/{grants[B]}/file", raw=True) == content
        self.passed("missing archived reference directory blocks recovery and GC without recreating history or deleting content")
        self.report.update({"passes": self.passes, "nativeSessions": {"A": native_a, "B": native_b, "A-new-binding": fresh},
                            "fileReferences": refs, "blob": str(objects[0]), "result": "passed"})
        (self.root / "report.json").write_text(json.dumps(self.report, indent=2) + "\n")
        print("E2E COMPLETE:", self.root / "report.json", flush=True)

    def cleanup(self):
        # Cleanup still runs after the acceptance deadline has elapsed.
        self.end = max(self.end, time.monotonic() + 120)
        for name in list(self.processes):
            self.stop_process(name)
        for log in self.logs:
            log.close()
        if self.args.keep:
            print(f"Kept private broker and artifacts: {self.root}")
            print(f"Cleanup broker: CLAWSYNAPSE_E2E_NATS_PORT={self.env['CLAWSYNAPSE_E2E_NATS_PORT']} docker compose -p {self.project} -f {HERE / 'compose.yml'} down -v")
        else:
            self.compose("down", "-v", "--remove-orphans", check=False)
            shutil.rmtree(self.root)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--daemon-bin", default=os.environ.get("CLAWSYNAPSED_BIN", "clawsynapsed"))
    parser.add_argument("--cli-bin", default=os.environ.get("CLAWSYNAPSE_BIN", "clawsynapse"))
    parser.add_argument("--opencode-bin", default=os.environ.get("OPENCODE_BIN", "opencode"))
    parser.add_argument("--model", default=os.environ.get("E2E_OPENCODE_MODEL", "zai-coding-plan/glm-5.3-flash"))
    parser.add_argument("--turn-timeout", type=int, default=150)
    parser.add_argument("--deadline", type=int, default=1200)
    parser.add_argument("--keep", action="store_true", help="keep only the private broker and artifact root; host daemons always stop")
    args = parser.parse_args()
    if args.turn_timeout < 40 or args.deadline < 120:
        parser.error("turn timeout must be >=40s and overall deadline >=120s")
    args.daemon_bin, args.cli_bin, args.opencode_bin = map(executable, (args.daemon_bin, args.cli_bin, args.opencode_bin))
    executable("docker")
    harness = Harness(args)
    print("Private acceptance root:", harness.root, flush=True)
    try:
        harness.run()
    except BaseException:
        for name, spec in harness.daemons.items():
            path = spec["root"] / "daemon.log"
            if path.exists():
                print(f"--- {name} daemon log (last 100 lines) ---", file=sys.stderr)
                print("\n".join(path.read_text(errors="replace").splitlines()[-100:]), file=sys.stderr)
        raise
    finally:
        harness.cleanup()


if __name__ == "__main__":
    main()
