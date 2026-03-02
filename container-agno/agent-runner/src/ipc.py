"""IPC polling for follow-up messages and close sentinel detection."""

import asyncio
import json
import os
import sys

from .config import IPC_INPUT_DIR, IPC_INPUT_CLOSE_SENTINEL, IPC_POLL_MS


def log(message: str) -> None:
    """Log to stderr (container captures stderr for debug)."""
    print(f"[agent-runner] {message}", file=sys.stderr, flush=True)


def should_close() -> bool:
    """Check for _close sentinel file. Returns True if found (and removes it)."""
    if os.path.exists(IPC_INPUT_CLOSE_SENTINEL):
        try:
            os.unlink(IPC_INPUT_CLOSE_SENTINEL)
        except OSError:
            pass
        return True
    return False


def drain_ipc_input() -> list[str]:
    """Read and delete all pending IPC input messages.

    Returns list of message texts, or empty list.
    """
    try:
        os.makedirs(IPC_INPUT_DIR, exist_ok=True)
        files = sorted(f for f in os.listdir(IPC_INPUT_DIR) if f.endswith(".json"))
        messages: list[str] = []

        for filename in files:
            filepath = os.path.join(IPC_INPUT_DIR, filename)
            try:
                with open(filepath) as f:
                    data = json.load(f)
                os.unlink(filepath)
                if data.get("type") == "message" and data.get("text"):
                    messages.append(data["text"])
            except (json.JSONDecodeError, OSError) as e:
                log(f"Failed to process input file {filename}: {e}")
                try:
                    os.unlink(filepath)
                except OSError:
                    pass

        return messages
    except OSError as e:
        log(f"IPC drain error: {e}")
        return []


async def wait_for_ipc_message() -> str | None:
    """Async wait for the next IPC message or close sentinel.

    Returns the message text(s) joined by newline, or None if _close detected.
    Polls every 500ms (matching TypeScript version).
    """
    while True:
        if should_close():
            return None

        messages = drain_ipc_input()
        if messages:
            return "\n".join(messages)

        await asyncio.sleep(IPC_POLL_MS)
