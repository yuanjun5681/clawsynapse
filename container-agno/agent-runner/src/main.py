"""NanoClaw Agno Agent Runner — core entry point.

Reads ContainerInput JSON from stdin, creates an Agno Agent, and runs
a query loop with IPC polling. Outputs results wrapped in sentinel markers
for the host to parse.

Protocol:
  stdin:  ContainerInput JSON (read until EOF)
  stdout: ---NANOCLAW_OUTPUT_START--- / ---NANOCLAW_OUTPUT_END--- marker pairs
  IPC:    Follow-up messages via /workspace/ipc/input/*.json
          Close sentinel: /workspace/ipc/input/_close
"""

import asyncio
import inspect
import json
import os
import sys
import uuid

from collections.abc import AsyncIterator

from agno.agent import Agent
from agno.compression.manager import CompressionManager
from agno.db.sqlite import SqliteDb
from agno.models.openai.like import OpenAILike
from agno.skills import Skills, LocalSkills
from agno.tools.duckduckgo import DuckDuckGoTools
from pathlib import Path

from agno.tools.file import FileTools
from agno.tools.python import PythonTools
from agno.tools.shell import ShellTools

from .config import (
    GROUP_DIR,
    SESSION_DB_PATH,
    SKILLS_DIR,
    load_model_config,
    load_nanoclaw_config,
)
from .ipc import drain_ipc_input, log, should_close, wait_for_ipc_message
from .prompts import build_system_prompt
from .tools import get_ipc_tools, set_context


OUTPUT_START_MARKER = "---NANOCLAW_OUTPUT_START---"
OUTPUT_END_MARKER = "---NANOCLAW_OUTPUT_END---"
STREAM_FLUSH_INTERVAL_SEC = 0.08
STREAM_FLUSH_MIN_CHARS = 120


def write_output(status: str, result: str | None, new_session_id: str | None = None, error: str | None = None) -> None:
    """Write a result wrapped in sentinel markers to stdout."""
    output = {"status": status, "result": result}
    if new_session_id:
        output["newSessionId"] = new_session_id
    if error:
        output["error"] = error
    print(OUTPUT_START_MARKER, flush=True)
    print(json.dumps(output), flush=True)
    print(OUTPUT_END_MARKER, flush=True)


def read_stdin() -> dict:
    """Read full JSON from stdin (blocks until EOF)."""
    data = sys.stdin.read()
    return json.loads(data)


def load_skills() -> Skills | None:
    """Load Agno skills from the mounted skills directory."""
    skills_path = Path(SKILLS_DIR)
    if not skills_path.is_dir():
        log(f"Skills directory not found: {SKILLS_DIR}")
        return None

    skill_dirs = [d for d in skills_path.iterdir() if d.is_dir() and (d / "SKILL.md").exists()]
    if not skill_dirs:
        log("No skills found in skills directory")
        return None

    try:
        skills = Skills(loaders=[LocalSkills(str(SKILLS_DIR))])
        skill_names = skills.get_skill_names()
        log(f"Loaded {len(skill_names)} skill(s): {', '.join(skill_names)}")
        return skills
    except Exception as e:
        log(f"Failed to load skills: {e}")
        return None


def create_agent(
    model_config,
    system_prompt: str,
    ipc_tools: list,
    session_id: str,
) -> Agent:
    """Create and configure the Agno Agent."""
    model = OpenAILike(
        id=model_config.model_id,
        api_key=model_config.api_key,
        base_url=model_config.base_url,
        temperature=model_config.temperature,
        max_tokens=model_config.max_tokens,
    )

    skills = load_skills()

    compression_manager = CompressionManager(
        model=model,
        compress_token_limit=80000,
        compress_tool_results_limit=3,
    )

    agent = Agent(
        model=model,
        tools=[
            FileTools(base_dir=Path(GROUP_DIR)),
            PythonTools(base_dir=Path(GROUP_DIR)),
            ShellTools(base_dir=GROUP_DIR),
            DuckDuckGoTools(),
            *ipc_tools,
        ],
        skills=skills,
        instructions=system_prompt,
        db=SqliteDb(
            session_table="agent_sessions",
            db_file=SESSION_DB_PATH,
        ),
        session_id=session_id,
        add_history_to_context=True,
        add_datetime_to_context=True,
        timezone_identifier=os.environ.get("TZ", "UTC"),
        num_history_runs=5,
        compress_tool_results=True,
        compression_manager=compression_manager,
        markdown=False,
        stream=False,
    )

    return agent


def _is_async_iterator(value: object) -> bool:
    """Return True when a value can be consumed with `async for`."""
    return isinstance(value, AsyncIterator) or hasattr(value, "__aiter__")


def _extract_text_chunk(event: object) -> str | None:
    """Extract text chunk from a RunOutputEvent-like object."""
    content = getattr(event, "content", None)
    if isinstance(content, str) and content:
        return content
    return None


