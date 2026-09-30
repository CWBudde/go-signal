#!/usr/bin/env python3
"""Example daemon client: JSON requests and SSE with a persistent resume cursor."""

import argparse
from http.client import HTTPException
import json
import os
from pathlib import Path
import sys
import tempfile
import time
from urllib.error import HTTPError, URLError
from urllib.parse import urlencode
from urllib.request import Request, urlopen


def arguments():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--url", default="http://127.0.0.1:8766")
    parser.add_argument("--token-file", type=Path, required=True)
    commands = parser.add_subparsers(dest="command", required=True)
    commands.add_parser("health")
    listing = commands.add_parser("list")
    listing.add_argument("--chat")
    listing.add_argument("--cursor")
    listing.add_argument("--since")
    listing.add_argument("--limit", type=int)
    sending = commands.add_parser("send")
    sending.add_argument("recipients", nargs="+")
    sending.add_argument("--text", required=True)
    marking = commands.add_parser("mark-read")
    marking.add_argument("--chat")
    marking.add_argument("--cursor")
    streaming = commands.add_parser("stream")
    streaming.add_argument("--chat")
    streaming.add_argument("--cursor-file", type=Path, required=True)
    return parser.parse_args()


def request(base, token, path, query=None, payload=None, cursor=None):
    suffix = "?" + urlencode(query) if query else ""
    headers = {"Authorization": "Bearer " + token}
    data = None
    if payload is not None:
        data = json.dumps(payload).encode("utf-8")
        headers["Content-Type"] = "application/json"
    if cursor is not None:
        headers["Last-Event-ID"] = cursor
    return Request(base.rstrip("/") + path + suffix, data=data, headers=headers)


def save_cursor(path, cursor):
    # Atomic replacement avoids a partially written cursor after interruption.
    # Use separate cursor files for separate accounts and chat filters.
    with tempfile.NamedTemporaryFile(mode="w", dir=path.parent, delete=False) as out:
        temporary = Path(out.name)
        try:
            out.write(cursor + "\n")
            out.flush()
            os.fsync(out.fileno())
        except BaseException:
            temporary.unlink(missing_ok=True)
            raise
    try:
        os.replace(temporary, path)
    finally:
        temporary.unlink(missing_ok=True)


def sse_events(response):
    event = "message"
    event_id = None
    data = []
    for raw in response:
        line = raw.decode("utf-8").rstrip("\r\n")
        if not line:
            if data:
                yield event, event_id, "\n".join(data)
            event, event_id, data = "message", None, []
            continue
        if line.startswith(":"):
            continue
        field, _, value = line.partition(":")
        if value.startswith(" "):
            value = value[1:]
        if field == "event":
            event = value
        elif field == "id":
            event_id = value
        elif field == "data":
            data.append(value)


def stream(opts, token):
    path = opts.cursor_file
    cursor = path.read_text().strip() if path.exists() else None
    query = {"chat": opts.chat} if opts.chat else {}
    # A new cursor file replays all retained history. A saved ID takes precedence.
    query["cursor"] = "0"
    delay = 1
    while True:
        try:
            req = request(opts.url, token, "/v1/events", query=query, cursor=cursor)
            with urlopen(req, timeout=60) as response:
                for event, event_id, data in sse_events(response):
                    if event == "ready":
                        if event_id is not None and cursor is None:
                            save_cursor(path, event_id)
                            cursor = event_id
                        continue
                    if event != "inbox" or event_id is None or event_id == cursor:
                        continue
                    entry = json.loads(data)
                    if entry["id"] != event_id:
                        raise ValueError("inbox ID does not match SSE ID")
                    # Replace this print with your bot's work. Persist only after success.
                    # Work and cursor saving are not transactional: deduplicate IDs in your
                    # application's own durable state if repeated side effects matter.
                    print(json.dumps(entry), flush=True)
                    save_cursor(path, event_id)
                    cursor = event_id
                    delay = 1
        except HTTPError as error:
            if error.code < 500:
                raise
            print("stream unavailable: " + str(error), file=sys.stderr)
        except (URLError, TimeoutError, ConnectionError, HTTPException) as error:
            print("stream disconnected: " + str(error), file=sys.stderr)
        # Only reconnect reads. Never retry a POST automatically.
        time.sleep(delay)
        delay = min(delay * 2, 30)


def main():
    opts = arguments()
    token = opts.token_file.read_text().strip()
    if not token:
        raise ValueError("empty token file")
    if opts.command == "stream":
        stream(opts, token)
        return 0
    query = None
    payload = None
    if opts.command == "health":
        path = "/v1/health"
    elif opts.command == "list":
        path = "/v1/messages"
        query = {
            name: getattr(opts, name)
            for name in ("chat", "cursor", "since", "limit")
            if getattr(opts, name) is not None
        }
    elif opts.command == "send":
        path = "/v1/messages"
        payload = {"recipients": opts.recipients, "text": opts.text}
    else:
        path = "/v1/mark-read"
        payload = {
            name: getattr(opts, name)
            for name in ("chat", "cursor")
            if getattr(opts, name) is not None
        }
    with urlopen(request(opts.url, token, path, query, payload), timeout=60) as response:
        result = json.load(response)
    print(json.dumps(result, indent=2))
    return 1 if result.get("ok") is False else 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except KeyboardInterrupt:
        sys.exit(0)
    except HTTPError as error:
        print(error.read().decode("utf-8", errors="replace"), file=sys.stderr)
        sys.exit(1)
    except (OSError, ValueError) as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)