async def run_agent_loop(
    agent: Agent,
    initial_prompt: str,
    session_id: str,
    is_scheduled_task: bool = False,
) -> None:
    """Main query loop: run agent → wait for IPC → repeat."""
    prompt = initial_prompt

    while True:
        log(f"Starting query (session: {session_id})...")

        try:
            run_result = agent.arun(
                prompt,
                session_id=session_id,
                stream=False,
            )
            response_or_stream = await run_result if inspect.isawaitable(run_result) else run_result

            if _is_async_iterator(response_or_stream):
                chunk_buffer: list[str] = []
                buffered_chars = 0
                preview = ""
                stream_chunk_count = 0
                loop = asyncio.get_running_loop()
                last_flush_at = loop.time()

                def flush_buffer() -> None:
                    nonlocal chunk_buffer, buffered_chars, last_flush_at
                    if not chunk_buffer:
                        return
                    chunk_text = "".join(chunk_buffer)
                    chunk_buffer = []
                    buffered_chars = 0
                    last_flush_at = loop.time()
                    if chunk_text:
                        write_output("success", chunk_text, session_id)

                async for event in response_or_stream:
                    text_chunk = _extract_text_chunk(event)
                    if not text_chunk:
                        continue

                    stream_chunk_count += 1
                    if len(preview) < 200:
                        preview += text_chunk[: 200 - len(preview)]

                    chunk_buffer.append(text_chunk)
                    buffered_chars += len(text_chunk)

                    now = loop.time()
                    if (
                        buffered_chars >= STREAM_FLUSH_MIN_CHARS
                        or now - last_flush_at >= STREAM_FLUSH_INTERVAL_SEC
                    ):
                        flush_buffer()

                flush_buffer()
                log(
                    "Query done (streaming). "
                    f"Chunks: {stream_chunk_count}, "
                    f"Preview: {preview if preview else 'None'}"
                )
            else:
                response = response_or_stream
                result_text: str | None = None
                get_content = getattr(response, "get_content_as_string", None)
                if callable(get_content):
                    value = get_content()
                    if isinstance(value, str):
                        result_text = value
                if result_text == "":
                    result_text = None
                log(f"Query done. Result: {result_text[:200] if result_text else 'None'}")
                if result_text is not None:
                    write_output("success", result_text, session_id)

            # End-of-turn marker for host SSE lifecycle.
            write_output("success", None, session_id)
        except Exception as e:
            error_msg = str(e)
            log(f"Agent error: {error_msg}")
            write_output("error", None, session_id, error_msg)
            return

        # Scheduled tasks: run once and exit — no IPC wait loop
        if is_scheduled_task:
            log("Scheduled task completed, exiting")
            break

        # Check if closed during the run
        if should_close():
            log("Close sentinel detected after query, exiting")
            break

        log("Query ended, waiting for next IPC message...")
        next_message = await wait_for_ipc_message()
        if next_message is None:
            log("Close sentinel received, exiting")
            break

        log(f"Got new message ({len(next_message)} chars), starting new query")
        prompt = next_message


async def main() -> None:
    """Entry point: parse input, create agent, run query loop."""
    try:
        stdin_data = read_stdin()
    except (json.JSONDecodeError, KeyError) as e:
        write_output("error", None, error=f"Failed to parse input: {e}")
        sys.exit(1)

    nc_config = load_nanoclaw_config(stdin_data)
    log(f"Received input for group: {nc_config.group_folder}")

    try:
        model_config = load_model_config()
    except ValueError as e:
        write_output("error", None, error=str(e))
        sys.exit(1)

    # Set IPC tool context
    set_context(nc_config.chat_jid, nc_config.group_folder, nc_config.is_main)

    # Clean up stale _close sentinel
    try:
        os.unlink("/workspace/ipc/input/_close")
    except OSError:
        pass

    # Build initial prompt
    prompt = nc_config.prompt
    if nc_config.is_scheduled_task:
        prompt = (
            "[SCHEDULED TASK - The following message was sent automatically "
            "and is not coming directly from the user or group.]\n\n" + prompt
        )

    # Drain any pending IPC messages into initial prompt
    pending = drain_ipc_input()
    if pending:
        log(f"Draining {len(pending)} pending IPC messages into initial prompt")
        prompt += "\n" + "\n".join(pending)

    # Session ID: reuse or generate new
    session_id = nc_config.session_id or str(uuid.uuid4())

    # Build system prompt and create agent
    system_prompt = build_system_prompt(nc_config)
    ipc_tools = get_ipc_tools(nc_config.is_main)
    agent = create_agent(model_config, system_prompt, ipc_tools, session_id)

    await run_agent_loop(agent, prompt, session_id, nc_config.is_scheduled_task)


if __name__ == "__main__":
    asyncio.run(main())
